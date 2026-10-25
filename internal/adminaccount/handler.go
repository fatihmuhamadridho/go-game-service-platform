package adminaccount

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

const (
	readIPLimit    = 1200
	readAdminLimit = 600
	rateWindow     = time.Minute
	permissionRead = "admin-account.read"
)

type AdminAccountHandler struct {
	service        *Service
	limiter        RateLimiter
	authHandler    *auth.AdminAuthHandler
	trustedProxies []*net.IPNet
}

func NewAdminAccountHandler(service *Service, limiter RateLimiter, authHandler *auth.AdminAuthHandler, proxies []*net.IPNet) *AdminAccountHandler {
	return &AdminAccountHandler{service: service, limiter: limiter, authHandler: authHandler, trustedProxies: proxies}
}

func (h *AdminAccountHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/admin-users/v1/list", h.protect(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/admin/admin-users/v1/detail/{adminId}", h.protect(http.HandlerFunc(h.detail)))
}

func (h *AdminAccountHandler) protect(next http.Handler) http.Handler {
	return h.authHandler.RequirePermission(permissionRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profile, ok := auth.AdminProfileFromRequest(r)
		if !ok {
			writeAccountError(w, "USRB01001", http.StatusUnauthorized, 0)
			return
		}
		// Auth middleware validates the token digest for this route; keep the
		// endpoint fail-closed if it is mounted without that route classification.
		csrfToken := r.Header.Get("X-CSRF-Token")
		if strings.TrimSpace(csrfToken) == "" || len(csrfToken) > 128 {
			writeAccountError(w, "USRB01002", http.StatusForbidden, 0)
			return
		}
		if err := h.consume(r.Context(), "ip:"+utils.DigestToken(auth.ClientIP(r, h.trustedProxies)), readIPLimit); err != nil {
			h.writeLimitOrTechnical(w, err)
			return
		}
		if err := h.consume(r.Context(), "admin:"+utils.DigestToken(profile.ID), readAdminLimit); err != nil {
			h.writeLimitOrTechnical(w, err)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

type rateLimitError struct{ retry time.Duration }

func (e *rateLimitError) Error() string { return "rate limit exceeded" }

func (h *AdminAccountHandler) consume(ctx context.Context, key string, limit int) error {
	retry, allowed, err := h.limiter.AllowWindow(ctx, "admin-account:"+key, limit, rateWindow, time.Now().UTC())
	if err != nil {
		return err
	}
	if !allowed {
		return &rateLimitError{retry: retry}
	}
	return nil
}

func (h *AdminAccountHandler) writeLimitOrTechnical(w http.ResponseWriter, err error) {
	var limited *rateLimitError
	if errors.As(err, &limited) {
		writeAccountError(w, "USRB02007", http.StatusTooManyRequests, limited.retry)
		return
	}
	writeAccountError(w, "USRT03001", http.StatusInternalServerError, 0)
}

func (h *AdminAccountHandler) list(w http.ResponseWriter, r *http.Request) {
	values, err := parseQuery(r.URL.RawQuery)
	if err != nil {
		writeAccountError(w, "USRB02001", http.StatusBadRequest, 0)
		return
	}
	page, err := h.service.List(r.Context(), values)
	if err != nil {
		writeAccountError(w, accountError(err), accountErrorStatus(err), 0)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Code       string     `json:"code"`
		Message    string     `json:"message"`
		Data       []Account  `json:"data"`
		Pagination Pagination `json:"pagination"`
	}{"USRS00000", "Daftar admin berhasil diambil.", page.Accounts, page.Pagination})
}

func (h *AdminAccountHandler) detail(w http.ResponseWriter, r *http.Request) {
	account, err := h.service.Detail(r.Context(), r.PathValue("adminId"))
	if err != nil {
		writeAccountError(w, accountError(err), accountErrorStatus(err), 0)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, http.StatusOK, utils.ResponseEnvelope{Code: "USRS00000", Message: "Detail admin berhasil diambil.", Data: account})
}

func accountError(err error) string {
	switch {
	case errors.Is(err, ErrInvalid):
		return "USRB02001"
	case errors.Is(err, ErrNotFound):
		return "USRB02004"
	default:
		return "USRT03001"
	}
}

func accountErrorStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeAccountError(w http.ResponseWriter, code string, status int, retry time.Duration) {
	message := map[string]string{
		"USRB01001": "Sesi admin tidak tersedia atau tidak valid.",
		"USRB01002": "Akses admin tidak diizinkan.",
		"USRB02001": "Filter admin tidak valid.",
		"USRB02004": "Akun admin tidak ditemukan.",
		"USRB02007": "Batas request admin tercapai.",
		"USRT03001": "Layanan admin tidak tersedia.",
	}[code]
	if status == http.StatusTooManyRequests {
		seconds := int(math.Ceil(retry.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, status, utils.ResponseEnvelope{Code: code, Message: message, Data: nil})
}
