package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type httpTestModel struct {
	AuthData
	profile    AdminProfile
	active     bool
	csrfValid  bool
	resolveErr error
}

type logoutTestModel struct {
	httpTestModel
	revokeErr error
	revoked   []string
}

func (data *logoutTestModel) RevokeSessionFamily(_ context.Context, familyID, _ string, _ time.Time) error {
	data.revoked = append(data.revoked, familyID)
	return data.revokeErr
}

func (data httpTestModel) ValidateCSRF(context.Context, string, string) (bool, error) {
	return data.csrfValid, nil
}
func (data httpTestModel) ResolveAccess(context.Context, string, string, time.Time) (AdminProfile, bool, error) {
	return data.profile, data.active, data.resolveErr
}

type httpTestLimiter struct {
	count   int
	blocked bool
	retry   time.Duration
}

func (l *httpTestLimiter) Allow(context.Context, string, int, time.Time) (time.Duration, bool, error) {
	l.count++
	return l.retry, !l.blocked, nil
}
func (l *httpTestLimiter) Check(context.Context, string, int, time.Time) (time.Duration, bool, error) {
	return 0, true, nil
}

func newHTTPTestHandler() (*AdminAuthHandler, *httpTestLimiter) {
	limiter := &httpTestLimiter{}
	service := &AuthService{Limiter: limiter}
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	handler := NewAdminAuthHandler(service, httpTestModel{}, manager, []string{"https://dashboard.example.test"}, nil, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	return handler, limiter
}

func TestCORSUsesExactAllowlistAndCredentialedOrigin(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.TLS = &tls.ConnectionState{}
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example.test" || response.Header().Get("Access-Control-Allow-Credentials") != "true" || response.Header().Get("Access-Control-Expose-Headers") != "Retry-After" {
		t.Fatalf("missing credentialed CORS headers: %#v", response.Header())
	}
	if limiter.count != 1 {
		t.Fatalf("expected one IP request reservation, got %d", limiter.count)
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	bad.Header.Set("Origin", "https://dashboard.example.test.attacker.invalid")
	bad.TLS = &tls.ConnectionState{}
	bad.RemoteAddr = "127.0.0.1:5000"
	blocked := httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(blocked, bad)
	if blocked.Code != http.StatusForbidden || strings.Contains(blocked.Body.String(), "attacker.invalid") {
		t.Fatalf("unexpected forbidden response: %d %s", blocked.Code, blocked.Body.String())
	}
	if blocked.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("auth response missing no-store")
	}
	if limiter.count != 1 {
		t.Fatal("disallowed origin should not touch rate limiter")
	}
}

func TestPublicCatalogCORSAllowsCredentialOmittedJSONReads(t *testing.T) {
	handler, _ := newHTTPTestHandler()
	for _, path := range []string{
		"/api/settings/v1/public",
		"/api/service-categories/v1/list",
		"/api/games/v1/list",
	} {
		t.Run(path, func(t *testing.T) {
			preflight := httptest.NewRequest(http.MethodOptions, path, nil)
			preflight.Header.Set("Origin", "http://localhost:3001")
			preflight.Header.Set("Access-Control-Request-Method", "GET")
			preflight.Header.Set("Access-Control-Request-Headers", "content-type, x-request-id")
			preflightResponse := httptest.NewRecorder()
			handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("preflight must not reach endpoint")
			})).ServeHTTP(preflightResponse, preflight)
			if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "*" || preflightResponse.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("unexpected public preflight response: %d %#v", preflightResponse.Code, preflightResponse.Header())
			}

			get := httptest.NewRequest(http.MethodGet, path, nil)
			get.Header.Set("Origin", "http://localhost:3001")
			getResponse := httptest.NewRecorder()
			handler.CORS(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) })).ServeHTTP(getResponse, get)
			if getResponse.Code != http.StatusOK || getResponse.Header().Get("Access-Control-Allow-Origin") != "*" {
				t.Fatalf("unexpected public GET response: %d %#v", getResponse.Code, getResponse.Header())
			}
		})
	}
}

