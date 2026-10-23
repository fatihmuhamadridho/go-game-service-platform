package controllers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

const (
	customerAccessCookie  = "__Host-jokihub-session"
	customerRefreshCookie = "__Host-jokihub-refresh"
	adminAccessCookie     = "jokihub-admin"
	adminRefreshCookie    = "jokihub-admin-refresh"
	refreshMaxAge         = 7 * 24 * 60 * 60
)

type AuthHTTP struct {
	presenter *controller.AuthPresenter
	mailer    model.EmailSender
}

var authRateLimitStore model.RateLimitRepository

func ConfigureAuthRateLimit(repository model.RateLimitRepository) {
	authRateLimitStore = repository
}

func rateLimitCounter(ctx context.Context, key string, expiry time.Time) (int64, error) {
	if authRateLimitStore == nil {
		return 0, errors.New("auth rate limit store is not configured")
	}
	return authRateLimitStore.Increment(ctx, key, expiry)
}

func RegisterAuthRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, mailer model.EmailSender) {
	handler := &AuthHTTP{presenter: auth, mailer: mailer}
	mux.HandleFunc("POST /api/auth/v1/login", handler.login(false))
	mux.HandleFunc("POST /api/auth/v1/register", handler.register)
	mux.HandleFunc("POST /api/auth/v1/logout", handler.logout(model.CustomerAccount))
	mux.HandleFunc("POST /api/auth/v1/refresh-token", handler.refresh(model.CustomerAccount))
	mux.HandleFunc("GET /api/auth/v1/session", handler.session)
	mux.HandleFunc("POST /api/auth/v1/forgot-password", handler.forgotPassword)
	mux.HandleFunc("POST /api/auth/v1/reset-password", handler.resetPassword)
	mux.HandleFunc("POST /api/admin/auth/v1/login", handler.login(true))
	mux.HandleFunc("POST /api/admin/auth/v1/logout", handler.logout(model.AdminAccountType))
	mux.HandleFunc("POST /api/admin/auth/v1/refresh-token", handler.refresh(model.AdminAccountType))
}

type registerRequest struct {
	Name          string          `json:"name"`
	Username      string          `json:"username"`
	Email         string          `json:"email"`
	WhatsApp      json.RawMessage `json:"whatsapp"`
	Password      string          `json:"password"`
	TermsAccepted bool            `json:"termsAccepted"`
}

func hasUnknownLoginField(raw map[string]json.RawMessage) bool {
	for key := range raw {
		if key != "identifier" && key != "username" && key != "email" && key != "password" {
			return true
		}
	}
	return false
}

func firstLoginString(raw map[string]json.RawMessage, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		value = bytes.TrimSpace(value)
		if len(value) == 0 || value[0] != '"' {
			continue
		}
		var parsed string
		if json.Unmarshal(value, &parsed) == nil {
			return parsed, true
		}
	}
	return "", false
}

func rawJSONString(value json.RawMessage) (string, bool) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || value[0] != '"' {
		return "", false
	}
	var parsed string
	if json.Unmarshal(value, &parsed) != nil {
		return "", false
	}
	return parsed, true
}

