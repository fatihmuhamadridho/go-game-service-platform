package game

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
)

type gameHTTPAuthData struct {
	auth.AuthData
	profile   auth.AdminProfile
	csrfValid bool
}

func (data gameHTTPAuthData) ResolveAccess(context.Context, string, string, time.Time) (auth.AdminProfile, bool, error) {
	return data.profile, true, nil
}

func (data gameHTTPAuthData) ValidateCSRF(context.Context, string, string) (bool, error) {
	return data.csrfValid, nil
}

func newGameTestServer(t *testing.T, profile auth.AdminProfile, data *testGameData) http.Handler {
	t.Helper()
	model := gameHTTPAuthData{profile: profile, csrfValid: true}
	manager := auth.TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	access, err := manager.IssueAccess(profile.ID, "family_game_test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	authHandler := auth.NewAdminAuthHandler(&auth.AuthService{}, model, manager, nil, nil, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	gameHandler := NewAdminGameHandler(&GameService{Data: data}, authHandler, nil)
	mux := http.NewServeMux()
	gameHandler.Register(mux)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.AddCookie(&http.Cookie{Name: "__Host-jokihub-admin-access", Value: access})
		request.Header.Set("X-CSRF-Token", "csrf")
		request.TLS = &tls.ConnectionState{}
		mux.ServeHTTP(writer, request)
	})
}

func TestParseGameFilterAppliesDefaultsAndRejectsUnsupportedOptions(t *testing.T) {
	filter, err := parseGameFilter(map[string][]string{"q": {"  legend  "}, "isActive": {"false"}, "page": {"3"}, "limit": {"50"}})
	if err != nil || filter.Query != "legend" || filter.Page != 3 || filter.Limit != 50 || filter.IsActive == nil || *filter.IsActive {
		t.Fatalf("unexpected parsed filter: %#v err=%v", filter, err)
	}
	defaults, err := parseGameFilter(nil)
	if err != nil || defaults.Page != 1 || defaults.Limit != 20 {
		t.Fatalf("unexpected defaults: %#v err=%v", defaults, err)
	}
	for _, values := range []map[string][]string{{"platform": {"PC"}}, {"sort": {"NAME_ASC"}}, {"isPopular": {"1"}}, {"limit": {"101"}}, {"page": {"0"}}} {
		if _, err := parseGameFilter(values); err == nil {
			t.Errorf("unsupported or invalid filter accepted: %#v", values)
		}
	}
}

func TestListHandlerRequiresPermissionAndReturnsPagination(t *testing.T) {
	data := &testGameData{listPage: GamePage{Items: []Game{validTestGame()}, Page: 1, Limit: 20, Total: 1, TotalPages: 1}}
	profile := auth.AdminProfile{ID: "admin_1", Perms: []string{"game.read"}}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list?page=1", nil)
	response := httptest.NewRecorder()
	newGameTestServer(t, profile, data).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"pagination":{"page":1,"limit":20,"total":1,"totalPages":1}`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list response does not match contract: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}

	profile = auth.AdminProfile{ID: "admin_2", Role: auth.Role{Code: "SUPER_ADMIN", System: true}}
	response = httptest.NewRecorder()
	newGameTestServer(t, profile, data).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list", nil))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "GAMB01002") {
		t.Fatalf("super admin without current permission bypassed permission gate: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGameRateLimitReturnsRetryAfter(t *testing.T) {
	data := &testGameData{limitBlock: true, limitRetry: 17 * time.Second}
	profile := auth.AdminProfile{ID: "admin_1", Perms: []string{"game.read"}}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list", nil)
	response := httptest.NewRecorder()
	newGameTestServer(t, profile, data).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "17" || !strings.Contains(response.Body.String(), gameRateLimitCode) {
		t.Fatalf("rate limit contract mismatch: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestGameRateLimitsUseSeparateReadAndWriteAdminBuckets(t *testing.T) {
	data := &testGameData{}
	profile := auth.AdminProfile{ID: "admin_1", Perms: []string{"game.read"}}
	response := httptest.NewRecorder()
	newGameTestServer(t, profile, data).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/games/v1/list", nil))
	if response.Code != http.StatusOK || len(data.limitCalls) != 2 {
		t.Fatalf("read rate limits were not applied: status=%d calls=%#v", response.Code, data.limitCalls)
	}
	if data.limitCalls[0].limit != gameReadIPLimit || data.limitCalls[1].limit != gameReadAdminLimit || !strings.Contains(data.limitCalls[1].key, ":read:") {
		t.Fatalf("unexpected read rate-limit buckets: %#v", data.limitCalls)
	}

	writeData := &testGameData{blockAdmin: true, limitRetry: 4 * time.Second}
	writeProfile := auth.AdminProfile{ID: "admin_1", Perms: []string{"game.create"}}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/games/v1/create", strings.NewReader(`{"name":"Game","slug":"game","imageUrl":"https://cdn.example.test/game.webp"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	newGameTestServer(t, writeProfile, writeData).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests || len(writeData.limitCalls) != 2 || writeData.limitCalls[1].limit != gameWriteAdminLimit || !strings.Contains(writeData.limitCalls[1].key, ":write:") {
		t.Fatalf("write rate limit used the wrong admin bucket: status=%d calls=%#v body=%s", response.Code, writeData.limitCalls, response.Body.String())
	}
}

func TestDeleteHandlerRejectsRequestBody(t *testing.T) {
	data := &testGameData{}
	profile := auth.AdminProfile{ID: "admin_1", Perms: []string{"game.delete"}}
	request := httptest.NewRequest(http.MethodDelete, "/api/admin/games/v1/delete/game_01ARZ3NDEKTSV4RRFFQ69G5FAV", strings.NewReader("{}"))
	response := httptest.NewRecorder()
	newGameTestServer(t, profile, data).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || data.deleteCall != 0 || !strings.Contains(response.Body.String(), gameInvalidCode) {
		t.Fatalf("DELETE body was not rejected before mutation: status=%d calls=%d body=%s", response.Code, data.deleteCall, response.Body.String())
	}
}
