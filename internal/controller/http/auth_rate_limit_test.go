package controllers

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type rateLimitRepositoryStub func(context.Context, string, time.Time) (int64, error)

func (stub rateLimitRepositoryStub) Increment(ctx context.Context, key string, expiresAt time.Time) (int64, error) {
	return stub(ctx, key, expiresAt)
}

func TestEnforceRateLimitAllowsWithinConfiguredLimit(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	previous := authRateLimitStore
	authRateLimitStore = rateLimitRepositoryStub(func(context.Context, string, time.Time) (int64, error) { return 10, nil })
	t.Cleanup(func() { authRateLimitStore = previous })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/auth/v1/login", nil)
	if enforceRateLimit(recorder, request, "login", "user@example.test") {
		t.Fatalf("request was rejected: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEnforceRateLimitReturns429WithRetryAfter(t *testing.T) {
	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	previous := authRateLimitStore
	authRateLimitStore = rateLimitRepositoryStub(func(context.Context, string, time.Time) (int64, error) { return 11, nil })
	t.Cleanup(func() { authRateLimitStore = previous })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/auth/v1/login", nil)
	if !enforceRateLimit(recorder, request, "login", "user@example.test") {
		t.Fatal("request beyond subject limit was allowed")
	}
	if recorder.Code != 429 || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("limited response = %d, Retry-After %q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}

func TestEnforceRateLimitFailsClosedForMissingSecretOrCounterError(t *testing.T) {
	t.Setenv("JWT_SECRET", "short")
	previous := authRateLimitStore
	calls := 0
	authRateLimitStore = rateLimitRepositoryStub(func(context.Context, string, time.Time) (int64, error) {
		calls++
		return 0, errors.New("counter unavailable")
	})
	t.Cleanup(func() { authRateLimitStore = previous })

	missingSecret := httptest.NewRecorder()
	if !enforceRateLimit(missingSecret, httptest.NewRequest("POST", "/", nil), "login", "") || missingSecret.Code != 503 {
		t.Fatalf("missing secret response = %d", missingSecret.Code)
	}
	if calls != 0 {
		t.Fatal("counter must not be called when JWT secret is invalid")
	}

	t.Setenv("JWT_SECRET", strings.Repeat("s", 32))
	counterFailure := httptest.NewRecorder()
	if !enforceRateLimit(counterFailure, httptest.NewRequest("POST", "/", nil), "login", "") || counterFailure.Code != 503 {
		t.Fatalf("counter error response = %d", counterFailure.Code)
	}
}
