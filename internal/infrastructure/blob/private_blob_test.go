package blob

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/learn-golang/internal/model"
)

func TestPrivateObjectGetErrorClassifiesNotFound(t *testing.T) {
	if !errors.Is(privateObjectGetError(404), model.ErrPrivateObjectNotFound) {
		t.Fatal("HTTP 404 should be classified as a missing private object")
	}
	if errors.Is(privateObjectGetError(503), model.ErrPrivateObjectNotFound) {
		t.Fatal("HTTP 503 should remain a storage failure")
	}
}

func TestPrivateObjectDataErrorClassifiesSize(t *testing.T) {
	if !errors.Is(privateObjectDataError(0), model.ErrPrivateObjectInvalid) {
		t.Fatal("empty object should be classified as invalid")
	}
	if !errors.Is(privateObjectDataError(maxProofBytes+1), model.ErrPrivateObjectInvalid) {
		t.Fatal("oversized object should be classified as invalid")
	}
	if err := privateObjectDataError(1); err != nil {
		t.Fatalf("valid object size returned error: %v", err)
	}
}

func TestClientTokenEncodesVercelUploadConstraintsAndCallback(t *testing.T) {
	readWriteToken := "vercel_blob_rw_storeid_secret"
	t.Setenv("BLOB_READ_WRITE_TOKEN", readWriteToken)
	validUntil := time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC)
	pathname := "payment-proofs/507f1f77bcf86cd799439011/12345678-1234-1234-1234-123456789abc.jpg"
	callbackURL := "https://example.test/api/proofs/upload"
	clientPayload := `{"paymentId":"507f1f77bcf86cd799439011"}`
	token, err := ClientToken(pathname, readWriteToken, validUntil, callbackURL, clientPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "vercel_blob_client_storeid_") {
		t.Fatalf("client token has unexpected store prefix: %q", strings.Split(token, "_")[3])
	}
	encoded := strings.TrimPrefix(token, "vercel_blob_client_storeid_")
	signedPayload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(signedPayload), ".", 2)
	if len(parts) != 2 {
		t.Fatalf("signed token payload has %d parts, want 2", len(parts))
	}
	mac := hmac.New(sha256.New, []byte(readWriteToken))
	_, _ = mac.Write([]byte(parts[1]))
	if !hmac.Equal([]byte(parts[0]), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		t.Fatal("client token signature does not match read-write token")
	}
	jsonPayload, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Pathname            string   `json:"pathname"`
		AllowedContentTypes []string `json:"allowedContentTypes"`
		MaximumSize         int      `json:"maximumSizeInBytes"`
		ValidUntil          int64    `json:"validUntil"`
		OnUploadCompleted   struct {
			CallbackURL  string `json:"callbackUrl"`
			TokenPayload string `json:"tokenPayload"`
		} `json:"onUploadCompleted"`
	}
	if err := json.Unmarshal(jsonPayload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Pathname != pathname || claims.MaximumSize != maxProofBytes || claims.ValidUntil != validUntil.UnixMilli() {
		t.Fatalf("upload constraints mismatch: %+v", claims)
	}
	if len(claims.AllowedContentTypes) != 2 || claims.AllowedContentTypes[0] != "image/jpeg" || claims.AllowedContentTypes[1] != "image/png" {
		t.Fatalf("allowed content types = %v", claims.AllowedContentTypes)
	}
	if claims.OnUploadCompleted.CallbackURL != callbackURL || claims.OnUploadCompleted.TokenPayload != clientPayload {
		t.Fatalf("callback claims mismatch: %+v", claims.OnUploadCompleted)
	}
}