func decodeBody(r *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func featureEnabled(name string) bool {
	return os.Getenv(name) == "true"
}

func (h *AuthHTTP) login(adminOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if enforceRateLimit(w, r, "login", "") {
			return
		}
		var raw map[string]json.RawMessage
		if err := decodeBody(r, &raw); err != nil {
			code, message := "AUTB02001", "Data masuk tidak valid."
			if adminOnly {
				code, message = "ADMB02001", "Username atau password tidak valid."
			}
			writeJSON(w, http.StatusBadRequest, code, message, nil)
			return
		}
		if hasUnknownLoginField(raw) {
			code, message := "AUTB02001", "Username/email dan password wajib diisi."
			if adminOnly {
				code, message = "ADMB02001", "Username/email atau password tidak valid."
			}
			writeJSON(w, http.StatusBadRequest, code, message, nil)
			return
		}
		identifier, found := firstLoginString(raw, "identifier", "username", "email")
		password, passwordOK := rawJSONString(raw["password"])
		invalidIdentifier := !found || identifier == "" || (!adminOnly && strings.TrimSpace(identifier) == "")
		invalidPassword := !passwordOK || (!adminOnly && password == "")
		if invalidIdentifier || controller.JSStringLength(identifier) > 254 || invalidPassword || controller.JSStringLength(password) > 128 {
			code, message := "AUTB02001", "Username/email dan password wajib diisi."
			if adminOnly {
				code, message = "ADMB02001", "Username/email atau password tidak valid."
			}
			writeJSON(w, http.StatusBadRequest, code, message, nil)
			return
		}
		if enforceRateLimit(w, r, "login", identifier) {
			return
		}
		account, err := h.presenter.Authenticate(r.Context(), identifier, password, adminOnly)
		if err != nil {
			code, message := "AUTT03001", "Layanan autentikasi sedang tidak tersedia."
			if adminOnly {
				code, message = "ADMT03001", "Autentikasi admin sedang tidak tersedia."
			}
			writeJSON(w, http.StatusInternalServerError, code, message, nil)
			return
		}
		if account == nil {
			code, message := "AUTB01001", "Kredensial tidak valid."
			if adminOnly {
				code, message = "ADMB01001", "Kredensial admin tidak valid."
			}
			writeJSON(w, http.StatusUnauthorized, code, message, nil)
			return
		}
		refreshToken, err := h.presenter.CreateRefreshSession(r.Context(), account.AccountType, controller.ObjectIDOrZero(account.Account.ID))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, "AUTT03001", "Layanan autentikasi sedang tidak tersedia.", nil)
			return
		}
		data := map[string]any{"accountType": account.AccountType, "user": account.Account, "expiresIn": 900}
		if account.AccountType == model.AdminAccountType {
			data["role"] = account.Role
		}
		if adminOnly {
			data = map[string]any{"tokenType": "Bearer", "expiresIn": 900, "admin": map[string]any{
				"id": account.Account.ID, "name": account.Account.Name, "username": account.Account.Username,
				"email": account.Account.Email, "role": account.Role,
			}}
		}
		setSessionCookies(w, account.AccountType, account.Token, refreshToken)
		writeJSON(w, http.StatusOK, map[bool]string{true: "ADMS00000", false: "AUTS00000"}[adminOnly], map[bool]string{true: "Login admin berhasil.", false: "Login berhasil."}[adminOnly], data)
	}
}

func (h *AuthHTTP) register(w http.ResponseWriter, r *http.Request) {
	if !featureEnabled("FEATURE_REGISTER") {
		writeJSON(w, http.StatusNotFound, "AUTT02004", "Pendaftaran akun sedang dinonaktifkan.", nil)
		return
	}
	if enforceRateLimit(w, r, "register", "") {
		return
	}
	var body registerRequest
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Data pendaftaran tidak valid.", nil)
		return
	}
	var whatsapp *string
	if len(body.WhatsApp) > 0 {
		value, ok := rawJSONString(body.WhatsApp)
		if !ok {
			writeJSON(w, http.StatusBadRequest, "AUTB02001", "Periksa kembali data pendaftaran.", nil)
			return
		}
		whatsapp = &value
	}
	name := strings.TrimSpace(body.Name)
	username := strings.ToLower(strings.TrimSpace(body.Username))
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if name == "" || controller.JSStringLength(body.Name) > 150 || !usernamePattern.MatchString(username) || controller.JSStringLength(body.Email) > 254 || !validEmail(body.Email) || controller.JSStringLength(body.Password) < 8 || controller.JSStringLength(body.Password) > 128 || !body.TermsAccepted || (whatsapp != nil && !whatsappPattern.MatchString(*whatsapp)) {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Periksa kembali data pendaftaran.", nil)
		return
	}
	if enforceRateLimit(w, r, "register", email) {
		return
	}
	account, err := h.presenter.RegisterCustomer(r.Context(), name, username, email, body.Password, whatsapp)
	if errors.Is(err, controller.ErrRegistrationClosed) {
		writeJSON(w, http.StatusForbidden, "AUTB02003", "Pendaftaran akun sedang ditutup.", nil)
		return
	}
	if errors.Is(err, controller.ErrAccountConflict) {
		writeJSON(w, http.StatusConflict, "AUTB02002", "Username atau email sudah digunakan.", nil)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "AUTT03001", "Layanan autentikasi sedang tidak tersedia.", nil)
		return
	}
	refreshToken, err := h.presenter.CreateRefreshSession(r.Context(), model.CustomerAccount, controller.ObjectIDOrZero(account.Account.ID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "AUTT03001", "Layanan autentikasi sedang tidak tersedia.", nil)
		return
	}
	setSessionCookies(w, model.CustomerAccount, account.Token, refreshToken)
	writeJSON(w, http.StatusCreated, "AUTS00000", "Akun berhasil dibuat.", map[string]any{"user": account.Account, "expiresIn": 900})
}

