package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

const (
	accessCookieName  = "__Host-jokihub-admin-access"
	refreshCookieName = "__Host-jokihub-admin-refresh"
	csrfHeader        = "X-CSRF-Token"
)

type authModelData interface {
	AuthData
	ValidateCSRF(context.Context, string, string) (bool, error)
}

type AdminAuthHandler struct {
	service           *AuthService
	data              authModelData
	tokens            TokenManager
	origins           map[string]struct{}
	logger            *slog.Logger
	trustedProxies    []*net.IPNet
	allowInsecureHTTP bool
}

type adminContextKey struct{}

func NewAdminAuthHandler(service *AuthService, data authModelData, tokens TokenManager, origins []string, trustedProxies []*net.IPNet, logger *slog.Logger, allowInsecureHTTP ...bool) *AdminAuthHandler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	allowInsecure := len(allowInsecureHTTP) > 0 && allowInsecureHTTP[0]
	return &AdminAuthHandler{service: service, data: data, tokens: tokens, origins: allowed, logger: logger, trustedProxies: trustedProxies, allowInsecureHTTP: allowInsecure}
}

func (h *AdminAuthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/auth/v1/login", h.login)
	mux.HandleFunc("POST /api/admin/auth/v1/refresh-token", h.refresh)
	mux.HandleFunc("POST /api/admin/auth/v1/logout", h.logout)
}

func (h *AdminAuthHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		accessName, _ := authCookieNames(!h.allowInsecureHTTP)
		cookie, err := request.Cookie(accessName)
		if err != nil {
			h.writeRequestError(writer, request, serviceErr("ADMB01003", 401, nil))
			return
		}
		claims, err := h.tokens.ParseAccess(cookie.Value, time.Now().UTC())
		if err != nil {
			h.clearAuthCookies(writer)
			h.writeRequestError(writer, request, serviceErr("ADMB01003", 401, nil))
			return
		}
		profile, active, err := h.data.ResolveAccess(request.Context(), claims.FamilyID, claims.Subject, time.Now().UTC())
		if err != nil {
			h.writeRequestError(writer, request, serviceErr("ADMT03001", 500, err))
			return
		}
		if !active {
			h.clearAuthCookies(writer)
			h.writeRequestError(writer, request, serviceErr("ADMB01003", 401, nil))
			return
		}
		requireCSRF := request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions
		requireCSRF = requireCSRF || isGameRequest(request) || isRoleRequest(request) || isAdminUserRequest(request)
		if requireCSRF && request.Method != http.MethodOptions {
			valid, err := h.data.ValidateCSRF(request.Context(), claims.FamilyID, utils.DigestToken(request.Header.Get(csrfHeader)))
			if err != nil {
				h.writeRequestError(writer, request, serviceErr("ADMT03001", 500, err))
				return
			}
			if !valid {
				h.writeRequestError(writer, request, serviceErr("ADMB01004", 403, nil))
				return
			}
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), adminContextKey{}, profile)))
	})
}

func (h *AdminAuthHandler) RequirePermission(permission string, next http.Handler) http.Handler {
	return h.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		profile, ok := request.Context().Value(adminContextKey{}).(AdminProfile)
		if !ok {
			h.writeRequestError(writer, request, serviceErr("ADMB01003", 401, nil))
			return
		}
		allowed := false
		for _, current := range profile.Perms {
			if current == permission {
				allowed = true
				break
			}
		}
		if !allowed {
			h.writeRequestError(writer, request, serviceErr("ADMB01005", 403, nil))
			return
		}
		next.ServeHTTP(writer, request)
	}))
}

func AdminProfileFromRequest(request *http.Request) (AdminProfile, bool) {
	profile, ok := request.Context().Value(adminContextKey{}).(AdminProfile)
	return profile, ok
}

func ClientIP(request *http.Request, trustedProxies []*net.IPNet) string {
	return requestIP(request, trustedProxies)
}

func isGameRequest(request *http.Request) bool {
	return strings.HasPrefix(request.URL.Path, "/api/admin/games/v1/")
}

func isRoleRequest(request *http.Request) bool {
	return strings.HasPrefix(request.URL.Path, "/api/admin/roles/v1/")
}