func TestOrderCORSAllowsTrackingAndIdempotencyHeaders(t *testing.T) {
	handler, _ := newHTTPTestHandler()
	handler.allowInsecureHTTP = true
	request := httptest.NewRequest(http.MethodOptions, "/api/orders/v1/checkout", nil)
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight must not reach endpoint") })).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected order preflight 204, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(strings.ToLower(got), "authorization") || !strings.Contains(strings.ToLower(got), "idempotency-key") {
		t.Fatalf("order preflight missing request headers: %q", got)
	}
}

func TestConfiguredLocalhost3001OrderCORS(t *testing.T) {
	limiter := &httpTestLimiter{}
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	handler := NewAdminAuthHandler(&AuthService{Limiter: limiter}, httpTestModel{}, manager, []string{"http://localhost:3001"}, nil, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), true)
	request := httptest.NewRequest(http.MethodOptions, "/api/orders/v1/tracking/order-1/payment", nil)
	request.Header.Set("Origin", "http://localhost:3001")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight must not reach endpoint") })).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3001" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("local order CORS mismatch: status=%d headers=%#v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestCORSRejectsRequestsWithoutOrigin(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	handler.allowInsecureHTTP = true
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	reached := false
	handler.CORS(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		reached = true
		writer.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || reached || !strings.Contains(response.Body.String(), "ADMB01002") {
		t.Fatalf("request without Origin was not rejected: %d %s", response.Code, response.Body.String())
	}
	if limiter.count != 0 {
		t.Fatalf("request without Origin must not consume IP quota, got %d", limiter.count)
	}
}

func TestAuthRejectsNonHTTPSRequestsWithoutTrustedTLSProxy(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("insecure request reached handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ADMB01006") {
		t.Fatalf("insecure request was accepted: %d %s", response.Code, response.Body.String())
	}
	if limiter.count != 0 {
		t.Fatal("insecure request consumed rate limit")
	}
}

func TestLocalHTTPModeOnlyAcceptsLoopbackPeer(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	handler.allowInsecureHTTP = true
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	local := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	local.Header.Set("Origin", "https://dashboard.example.test")
	local.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(response, local)
	if response.Code != http.StatusNoContent {
		t.Fatalf("loopback HTTP request rejected: %d %s", response.Code, response.Body.String())
	}

	remote := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", nil)
	remote.Header.Set("Origin", "https://dashboard.example.test")
	remote.RemoteAddr = "192.0.2.2:5000"
	response = httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(response, remote)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ADMB01006") {
		t.Fatalf("non-loopback HTTP request accepted: %d %s", response.Code, response.Body.String())
	}
	if limiter.count != 1 {
		t.Fatalf("expected only loopback request to consume rate limit, got %d", limiter.count)
	}
}

func TestPreflightAllowsOnlyExpectedMethodAndHeaders(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { t.Fatal("preflight must not reach route") })
	request := httptest.NewRequest(http.MethodOptions, "/api/admin/auth/v1/refresh-token", nil)
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token")
	response := httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("unexpected preflight response: %d", response.Code)
	}
	if limiter.count != 0 {
		t.Fatal("preflight must not consume IP request quota")
	}

	request = httptest.NewRequest(http.MethodOptions, "/api/admin/auth/v1/login", nil)
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "authorization")
	response = httptest.NewRecorder()
	handler.CORS(next).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unexpected header accepted: %d", response.Code)
	}
}