var (
	usernamePattern     = regexp.MustCompile(`^[a-z0-9._-]{3,30}$`)
	whatsappPattern     = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)
	bearerPrefixPattern = regexp.MustCompile(`(?i)^Bearer\s+`)
)

func validEmail(value string) bool {
	return regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(value)
}

func setSessionCookies(w http.ResponseWriter, accountType model.AccountType, access, refresh string) {
	w.Header().Set("Cache-Control", "no-store")
	secure := accountType == model.CustomerAccount || os.Getenv("NODE_ENV") == "production"
	sameSite := http.SameSiteStrictMode
	accessCookie, refreshCookie, hintCookie := adminAccessCookie, adminRefreshCookie, "jokihub-admin-refresh-present"
	if accountType == model.CustomerAccount {
		accessCookie, refreshCookie, hintCookie = customerAccessCookie, customerRefreshCookie, "jokihub-customer-refresh-present"
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: access, Path: "/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: 900})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: refresh, Path: "/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: refreshMaxAge})
	http.SetCookie(w, &http.Cookie{Name: hintCookie, Value: "1", Path: "/", Secure: secure, SameSite: sameSite, MaxAge: refreshMaxAge})
}

func clearSessionCookies(w http.ResponseWriter, accountType model.AccountType) {
	w.Header().Set("Cache-Control", "no-store")
	secure := accountType == model.CustomerAccount || os.Getenv("NODE_ENV") == "production"
	sameSite := http.SameSiteStrictMode
	accessCookie, refreshCookie, hintCookie := adminAccessCookie, adminRefreshCookie, "jokihub-admin-refresh-present"
	if accountType == model.CustomerAccount {
		accessCookie, refreshCookie, hintCookie = customerAccessCookie, customerRefreshCookie, "jokihub-customer-refresh-present"
		sameSite = http.SameSiteLaxMode
	}
	for _, name := range []string{accessCookie, refreshCookie, hintCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: name != hintCookie, Secure: secure, SameSite: sameSite, MaxAge: -1})
	}
}

func clearAccessCookie(w http.ResponseWriter, accountType model.AccountType) {
	secure := accountType == model.CustomerAccount || os.Getenv("NODE_ENV") == "production"
	sameSite := http.SameSiteStrictMode
	name := adminAccessCookie
	if accountType == model.CustomerAccount {
		name = customerAccessCookie
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: -1})
}

func readCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func authToken(r *http.Request, cookieName string) string {
	if token, ok := parseBearerToken(r.Header.Get("Authorization")); ok {
		return token
	}
	return readCookie(r, cookieName)
}

