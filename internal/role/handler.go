package role

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

const (
	readIPLimit        = 1200
	readAdminLimit     = 600
	mutationIPLimit    = 1200
	mutationAdminLimit = 60
	roleRateWindow     = time.Minute
)

type AdminRoleHandler struct {
	service        *Service
	limiter        RateLimiter
	authHandler    *auth.AdminAuthHandler
	trustedProxies []*net.IPNet
}

func NewAdminRoleHandler(service *Service, limiter RateLimiter, authHandler *auth.AdminAuthHandler, proxies []*net.IPNet) *AdminRoleHandler {
	return &AdminRoleHandler{service: service, limiter: limiter, authHandler: authHandler, trustedProxies: proxies}
}
func (h *AdminRoleHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/roles/v1/list", h.protect("role.read", false, http.HandlerFunc(h.list)))
	mux.Handle("GET /api/admin/roles/v1/detail/{roleId}", h.protect("role.read", false, http.HandlerFunc(h.detail)))
	mux.Handle("POST /api/admin/roles/v1/create", h.protect("role.create", true, http.HandlerFunc(h.create)))
	mux.Handle("PUT /api/admin/roles/v1/update/{roleId}", h.protect("role.update", true, http.HandlerFunc(h.update)))
	mux.Handle("DELETE /api/admin/roles/v1/delete/{roleId}", h.protect("role.delete", true, http.HandlerFunc(h.delete)))
}
func (h *AdminRoleHandler) protect(permission string, mutation bool, next http.Handler) http.Handler {
	return h.authHandler.RequirePermission(permission, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profile, ok := auth.AdminProfileFromRequest(r)
		if !ok {
			writeRoleError(w, "ROLB01001", 401, 0)
			return
		}
		ipLimit, adminLimit := readIPLimit, readAdminLimit
		if mutation {
			ipLimit, adminLimit = mutationIPLimit, mutationAdminLimit
		}
		if err := h.consume(r.Context(), "ip:"+utils.DigestToken(auth.ClientIP(r, h.trustedProxies)), ipLimit); err != nil {
			var limited *rateLimitError
			if errors.As(err, &limited) {
				writeRoleError(w, "ROLB02007", 429, limited.retry)
				return
			}
			writeRoleError(w, "ROLT03001", 500, 0)
			return
		}
		if err := h.consume(r.Context(), "admin:"+utils.DigestToken(profile.ID)+":"+strconv.FormatBool(mutation), adminLimit); err != nil {
			var limited *rateLimitError
			if errors.As(err, &limited) {
				writeRoleError(w, "ROLB02007", 429, limited.retry)
				return
			}
			writeRoleError(w, "ROLT03001", 500, 0)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

type rateLimitError struct{ retry time.Duration }

func (e *rateLimitError) Error() string { return "rate limit exceeded" }
func (h *AdminRoleHandler) consume(ctx context.Context, key string, limit int) error {
	retry, allowed, err := h.limiter.AllowWindow(ctx, "role:"+key, limit, roleRateWindow, time.Now().UTC())
	if err != nil {
		return err
	}
	if !allowed {
		return &rateLimitError{retry}
	}
	return nil
}
func (h *AdminRoleHandler) list(w http.ResponseWriter, r *http.Request) {
	page, err := h.service.List(r.Context(), map[string][]string(r.URL.Query()))
	if err != nil {
		writeBusinessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Code       string     `json:"code"`
		Message    string     `json:"message"`
		Data       []Role     `json:"data"`
		Pagination Pagination `json:"pagination"`
	}{"ROLS00000", "Role list retrieved", page.Roles, page.Pagination})
}
func (h *AdminRoleHandler) detail(w http.ResponseWriter, r *http.Request) {
	value, err := h.service.Detail(r.Context(), r.PathValue("roleId"))
	if err != nil {
		writeBusinessError(w, err)
		return
	}
	writeRoleSuccess(w, "Role detail retrieved", value)
}
func (h *AdminRoleHandler) create(w http.ResponseWriter, r *http.Request) {
	var input CreateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := h.service.Create(r.Context(), input); err != nil {
		writeBusinessError(w, err)
		return
	}
	writeRoleSuccess(w, "Role created", nil)
}
func (h *AdminRoleHandler) update(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeRawJSON(w, r)
	if !ok {
		return
	}
	var changes Changes
	for key, value := range raw {
		switch key {
		case "name":
			var v string
			if json.Unmarshal(value, &v) != nil {
				writeBusinessError(w, ErrInvalid)
				return
			}
			changes.Name = &v
		case "code":
			var v string
			if json.Unmarshal(value, &v) != nil {
				writeBusinessError(w, ErrInvalid)
				return
			}
			changes.Code = &v
		case "description":
			var v *string
			if string(value) != "null" && json.Unmarshal(value, &v) != nil {
				writeBusinessError(w, ErrInvalid)
				return
			}
			changes.Description = &v
		case "status":
			var v string
			if json.Unmarshal(value, &v) != nil {
				writeBusinessError(w, ErrInvalid)
				return
			}
			changes.Status = &v
		case "permissions":
			var v []string
			if json.Unmarshal(value, &v) != nil {
				writeBusinessError(w, ErrInvalid)
				return
			}
			changes.Permissions = &v
		default:
			writeBusinessError(w, ErrInvalid)
			return
		}
	}
	if err := h.service.Update(r.Context(), r.PathValue("roleId"), changes); err != nil {
		writeBusinessError(w, err)
		return
	}
	writeRoleSuccess(w, "Role updated", nil)
}
func (h *AdminRoleHandler) delete(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil || len(body) != 0 {
		writeBusinessError(w, ErrInvalid)
		return
	}
	if err = h.service.Delete(r.Context(), r.PathValue("roleId")); err != nil {
		writeBusinessError(w, err)
		return
	}
	writeRoleSuccess(w, "Role deleted", nil)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if media != "application/json" {
		writeBusinessError(w, ErrInvalid)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		writeBusinessError(w, ErrInvalid)
		return false
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		writeBusinessError(w, ErrInvalid)
		return false
	}
	return true
}
func decodeRawJSON(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if media != "application/json" {
		writeBusinessError(w, ErrInvalid)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	var value map[string]json.RawMessage
	if decoder.Decode(&value) != nil || value == nil {
		writeBusinessError(w, ErrInvalid)
		return nil, false
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		writeBusinessError(w, ErrInvalid)
		return nil, false
	}
	return value, true
}
func writeBusinessError(w http.ResponseWriter, err error) {
	code, status := businessCode(err)
	writeRoleError(w, code, status, 0)
}
func writeRoleSuccess(w http.ResponseWriter, message string, data any) {
	utils.WriteJSON(w, http.StatusOK, utils.ResponseEnvelope{Code: "ROLS00000", Message: message, Data: data})
}
func writeRoleError(w http.ResponseWriter, code string, status int, retry time.Duration) {
	message := map[string]string{"ROLB01001": "Administrator belum terautentikasi.", "ROLB01002": "Akses role tidak diizinkan.", "ROLB02001": "Parameter role tidak valid.", "ROLB02002": "Nama atau kode role sudah digunakan.", "ROLB02003": "Permission tidak dikenal atau tidak aktif.", "ROLB02004": "Role tidak ditemukan.", "ROLB02005": "Role system tidak dapat diubah.", "ROLB02006": "Role masih ditugaskan ke akun aktif.", "ROLB02007": "Batas request role tercapai.", "ROLT03001": "Role service is temporarily unavailable."}[code]
	if status == 429 {
		seconds := int(retry.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	utils.WriteJSON(w, status, utils.ResponseEnvelope{Code: code, Message: message, Data: nil})
}
