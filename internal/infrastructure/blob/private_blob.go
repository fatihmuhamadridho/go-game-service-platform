package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
)

const maxProofBytes = 5 << 20

type Storage struct{}

func NewStorage() *Storage        { return &Storage{} }
func (*Storage) Configured() bool { t, _ := Token(); return t != "" }
func (*Storage) ClientUploadToken(path, payload, callback string, until time.Time) (string, error) {
	t, _ := Token()
	return ClientToken(path, t, until, callback, payload)
}
func (*Storage) VerifyUploadCallback(body []byte, signature string) bool {
	t, _ := Token()
	return VerifyCallback(body, signature, t)
}
func (*Storage) Get(c context.Context, p string) ([]byte, error) { return Get(c, p) }
func (*Storage) Put(c context.Context, p string, b []byte, ct string) (string, error) {
	return Put(c, p, b, ct)
}
func (*Storage) Delete(c context.Context, p string) error { return Delete(c, p) }
func Token() (string, string) {
	t := strings.TrimSpace(os.Getenv("BLOB_READ_WRITE_TOKEN"))
	if t == "" {
		return "", ""
	}
	parts := strings.Split(t, "_")
	if len(parts) < 4 {
		return "", ""
	}
	return t, parts[3]
}
func ClientToken(pathname, token string, validUntil time.Time, callbackURL, tokenPayload string) (string, error) {
	_, store := Token()
	if token == "" || store == "" {
		return "", errors.New("blob storage not configured")
	}
	options := map[string]any{"allowedContentTypes": []string{"image/jpeg", "image/png"}, "maximumSizeInBytes": maxProofBytes, "validUntil": validUntil.UnixMilli(), "addRandomSuffix": false, "pathname": pathname}
	if callbackURL != "" {
		options["onUploadCompleted"] = map[string]any{"callbackUrl": callbackURL, "tokenPayload": tokenPayload}
	}
	data, _ := json.Marshal(options)
	payload := base64.StdEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(payload))
	signed := hex.EncodeToString(mac.Sum(nil)) + "." + payload
	return "vercel_blob_client_" + store + "_" + base64.StdEncoding.EncodeToString([]byte(signed)), nil
}
func VerifyCallback(body []byte, signature, token string) bool {
	if token == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write(body)
	want := []byte(hex.EncodeToString(mac.Sum(nil)))
	got := []byte(signature)
	return hmac.Equal(want, got)
}
func Get(ctx context.Context, pathname string) ([]byte, error) {
	token, store := Token()
	if token == "" || store == "" {
		return nil, errors.New("blob storage not configured")
	}
	u := "https://" + store + ".private.blob.vercel-storage.com/" + strings.TrimLeft(pathname, "/")
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, privateObjectGetError(resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, privateObjectGetError(resp.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxProofBytes+1))
	if e != nil {
		return nil, e
	}
	if err := privateObjectDataError(len(data)); err != nil {
		return nil, err
	}
	return data, nil
}

func privateObjectGetError(status int) error {
	if status == http.StatusNotFound {
		return model.ErrPrivateObjectNotFound
	}
	return fmt.Errorf("private blob get returned %d", status)
}

func privateObjectDataError(size int) error {
	if size == 0 || size > maxProofBytes {
		return model.ErrPrivateObjectInvalid
	}
	return nil
}

func Put(ctx context.Context, pathname string, data []byte, contentType string) (string, error) {
	token, store := Token()
	if token == "" || store == "" {
		return "", errors.New("blob storage not configured")
	}
	q := url.Values{"pathname": {pathname}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPut, "https://vercel.com/api/blob/?"+q.Encode(), bytes.NewReader(data))
	if e != nil {
		return "", e
	}
	randomID := make([]byte, 8)
	if _, e := rand.Read(randomID); e != nil {
		return "", e
	}
	requestID := fmt.Sprintf("%s:%d:%s", store, time.Now().UnixMilli(), hex.EncodeToString(randomID))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-vercel-blob-store-id", store)
	req.Header.Set("x-api-version", "13")
	req.Header.Set("x-api-blob-request-id", requestID)
	req.Header.Set("x-api-blob-request-attempt", "0")
	req.Header.Set("x-vercel-blob-access", "private")
	req.Header.Set("x-content-type", contentType)
	req.Header.Set("x-content-length", fmt.Sprint(len(data)))
	req.Header.Set("x-cache-control-max-age", "60")
	req.Header.Set("Content-Type", contentType)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("private blob put returned %d: %s", resp.StatusCode, string(raw))
	}
	var result struct {
		Pathname string `json:"pathname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode private blob put response: %w", err)
	}
	if result.Pathname == "" || result.Pathname != pathname {
		return "", errors.New("private blob put returned unexpected pathname")
	}
	return result.Pathname, nil
}
func Delete(ctx context.Context, pathname string) error {
	token, store := Token()
	if token == "" || store == "" {
		return errors.New("blob storage not configured")
	}
	data, _ := json.Marshal(map[string]any{"urls": []string{"https://" + store + ".private.blob.vercel-storage.com/" + strings.TrimLeft(pathname, "/")}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "https://vercel.com/api/blob/delete", bytes.NewReader(data))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-vercel-blob-store-id", store)
	req.Header.Set("x-api-version", "13")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("private blob delete returned %d", resp.StatusCode)
	}
	return nil
}