func parseBearerToken(value string) (string, bool) {
	match := bearerTokenPattern.FindStringSubmatch(value)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func trackingBearerToken(value string) string {
	return strings.TrimSpace(bearerPrefixPattern.ReplaceAllString(value, ""))
}

func authTokens(r *http.Request, cookieName string) []string {
	tokens := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(value string) {
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		tokens = append(tokens, value)
	}
	if value, ok := parseBearerToken(r.Header.Get("Authorization")); ok {
		add(value)
	}
	add(readCookie(r, cookieName))
	return tokens
}

func (h *AuthHTTP) logout(accountType model.AccountType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); !isTrustedRequestOrigin(origin, requestOrigin(r), parseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))) {
			code, message := "AUTB02001", "Permintaan logout tidak valid."
			if accountType == model.AdminAccountType {
				code, message = "ADMB02001", "Permintaan logout tidak valid."
			}
			writeJSON(w, http.StatusBadRequest, code, message, nil)
			return
		}
		accessCookie, refreshCookie := customerAccessCookie, customerRefreshCookie
		if accountType == model.AdminAccountType {
			accessCookie, refreshCookie = adminAccessCookie, adminRefreshCookie
		}
		var revokeErr error
		for _, accessToken := range authTokens(r, accessCookie) {
			if err := h.presenter.RevokeAccessToken(r.Context(), accessToken, accountType); err != nil {
				revokeErr = err
			}
		}
		err2 := h.presenter.RevokeRefreshToken(r.Context(), readCookie(r, refreshCookie), accountType)
		clearSessionCookies(w, accountType)
		if revokeErr != nil || err2 != nil {
			code, message := "AUTT03001", "Logout sementara tidak tersedia."
			if accountType == model.AdminAccountType {
				code, message = "ADMT03001", "Logout admin sementara tidak tersedia."
			}
			writeJSON(w, http.StatusInternalServerError, code, message, nil)
			return
		}
		code, message := "AUTS00000", "Logout diproses."
		if accountType == model.AdminAccountType {
			code, message = "ADMS00000", "Logout admin berhasil."
		}
		writeJSON(w, http.StatusOK, code, message, map[string]bool{"loggedOut": true})
	}
}

func requestOrigin(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}

func (h *AuthHTTP) refresh(accountType model.AccountType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookieName := customerRefreshCookie
		accessName := customerAccessCookie
		if accountType == model.AdminAccountType {
			cookieName, accessName = adminRefreshCookie, adminAccessCookie
		}
		access, nextRefresh, account, err := h.presenter.RotateRefreshSession(r.Context(), readCookie(r, cookieName), accountType)
		if err != nil {
			code, message := "AUTT03001", "Layanan autentikasi sedang tidak tersedia."
			if accountType == model.AdminAccountType {
				code, message = "ADMT03001", "Autentikasi admin sedang tidak tersedia."
			}
			writeJSON(w, http.StatusInternalServerError, code, message, nil)
			return
		}
		if account == nil {
			code, message := "AUTB01001", "Sesi tidak tersedia atau sudah berakhir."
			if accountType == model.AdminAccountType {
				code, message = "ADMB01001", "Sesi admin tidak tersedia atau sudah berakhir."
			}
			writeJSON(w, http.StatusUnauthorized, code, message, nil)
			clearSessionCookies(w, accountType)
			return
		}
		code, message := "AUTS00000", "Sesi diperbarui."
		if accountType == model.AdminAccountType {
			code, message = "ADMS00000", "Sesi admin diperbarui."
		}
		http.SetCookie(w, &http.Cookie{Name: accessName, Value: access, Path: "/", HttpOnly: true, Secure: accountType == model.CustomerAccount || os.Getenv("NODE_ENV") == "production", SameSite: map[bool]http.SameSite{true: http.SameSiteLaxMode, false: http.SameSiteStrictMode}[accountType == model.CustomerAccount], MaxAge: 900})
		setRefreshCookies(w, accountType, nextRefresh)
		writeJSON(w, http.StatusOK, code, message, map[string]any{"accountType": accountType, "expiresIn": 900})
	}
}

func setRefreshCookies(w http.ResponseWriter, accountType model.AccountType, refresh string) {
	secure := accountType == model.CustomerAccount || os.Getenv("NODE_ENV") == "production"
	sameSite := http.SameSiteStrictMode
	name, hint := adminRefreshCookie, "jokihub-admin-refresh-present"
	if accountType == model.CustomerAccount {
		name, hint, sameSite = customerRefreshCookie, "jokihub-customer-refresh-present", http.SameSiteLaxMode
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: refresh, Path: "/", HttpOnly: true, Secure: secure, SameSite: sameSite, MaxAge: refreshMaxAge})
	http.SetCookie(w, &http.Cookie{Name: hint, Value: "1", Path: "/", Secure: secure, SameSite: sameSite, MaxAge: refreshMaxAge})
}