func TestGamePreflightAllowsDashboardCRUDMethods(t *testing.T) {
	handler, _ := newHTTPTestHandler()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		request := httptest.NewRequest(http.MethodOptions, "/api/admin/games/v1/list", nil)
		request.Header.Set("Origin", "https://dashboard.example.test")
		request.Header.Set("Access-Control-Request-Method", method)
		request.Header.Set("Access-Control-Request-Headers", "content-type, x-csrf-token, x-request-id")
		request.TLS = &tls.ConnectionState{}
		response := httptest.NewRecorder()
		handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight reached route") })).ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), method) || !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-Request-Id") {
			t.Errorf("game preflight for %s failed: status=%d headers=%v body=%s", method, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestGameCORSOriginFailureUsesGameErrorCode(t *testing.T) {
	handler, _ := newHTTPTestHandler()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list", nil)
	request.Header.Set("Origin", "https://dashboard.attacker.test")
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("disallowed origin reached route") })).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "GAMB01002") {
		t.Fatalf("game origin failure did not use game contract: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGameMiddlewareRequiresCSRFForReadRequestsAndUsesGameCodes(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data := httpTestModel{active: true, csrfValid: false, profile: AdminProfile{ID: "admin_1", Perms: []string{"game.read"}}}
	handler := NewAdminAuthHandler(&AuthService{}, data, manager, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	response := httptest.NewRecorder()
	handler.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid CSRF reached route") })).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "GAMB01002") {
		t.Fatalf("game GET did not enforce CSRF with game code: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAuthCookiesHaveExactHostCookieSecurityAttributes(t *testing.T) {
	response := httptest.NewRecorder()
	setAuthCookies(response, "access", "refresh", 900)
	cookies := response.Result().Cookies()
	if len(cookies) != 4 {
		t.Fatalf("expected 2 current cookies plus 2 legacy-cookie expirations, got %d", len(cookies))
	}
	current := map[string]bool{}
	legacyExpired := map[string]bool{}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteNoneMode || cookie.Domain != "" {
			t.Fatalf("unsafe cookie attributes: %#v", cookie)
		}
		if cookie.Name == accessCookieName || cookie.Name == refreshCookieName {
			current[cookie.Name] = cookie.MaxAge > 0
		}
		if cookie.Name == legacyAccessCookieName || cookie.Name == legacyRefreshCookieName {
			legacyExpired[cookie.Name] = cookie.MaxAge < 0
		}
	}
	if !current[accessCookieName] || !current[refreshCookieName] || !legacyExpired[legacyAccessCookieName] || !legacyExpired[legacyRefreshCookieName] {
		t.Fatalf("current cookies not issued or legacy cookies not expired: current=%v legacy=%v", current, legacyExpired)
	}
	response = httptest.NewRecorder()
	handler, _ := newHTTPTestHandler()
	handler.clearAuthCookies(response)
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge >= 0 {
			t.Fatalf("cookie was not cleared: %#v", cookie)
		}
	}
}

func TestLocalHTTPCookiesUseLocalhostCompatibleAttributes(t *testing.T) {
	response := httptest.NewRecorder()
	setAuthCookies(response, "access", "refresh", 900, false)
	cookies := response.Result().Cookies()
	if len(cookies) != 4 {
		t.Fatalf("expected 2 current cookies plus 2 legacy-cookie expirations, got %d", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Secure || cookie.HttpOnly != true || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || strings.HasPrefix(cookie.Name, "__Host-") {
			t.Fatalf("unexpected local cookie attributes: %#v", cookie)
		}
	}
	handler, _ := newHTTPTestHandler()
	handler.allowInsecureHTTP = true
	response = httptest.NewRecorder()
	handler.clearAuthCookies(response)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Secure || cookie.MaxAge >= 0 || strings.HasPrefix(cookie.Name, "__Host-") {
			t.Fatalf("local cookie was not cleared with matching attributes: %#v", cookie)
		}
	}
}

func TestLogoutRevokesFamilyAndClearsCookiesIdempotently(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data := &logoutTestModel{httpTestModel: httpTestModel{active: true, csrfValid: true}}
	service := &AuthService{Data: data}
	handler := NewAdminAuthHandler(service, data, manager, nil, nil, nil)

	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/logout", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	request.Header.Set(csrfHeader, "csrf-token")
	response := httptest.NewRecorder()
	handler.logout(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"data":null`) {
		t.Fatalf("logout failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(data.revoked) != 1 || data.revoked[0] != "family_1" {
		t.Fatalf("logout did not revoke the active family: %#v", data.revoked)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 4 {
		t.Fatalf("logout did not clear both cookies: %#v", cookies)
	} else {
		for _, cookie := range cookies {
			if cookie.MaxAge >= 0 {
				t.Fatalf("logout left cookie active: %#v", cookie)
			}
		}
	}

	request = httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/logout", nil)
	response = httptest.NewRecorder()
	handler.logout(response, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 4 || len(data.revoked) != 1 {
		t.Fatalf("logout without a session was not idempotent: status=%d cookies=%#v revoked=%#v", response.Code, response.Result().Cookies(), data.revoked)
	}
}

func TestAuthCookieNamesUseBongastoreAndRecognizeLegacyNames(t *testing.T) {
	access, refresh := authCookieNames(true)
	if access != "__Host-bongastore-admin-access" || refresh != "__Host-bongastore-admin-refresh" {
		t.Fatalf("unexpected secure auth cookie names: %q %q", access, refresh)
	}
	legacyAccess, legacyRefresh := legacyAuthCookieNames(true)
	if legacyAccess != "__Host-jokihub-admin-access" || legacyRefresh != "__Host-jokihub-admin-refresh" {
		t.Fatalf("unexpected secure legacy cookie names: %q %q", legacyAccess, legacyRefresh)
	}
	access, refresh = authCookieNames(false)
	if access != "bongastore-admin-access" || refresh != "bongastore-admin-refresh" {
		t.Fatalf("unexpected localhost auth cookie names: %q %q", access, refresh)
	}
	legacyAccess, legacyRefresh = legacyAuthCookieNames(false)
	if legacyAccess != "jokihub-admin-access" || legacyRefresh != "jokihub-admin-refresh" {
		t.Fatalf("unexpected localhost legacy cookie names: %q %q", legacyAccess, legacyRefresh)
	}
}

func TestMiddlewareAcceptsValidLegacyAccessCookieForActiveSession(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	legacyTokens := TokenManager{Secret: secret, Issuer: "jokihub-admin-api", Audience: "jokihub-admin-dashboard"}
	access, err := legacyTokens.IssueAccess("admin_1", "family_legacy", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	model := httpTestModel{active: true, profile: AdminProfile{ID: "admin_1", Username: "admin"}}
	currentTokens := TokenManager{Secret: secret, Issuer: "bongastore-admin-api", Audience: "bongastore-admin-dashboard"}
	handler := NewAdminAuthHandler(&AuthService{}, model, currentTokens, nil, nil, nil, true)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/auth/v1/session?accountType=ADMIN", nil)
	legacyAccess, _ := legacyAuthCookieNames(false)
	request.AddCookie(&http.Cookie{Name: legacyAccess, Value: access})
	response := httptest.NewRecorder()
	handler.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		profile, ok := AdminProfileFromRequest(request)
		if !ok || profile.ID != "admin_1" {
			t.Fatalf("legacy cookie did not resolve active profile: %#v %t", profile, ok)
		}
		writer.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid legacy session rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLogoutStorageFailureDoesNotClearCookies(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data := &logoutTestModel{httpTestModel: httpTestModel{active: true, csrfValid: true}, revokeErr: errors.New("database secret detail")}
	handler := NewAdminAuthHandler(&AuthService{Data: data}, data, manager, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/logout", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	request.Header.Set(csrfHeader, "csrf-token")
	response := httptest.NewRecorder()
	handler.logout(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database secret detail") {
		t.Fatalf("storage failure was not mapped safely: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("cookies cleared despite failed revocation: %#v", response.Result().Cookies())
	}
}

func TestLogoutInvalidCSRFDoesNotRevokeOrClearCookies(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data := &logoutTestModel{httpTestModel: httpTestModel{active: true, csrfValid: false}}
	handler := NewAdminAuthHandler(&AuthService{Data: data}, data, manager, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/logout", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	request.Header.Set(csrfHeader, "wrong-csrf")
	response := httptest.NewRecorder()
	handler.logout(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ADMB01004") {
		t.Fatalf("invalid CSRF was not rejected: %d %s", response.Code, response.Body.String())
	}
	if len(data.revoked) != 0 || len(response.Result().Cookies()) != 0 {
		t.Fatalf("invalid CSRF changed session state: revoked=%v cookies=%v", data.revoked, response.Result().Cookies())
	}
}

func TestRequestIPTrustsForwardedChainOnlyFromConfiguredProxy(t *testing.T) {
	_, trustedNetwork, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "10.0.0.5:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.1.1.2")
	if got := requestIP(request, []*net.IPNet{trustedNetwork}); got != "198.51.100.8" {
		t.Fatalf("trusted proxy chain resolved to %q", got)
	}

	request.RemoteAddr = "203.0.113.10:443"
	request.Header.Set("X-Forwarded-For", "198.51.100.8")
	if got := requestIP(request, []*net.IPNet{trustedNetwork}); got != "203.0.113.10" {
		t.Fatalf("untrusted forwarded header changed source IP to %q", got)
	}
}

func TestVercelForwardedHeadersProvideClientIPAndHTTPS(t *testing.T) {
	t.Setenv("VERCEL", "1")
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "127.0.0.1:9000"
	request.Header.Set("x-vercel-forwarded-for", "198.51.100.27")
	request.Header.Set("x-forwarded-proto", "https")

	if got := ClientIP(request, nil); got != "198.51.100.27" {
		t.Fatalf("Vercel client IP = %q, want %q", got, "198.51.100.27")
	}
	if !isSecureRequest(request, nil) {
		t.Fatal("Vercel HTTPS forwarding header was not trusted")
	}
}

func TestLoginRejectsUnknownFieldsBeforeCallingService(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", strings.NewReader(`{"identifier":"admin","password":"secret","extra":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.TLS = &tls.ConnectionState{}
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(mux).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"data":null`) {
		t.Fatalf("unexpected validation response: %d %s", response.Code, response.Body.String())
	}
	if limiter.count != 1 {
		t.Fatalf("valid-origin request should count against IP quota before validation, got %d", limiter.count)
	}
}

func TestLoginSuccessSetsCookiesAndReturnsProfileWithCSRFOnly(t *testing.T) {
	profile := AdminProfile{ID: "admin_1", Username: "ops.admin", Name: "Ops Admin", Email: "ops@example.test", Role: Role{ID: "role_1", Name: "Super Admin", Code: "SUPER_ADMIN", System: true}}
	data := &testModelData{identity: LoginIdentity{Profile: profile, Credential: Credential{Hash: "argon-hash"}}}
	limiter := &testLimiter{}
	service := &AuthService{Data: data, Limiter: limiter, Tokens: &testTokens{}, Passwords: testPasswordVerifier{valid: true}}
	handler := NewAdminAuthHandler(service, httpTestModel{}, TokenManager{}, []string{"http://localhost:3000"}, nil, nil, true)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", strings.NewReader(`{"identifier":"ops.admin","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:3000")
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(mux).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login response mismatch: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"username":"ops.admin"`) || !strings.Contains(response.Body.String(), `"csrfToken"`) || strings.Contains(response.Body.String(), `"access-token"`) {
		t.Fatalf("login response did not contain only profile and CSRF data: %s", response.Body.String())
	}
	cookies := response.Result().Cookies()
	values := map[string]string{}
	for _, cookie := range cookies {
		if cookie.MaxAge > 0 {
			values[cookie.Name] = cookie.Value
		}
	}
	if len(cookies) != 4 || values["bongastore-admin-access"] != "access-token" || values["bongastore-admin-refresh"] != "xx" {
		t.Fatalf("login cookies missing or malformed: %#v", cookies)
	}
	if strings.Contains(response.Body.String(), `"`+values["bongastore-admin-refresh"]+`"`) {
		t.Fatalf("refresh token leaked into login response: %s", response.Body.String())
	}
}

func TestLoginCredentialFailureUsesGenericEnvelopeWithoutCookies(t *testing.T) {
	data := &testModelData{findErr: ErrNotFound}
	service := &AuthService{Data: data, Limiter: &testLimiter{}, Tokens: &testTokens{}, Passwords: testPasswordVerifier{}}
	handler := NewAdminAuthHandler(service, httpTestModel{}, TokenManager{}, []string{"http://localhost:3000"}, nil, nil, true)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", strings.NewReader(`{"identifier":"missing","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:3000")
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(mux).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "ADMB01001") || !strings.Contains(response.Body.String(), `"data":null`) {
		t.Fatalf("credential error was not generic: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("failed login set cookies: %#v", response.Result().Cookies())
	}
}

func TestIPRateLimitReturnsRetryAfterAndSafeEnvelope(t *testing.T) {
	handler, limiter := newHTTPTestHandler()
	limiter.blocked = true
	limiter.retry = 27 * time.Second
	request := httptest.NewRequest(http.MethodPost, "/api/admin/auth/v1/login", strings.NewReader("not-json"))
	request.Header.Set("Origin", "https://dashboard.example.test")
	request.TLS = &tls.ConnectionState{}
	request.RemoteAddr = "127.0.0.1:5000"
	response := httptest.NewRecorder()
	handler.CORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("rate-limited request reached handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "27" {
		t.Fatalf("unexpected rate-limit response: %d %s", response.Code, response.Header().Get("Retry-After"))
	}
	if !strings.Contains(response.Body.String(), `"data":null`) || strings.Contains(response.Body.String(), "not-json") {
		t.Fatalf("unsafe rate-limit envelope: %s", response.Body.String())
	}
}

func TestStatusMappingsUseSafeEnvelopeAndRetryHeader(t *testing.T) {
	for _, test := range []struct {
		code   string
		status int
	}{
		{"ADMB02001", 400}, {"ADMB01001", 401}, {"ADMB01002", 403}, {"ADMB02002", 429}, {"ADMT03001", 500},
	} {
		t.Run(test.code, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeServiceError(response, &ServiceError{Code: test.code, HTTPStatus: test.status, RetryAfter: 3 * time.Second, Cause: errors.New("do-not-leak-secret")})
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"data":null`) || strings.Contains(response.Body.String(), "do-not-leak-secret") {
				t.Fatalf("unsafe mapping: %d %s", response.Code, response.Body.String())
			}
			if test.status == 429 && response.Header().Get("Retry-After") != "3" {
				t.Fatalf("Retry-After missing: %#v", response.Header())
			}
		})
	}
}

func TestAdminMiddlewareUsesCurrentPermissionAndCSRFState(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	data := httpTestModel{active: true, csrfValid: true, profile: AdminProfile{ID: "admin_1", Role: Role{Code: "SUPPORT"}, Perms: []string{"orders.read"}}}
	handler := NewAdminAuthHandler(&AuthService{}, data, manager, nil, nil, nil)
	called := false
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		called = true
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	request.Header.Set(csrfHeader, "current-csrf")
	response := httptest.NewRecorder()
	handler.RequirePermission("orders.read", next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("current permission should pass: status=%d body=%s", response.Code, response.Body.String())
	}

	data.profile.Perms = []string{"orders.write"}
	handler = NewAdminAuthHandler(&AuthService{}, data, manager, nil, nil, nil)
	called = false
	response = httptest.NewRecorder()
	handler.RequirePermission("orders.read", next).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || called || !strings.Contains(response.Body.String(), "ADMB01005") {
		t.Fatalf("stale permission was accepted: %d %s", response.Code, response.Body.String())
	}

	data.profile.Perms = []string{"orders.read"}
	data.csrfValid = false
	handler = NewAdminAuthHandler(&AuthService{}, data, manager, nil, nil, nil)
	response = httptest.NewRecorder()
	handler.RequirePermission("orders.read", next).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ADMB01004") {
		t.Fatalf("invalid CSRF token was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestAdminMiddlewareRejectsInvalidJWTAndRevokedFamily(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	valid, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		token string
		data  httpTestModel
	}{
		{name: "invalid jwt", token: "tampered", data: httpTestModel{active: true}},
		{name: "revoked family", token: valid, data: httpTestModel{active: false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewAdminAuthHandler(&AuthService{}, test.data, manager, nil, nil, nil)
			called := false
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.AddCookie(&http.Cookie{Name: accessCookieName, Value: test.token})
			response := httptest.NewRecorder()
			handler.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || called || !strings.Contains(response.Body.String(), "ADMB01003") {
				t.Fatalf("invalid auth state reached protected handler: status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestAdminMiddlewareMapsSessionStoreFailureToSafeTechnicalError(t *testing.T) {
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess("admin_1", "family_1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAdminAuthHandler(&AuthService{}, httpTestModel{resolveErr: errors.New("database credential detail")}, manager, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: access})
	response := httptest.NewRecorder()
	handler.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("store failure reached handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "ADMT03001") || strings.Contains(response.Body.String(), "database credential detail") {
		t.Fatalf("store error was not mapped safely: %d %s", response.Code, response.Body.String())
	}
}