func isAdminUserRequest(request *http.Request) bool {
	return strings.HasPrefix(request.URL.Path, "/api/admin/admin-users/v1/")
}

func isPublicCatalogRequest(request *http.Request) bool {
	switch request.URL.Path {
	case "/api/games/v1/list", "/api/games/v1/popular":
		return true
	default:
		return false
	}
}

func (h *AdminAuthHandler) writeRequestError(writer http.ResponseWriter, request *http.Request, err error) {
	if isAdminUserRequest(request) {
		var typed *ServiceError
		if !errors.As(err, &typed) {
			typed = &ServiceError{Code: "USRT03001", HTTPStatus: http.StatusInternalServerError}
		}
		code := "USRT03001"
		switch typed.Code {
		case "ADMB01003":
			code = "USRB01001"
		case "ADMB01002", "ADMB01004", "ADMB01005", "ADMB01006":
			code = "USRB01002"
		case "ADMB02001":
			code = "USRB02001"
		case "ADMB02002":
			code = "USRB02007"
		}
		message := map[string]string{
			"USRB01001": "Sesi admin tidak tersedia atau tidak valid.",
			"USRB01002": "Permission ditolak.",
			"USRB02001": "Query atau filter tidak valid.",
			"USRB02007": "Batas request akun admin tercapai.",
			"USRT03001": "Kegagalan persistence akun admin.",
		}[code]
		if typed.HTTPStatus <= 0 {
			typed.HTTPStatus = http.StatusInternalServerError
		}
		if typed.HTTPStatus == http.StatusTooManyRequests {
			seconds := int(typed.RetryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			writer.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		utils.WriteJSON(writer, typed.HTTPStatus, utils.ResponseEnvelope{Code: code, Message: message, Data: nil})
		return
	}
	if isRoleRequest(request) {
		var typed *ServiceError
		if !errors.As(err, &typed) {
			typed = &ServiceError{Code: "ROLT03001", HTTPStatus: http.StatusInternalServerError}
		}
		code := "ROLT03001"
		switch typed.Code {
		case "ADMB01003":
			code = "ROLB01001"
		case "ADMB01002", "ADMB01004", "ADMB01005", "ADMB01006":
			code = "ROLB01002"
		case "ADMB02001":
			code = "ROLB02001"
		case "ADMB02002":
			code = "ROLB02007"
		}
		message := map[string]string{"ROLB01001": "Administrator belum terautentikasi.", "ROLB01002": "Akses role tidak diizinkan.", "ROLB02001": "Parameter role tidak valid.", "ROLB02007": "Batas request role tercapai.", "ROLT03001": "Role service is temporarily unavailable."}[code]
		if typed.HTTPStatus == http.StatusTooManyRequests {
			seconds := int(typed.RetryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			writer.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		utils.WriteJSON(writer, typed.HTTPStatus, utils.ResponseEnvelope{Code: code, Message: message, Data: nil})
		return
	}
	if !isGameRequest(request) {
		writeServiceError(writer, err)
		return
	}
	var typed *ServiceError
	if !errors.As(err, &typed) {
		typed = &ServiceError{Code: "GAMT03001", HTTPStatus: http.StatusInternalServerError}
	}
	code := "GAMT03001"
	switch typed.Code {
	case "ADMB01003":
		code = "GAMB01001"
	case "ADMB01002", "ADMB01004", "ADMB01005", "ADMB01006":
		code = "GAMB01002"
	case "ADMB02001":
		code = "GAMB02001"
	}
	utils.WriteJSON(writer, typed.HTTPStatus, utils.ResponseEnvelope{Code: code, Message: gameAuthErrorMessage(code), Data: nil})
}

func gameAuthErrorMessage(code string) string {
	switch code {
	case "GAMB01001":
		return "Administrator belum terautentikasi."
	case "GAMB01002":
		return "Akses katalog game tidak diizinkan."
	case "GAMB02001":
		return "Parameter request game tidak valid."
	default:
		return "Game catalog is temporarily unavailable."
	}
}

func (h *AdminAuthHandler) login(writer http.ResponseWriter, request *http.Request) {
	if !requireJSON(writer, request, true) {
		return
	}
	var body LoginRequestDTO
	if err := decodeStrictJSON(writer, request, &body); err != nil {
		writeServiceError(writer, serviceErr("ADMB02001", 400, err))
		return
	}
	issued, err := h.service.Login(request.Context(), body.Identifier, body.Password)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	setAuthCookies(writer, issued.AccessToken, issued.RefreshToken, issued.AccessExpires, !h.allowInsecureHTTP)
	utils.WriteJSON(writer, http.StatusOK, utils.ResponseEnvelope{Code: "ADMS00000", Message: "Login admin berhasil.", Data: LoginResponseDTO{AdminProfile: issued.Profile, CSRFToken: issued.CSRFToken, AccessExpires: issued.AccessExpires}})
}

func (h *AdminAuthHandler) refresh(writer http.ResponseWriter, request *http.Request) {
	if !requireJSON(writer, request, false) {
		return
	}
	if !requireEmptyBody(writer, request) {
		return
	}
	_, refreshName := authCookieNames(!h.allowInsecureHTTP)
	refreshCookie, err := request.Cookie(refreshName)
	if err != nil {
		h.clearAuthCookies(writer)
		writeServiceError(writer, serviceErr("ADMB01003", 401, nil))
		return
	}
	issued, err := h.service.Refresh(request.Context(), refreshCookie.Value, request.Header.Get(csrfHeader))
	if err != nil {
		var serviceError *ServiceError
		if errors.As(err, &serviceError) && serviceError.HTTPStatus == 401 {
			h.clearAuthCookies(writer)
		}
		writeServiceError(writer, err)
		return
	}
	setAuthCookies(writer, issued.AccessToken, issued.RefreshToken, issued.AccessExpires, !h.allowInsecureHTTP)
	utils.WriteJSON(writer, http.StatusOK, utils.ResponseEnvelope{Code: "ADMS00000", Message: "Session admin diperbarui.", Data: RefreshResponseDTO{CSRFToken: issued.CSRFToken, AccessExpires: issued.AccessExpires}})
}

func (h *AdminAuthHandler) logout(writer http.ResponseWriter, request *http.Request) {
	if !requireJSON(writer, request, false) {
		return
	}
	if !requireEmptyBody(writer, request) {
		return
	}
	var familyID string
	accessName, refreshName := authCookieNames(!h.allowInsecureHTTP)
	if cookie, err := request.Cookie(accessName); err == nil {
		if claims, parseErr := h.tokens.ParseAccess(cookie.Value, time.Now().UTC()); parseErr == nil {
			_, active, resolveErr := h.data.ResolveAccess(request.Context(), claims.FamilyID, claims.Subject, time.Now().UTC())
			if resolveErr != nil {
				writeServiceError(writer, serviceErr("ADMT03001", 500, resolveErr))
				return
			}
			if active {
				familyID = claims.FamilyID
			}
		}
	}
	if familyID == "" {
		if cookie, err := request.Cookie(refreshName); err == nil {
			session, findErr := h.data.FindSessionByRefreshDigest(request.Context(), utils.DigestToken(cookie.Value))
			if findErr != nil && !errors.Is(findErr, ErrNotFound) {
				writeServiceError(writer, serviceErr("ADMT03001", 500, findErr))
				return
			}
			if findErr == nil && session.RevokedAt == nil && session.ExpiresAt.After(time.Now().UTC()) {
				familyID = session.FamilyID
			}
		}
	}
	if familyID != "" {
		valid, err := h.data.ValidateCSRF(request.Context(), familyID, utils.DigestToken(request.Header.Get(csrfHeader)))
		if err != nil {
			writeServiceError(writer, serviceErr("ADMT03001", 500, err))
			return
		}
		if !valid {
			writeServiceError(writer, serviceErr("ADMB01004", 403, nil))
			return
		}
		if err := h.service.Logout(request.Context(), familyID); err != nil {
			writeServiceError(writer, err)
			return
		}
	}
	h.clearAuthCookies(writer)
	utils.WriteJSON(writer, http.StatusOK, utils.ResponseEnvelope{Code: "ADMS00000", Message: "Logout admin berhasil.", Data: nil})
}

func requireEmptyBody(writer http.ResponseWriter, request *http.Request) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1024)
	body, err := io.ReadAll(request.Body)
	if err != nil || len(body) != 0 {
		writeServiceError(writer, serviceErr("ADMB02001", 400, err))
		return false
	}
	return true
}

func (h *AdminAuthHandler) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if isPublicCatalogRequest(request) {
			h.publicCatalogCORS(writer, request, next)
			return
		}
		origin := request.Header.Get("Origin")
		if _, allowed := h.origins[origin]; !allowed {
			h.writeRequestError(writer, request, serviceErr("ADMB01002", 403, nil))
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		writer.Header().Add("Vary", "Origin")
		if !(h.allowInsecureHTTP && isLoopbackRequest(request)) && !isSecureRequest(request, h.trustedProxies) {
			h.writeRequestError(writer, request, serviceErr("ADMB01006", 403, nil))
			return
		}
		if request.Method == http.MethodOptions {
			requestedMethod := strings.ToUpper(strings.TrimSpace(request.Header.Get("Access-Control-Request-Method")))
			methods := corsMethods(request)
			if !containsString(methods, requestedMethod) || !allowedPreflightHeaders(request.Header.Get("Access-Control-Request-Headers")) {
				h.writeRequestError(writer, request, serviceErr("ADMB01002", 403, nil))
				return
			}
			writer.Header().Set("Access-Control-Allow-Methods", strings.Join(append(methods, "OPTIONS"), ", "))
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Request-Id")
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if !containsString(corsMethods(request), request.Method) {
			h.writeRequestError(writer, request, serviceErr("ADMB02001", 400, nil))
			return
		}
		if request.URL.Path == "/api/admin/auth/v1/login" || request.URL.Path == "/api/admin/auth/v1/refresh-token" {
			retry, allowed, err := h.service.Limiter.Allow(request.Context(), "ip:"+utils.DigestToken(requestIP(request, h.trustedProxies)), IPRequestLimit, time.Now().UTC())
			if err != nil {
				writeServiceError(writer, serviceErr("ADMT03001", 500, err))
				return
			}
			if !allowed {
				writeServiceError(writer, &ServiceError{Code: "ADMB02002", HTTPStatus: 429, RetryAfter: retry})
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func (h *AdminAuthHandler) publicCatalogCORS(writer http.ResponseWriter, request *http.Request, next http.Handler) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	if request.Method == http.MethodOptions {
		requestedMethod := strings.ToUpper(strings.TrimSpace(request.Header.Get("Access-Control-Request-Method")))
		if (requestedMethod != "" && requestedMethod != http.MethodGet) || !allowedPublicPreflightHeaders(request.Header.Get("Access-Control-Request-Headers")) {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Headers", "X-Request-Id")
		writer.Header().Set("Access-Control-Max-Age", "600")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	next.ServeHTTP(writer, request)
}

func allowedPublicPreflightHeaders(value string) bool {
	allowed := map[string]bool{"x-request-id": true}
	for _, header := range strings.Split(value, ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header != "" && !allowed[header] {
			return false
		}
	}
	return true
}

func corsMethods(request *http.Request) []string {
	if isPublicCatalogRequest(request) {
		return []string{http.MethodGet}
	}
	if isAdminUserRequest(request) {
		return []string{http.MethodGet}
	}
	if isRoleRequest(request) {
		return []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}
	}
	if isGameRequest(request) {
		return []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}
	}
	return []string{http.MethodPost}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func allowedPreflightHeaders(value string) bool {
	allowed := map[string]bool{"content-type": true, "x-csrf-token": true, "x-request-id": true}
	for _, header := range strings.Split(value, ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header != "" && !allowed[header] {
			return false
		}
	}
	return true
}

func requireJSON(writer http.ResponseWriter, request *http.Request, required bool) bool {
	value := strings.TrimSpace(request.Header.Get("Content-Type"))
	if !required && value == "" {
		return true
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	if mediaType != "application/json" {
		writeServiceError(writer, serviceErr("ADMB02001", 400, nil))
		return false
	}
	return true
}

func decodeStrictJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 16*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func requestIP(request *http.Request, trustedProxies []*net.IPNet) string {
	if os.Getenv("VERCEL") == "1" {
		if candidate, err := netip.ParseAddr(strings.TrimSpace(strings.Split(request.Header.Get("x-vercel-forwarded-for"), ",")[0])); err == nil {
			return candidate.String()
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = strings.Trim(request.RemoteAddr, "[]")
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return host
	}
	if !isTrustedProxy(peer, trustedProxies) {
		return peer.String()
	}
	forwarded := strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0; index-- {
		candidate, parseErr := netip.ParseAddr(strings.TrimSpace(forwarded[index]))
		if parseErr != nil {
			break
		}
		peer = net.IP(candidate.AsSlice())
		if !isTrustedProxy(peer, trustedProxies) {
			return peer.String()
		}
	}
	return peer.String()
}

func isTrustedProxy(address net.IP, trustedProxies []*net.IPNet) bool {
	for _, network := range trustedProxies {
		if network.Contains(address) {
			return true
		}
	}
	return false
}

func isSecureRequest(request *http.Request, trustedProxies []*net.IPNet) bool {
	if request.TLS != nil {
		return true
	}
	if os.Getenv("VERCEL") == "1" && strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = strings.Trim(request.RemoteAddr, "[]")
	}
	peer := net.ParseIP(host)
	return peer != nil && isTrustedProxy(peer, trustedProxies) && strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https")
}

func isLoopbackRequest(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = strings.Trim(request.RemoteAddr, "[]")
	}
	peer := net.ParseIP(host)
	return peer != nil && peer.IsLoopback()
}

func setAuthCookies(writer http.ResponseWriter, accessToken, refreshToken string, accessExpires int, secure ...bool) {
	isSecure := len(secure) == 0 || secure[0]
	accessName, refreshName := authCookieNames(isSecure)
	http.SetCookie(writer, authCookie(accessName, accessToken, accessExpires, isSecure))
	http.SetCookie(writer, authCookie(refreshName, refreshToken, int(RefreshLifetime.Seconds()), isSecure))
}

func (h *AdminAuthHandler) clearAuthCookies(writer http.ResponseWriter) {
	secure := !h.allowInsecureHTTP
	accessName, refreshName := authCookieNames(secure)
	access := authCookie(accessName, "", 0, secure)
	access.MaxAge = -1
	refresh := authCookie(refreshName, "", 0, secure)
	refresh.MaxAge = -1
	http.SetCookie(writer, access)
	http.SetCookie(writer, refresh)
}

func authCookie(name, value string, maxAge int, secure ...bool) *http.Cookie {
	isSecure := len(secure) == 0 || secure[0]
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: isSecure, SameSite: sameSite(isSecure)}
}

func authCookieNames(secure bool) (string, string) {
	if secure {
		return accessCookieName, refreshCookieName
	}
	return "jokihub-admin-access", "jokihub-admin-refresh"
}

func sameSite(secure bool) http.SameSite {
	if secure {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func serviceErr(code string, status int, cause error) error {
	return &ServiceError{Code: code, HTTPStatus: status, Cause: cause}
}

func writeServiceError(writer http.ResponseWriter, err error) {
	var typed *ServiceError
	if !errors.As(err, &typed) {
		typed = &ServiceError{Code: "ADMT03001", HTTPStatus: 500}
	}
	message := "Layanan autentikasi sedang tidak tersedia."
	switch typed.Code {
	case "ADMB01001":
		message = "Username/email atau password tidak valid."
	case "ADMB01002":
		message = "Origin dashboard tidak diizinkan."
	case "ADMB01003":
		message = "Session tidak valid atau telah berakhir."
	case "ADMB01004":
		message = "CSRF token tidak valid."
	case "ADMB01005":
		message = "Akses tidak diizinkan."
	case "ADMB01006":
		message = "HTTPS diperlukan."
	case "ADMB02001":
		message = "Payload autentikasi tidak valid."
	case "ADMB02002":
		message = "Batas rate limit tercapai."
	}
	if typed.HTTPStatus <= 0 {
		typed.HTTPStatus = 500
	}
	if typed.HTTPStatus == http.StatusTooManyRequests {
		seconds := int(typed.RetryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	utils.WriteJSON(writer, typed.HTTPStatus, utils.ResponseEnvelope{Code: typed.Code, Message: message, Data: nil})
}

func RequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		wrapped := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(wrapped, request)
		logger.Info("http request", "method", request.Method, "path", request.URL.Path, "status", wrapped.status, "duration_ms", time.Since(started).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