func (h *AuthHTTP) session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	requested := r.URL.Query().Get("accountType")
	if requested != "CUSTOMER" {
		if token := authToken(r, adminAccessCookie); token != "" {
			if account, claims, err := h.presenter.AccountByToken(r.Context(), token, model.AdminAccountType); err == nil && account != nil {
				canDashboard := claims.Role == "SUPER_ADMIN" || contains(claims.Permissions, "*") || contains(claims.Permissions, "dashboard-read")
				writeJSON(w, http.StatusOK, "AUTS00000", "Status sesi.", map[string]any{
					"accountType": "ADMIN", "name": account.Name, "username": account.Username, "role": claims.Role,
					"roleLabel": map[bool]string{true: "Super Admin", false: "Admin"}[claims.Role == "SUPER_ADMIN"], "canAccessDashboard": canDashboard,
				})
				return
			}
		}
	}
	if requested != "ADMIN" {
		if token := readCookie(r, customerAccessCookie); token != "" {
			if account, _, err := h.presenter.AccountByToken(r.Context(), token, model.CustomerAccount); err == nil && account != nil {
				writeJSON(w, http.StatusOK, "AUTS00000", "Status sesi.", map[string]any{
					"accountType": "CUSTOMER", "name": account.Name, "username": account.Username,
					"role": "CUSTOMER", "roleLabel": "Member", "canAccessDashboard": false,
				})
				return
			}
		}
	}
	if readCookie(r, adminRefreshCookie) != "" {
		w.Header().Set("X-Account-Type", "ADMIN")
	} else if readCookie(r, customerRefreshCookie) != "" {
		w.Header().Set("X-Account-Type", "CUSTOMER")
	}
	clearAccessCookie(w, model.AdminAccountType)
	clearAccessCookie(w, model.CustomerAccount)
	writeJSON(w, http.StatusUnauthorized, "AUTB01001", "Status sesi.", nil)
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (h *AuthHTTP) forgotPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !featureEnabled("FEATURE_PASSWORD_RECOVERY") {
		writeJSON(w, http.StatusNotFound, "AUTT02004", "Pemulihan password sedang dinonaktifkan.", nil)
		return
	}
	if enforceRateLimit(w, r, "recovery", "") {
		return
	}
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil || len(raw) != 1 {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Data pemulihan tidak valid.", nil)
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if _, ok := raw["email"]; !ok || json.Unmarshal(raw["email"], &body.Email) != nil || controller.JSStringLength(body.Email) > 254 || !regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(strings.TrimSpace(body.Email)) {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Masukkan alamat email yang valid.", nil)
		return
	}
	address := strings.ToLower(strings.TrimSpace(body.Email))
	if limited := enforceRateLimit(w, r, "recovery", address); limited {
		return
	}
	if h.mailer == nil || !h.mailer.Configured() || recoveryBaseURL() == "" {
		writeJSON(w, http.StatusServiceUnavailable, "AUTT02004", "Pemulihan password belum tersedia karena email belum dikonfigurasi.", nil)
		return
	}
	token, err := h.presenter.RequestPasswordReset(r.Context(), address, 30*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, "AUTT03001", "Layanan pemulihan password sedang tidak tersedia.", nil)
		return
	}
	if token != "" {
		resetURL := recoveryBaseURL() + "/reset-password?token=" + url.QueryEscape(token)
		text := fmt.Sprintf("Gunakan tautan ini dalam 30 menit untuk mengatur ulang password: %s\nJika kamu tidak meminta reset, abaikan email ini.", resetURL)
		html := fmt.Sprintf(`<p>Gunakan tautan ini dalam 30 menit untuk mengatur ulang password:</p><p><a href="%s">Atur ulang password</a></p><p>Jika kamu tidak meminta reset, abaikan email ini.</p>`, resetURL)
		if h.mailer.Send(address, "Atur ulang password JokiHub", text, html) != nil {
			_ = h.presenter.InvalidatePasswordResetToken(r.Context(), token)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, "AUTS00000", "Jika email terdaftar, instruksi pemulihan akan dikirim.", nil)
}

