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
	profile   AdminProfile
	active    bool
	csrfValid bool
}

func (data httpTestModel) ValidateCSRF(context.Context, string, string) (bool, error) {
	return data.csrfValid, nil
}
func (data httpTestModel) ResolveAccess(context.Context, string, string, time.Time) (AdminProfile, bool, error) {
	return data.profile, data.active, nil
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

func TestCORSAllowsLocalNonBrowserClientWithoutOrigin(t *testing.T) {
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
	if response.Code != http.StatusNoContent || !reached {
		t.Fatalf("local non-browser request rejected: %d %s", response.Code, response.Body.String())
	}
	if limiter.count != 1 {
		t.Fatalf("expected request without Origin to count against IP quota, got %d", limiter.count)
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

func TestAuthCookiesHaveExactHostCookieSecurityAttributes(t *testing.T) {
	response := httptest.NewRecorder()
	setAuthCookies(response, "access", "refresh", 900)
	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.SameSite != http.SameSiteNoneMode || cookie.Domain != "" {
			t.Fatalf("unsafe cookie attributes: %#v", cookie)
		}
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
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
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