func (h *AuthHTTP) resetPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !featureEnabled("FEATURE_PASSWORD_RECOVERY") {
		writeJSON(w, http.StatusNotFound, "AUTT02004", "Pemulihan password sedang dinonaktifkan.", nil)
		return
	}
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil || len(raw) != 2 {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Data reset password tidak valid.", nil)
		return
	}
	if _, ok := raw["token"]; !ok {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Data reset password tidak valid.", nil)
		return
	}
	if _, ok := raw["password"]; !ok {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Data reset password tidak valid.", nil)
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if json.Unmarshal(raw["token"], &body.Token) != nil || json.Unmarshal(raw["password"], &body.Password) != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{40,60}$`).MatchString(body.Token) || controller.JSStringLength(body.Password) < 8 || controller.JSStringLength(body.Password) > 128 {
		writeJSON(w, http.StatusBadRequest, "AUTB02001", "Password harus terdiri dari 8–128 karakter.", nil)
		return
	}
	if limited := enforceRateLimit(w, r, "recovery", body.Token); limited {
		return
	}
	if err := h.presenter.ResetCustomerPassword(r.Context(), body.Token, body.Password); err != nil {
		if errors.Is(err, controller.ErrInvalidPasswordReset) {
			writeJSON(w, http.StatusBadRequest, "AUTB02005", "Tautan reset tidak valid atau sudah kedaluwarsa.", nil)
		} else {
			writeJSON(w, http.StatusInternalServerError, "AUTT03001", "Layanan pemulihan password sedang tidak tersedia.", nil)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, "AUTS00000", "Password berhasil diubah. Silakan masuk kembali.", nil)
}

func recoveryBaseURL() string {
	value := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && os.Getenv("NODE_ENV") != "production")) {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func enforceRateLimit(w http.ResponseWriter, r *http.Request, action, subject string) bool {
	hit, err := rateLimitHit(r.Context(), r, action, subject)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, "AUTT03001", "Layanan autentikasi sedang tidak tersedia.", nil)
		return true
	}
	if !hit {
		return false
	}
	window, _, _ := rateLimitConfig(action)
	windowNumber := time.Now().UnixNano() / int64(window)
	retry := int(math.Ceil(time.Until(time.Unix(0, (windowNumber+1)*int64(window))).Seconds()))
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", fmt.Sprint(retry))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusTooManyRequests, "AUTB01002", "Terlalu banyak percobaan. Coba lagi setelah jeda.", nil)
	return true
}

func rateLimitHit(ctx context.Context, r *http.Request, action, subject string) (bool, error) {
	secret := os.Getenv("JWT_SECRET")
	if controller.JSStringLength(secret) < 32 {
		return false, errors.New("JWT_SECRET is not configured")
	}
	ip := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if ip == "" {
		ip = strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	}
	if ip == "" {
		ip = "unknown"
	}
	ip = controller.JSStringPrefix(strings.ToLower(strings.TrimSpace(ip)), 128)
	window, ipLimit, subjectLimit := rateLimitConfig(action)
	ipHash := hmacDigest(secret, ip)
	keyValue := "ip:" + ipHash
	limit := ipLimit
	if subject != "" {
		keyValue = "ip-subject:" + ipHash + ":" + hmacDigest(secret, strings.ToLower(strings.TrimSpace(subject)))
		limit = subjectLimit
	}
	windowNumber := time.Now().UnixNano() / int64(window)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(fmt.Appendf(nil, "%s:%d:%s", action, windowNumber, keyValue))
	key := fmt.Sprintf("%x", mac.Sum(nil))
	count, err := rateLimitCounter(ctx, key, time.Unix(0, (windowNumber+1)*int64(window)))
	if err != nil {
		return false, err
	}
	return count > int64(limit), nil
}

func rateLimitConfig(action string) (time.Duration, int, int) {
	switch action {
	case "login":
		return 15 * time.Minute, 100, 10
	case "register":
		return time.Hour, 20, 3
	case "recovery":
		return time.Hour, 20, 5
	case "checkout":
		return 15 * time.Minute, 40, 10
	case "payment-proof":
		return time.Hour, 12, 4
	case "payment-status":
		return time.Hour, 240, 120
	case "order-tracking":
		return time.Hour, 50, 10
	default:
		return time.Hour, 1, 1
	}
}

func hmacDigest(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return fmt.Sprintf("%x", mac.Sum(nil))
}
