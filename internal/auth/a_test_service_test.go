package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

type testModelData struct {
	identity       LoginIdentity
	findField      string
	findIdentifier string
	findErr        error
	session        Session
	createErr      error
	refreshSession Session
	refreshResult  RefreshResult
	replayErr      error
	rotatedSession Session
	rotatedCSRF    string
	rotatedCalls   int
	revokedFamily  string
}

func (r *testModelData) FindLoginIdentity(_ context.Context, field, identifier string) (LoginIdentity, error) {
	r.findField = field
	r.findIdentifier = identifier
	return r.identity, r.findErr
}
func (r *testModelData) CreateLoginSession(_ context.Context, _ string, session Session) (AdminProfile, error) {
	r.session = session
	return r.identity.Profile, r.createErr
}
func (r *testModelData) FindSessionByRefreshDigest(_ context.Context, tokenDigest string) (Session, error) {
	if tokenDigest == "" {
		return Session{}, ErrNotFound
	}
	return r.refreshSession, nil
}

func (r *testModelData) RotateSession(_ context.Context, _, csrfDigest string, session Session, _ time.Time) (RefreshResult, error) {
	r.rotatedCalls++
	r.rotatedCSRF = csrfDigest
	r.rotatedSession = session
	return r.refreshResult, r.replayErr
}

func (r *testModelData) RevokeSessionFamily(_ context.Context, familyID, _ string, _ time.Time) error {
	r.revokedFamily = familyID
	return nil
}
func (r *testModelData) ResolveAccess(context.Context, string, string, time.Time) (AdminProfile, bool, error) {
	return AdminProfile{}, false, nil
}
func (r *testModelData) ValidateCSRF(context.Context, string, string) (bool, error) {
	return false, nil
}
func (r *testModelData) RevokeAccountSessions(context.Context, string, string, time.Time) error {
	return nil
}

type testLimiter struct {
	checks  int
	allows  []string
	blocked bool
}

type countedFailureLimiter struct{ counts map[string]int }

func (l *countedFailureLimiter) Allow(_ context.Context, key string, limit int, _ time.Time) (time.Duration, bool, error) {
	if l.counts == nil {
		l.counts = make(map[string]int)
	}
	if l.counts[key] >= limit {
		return time.Minute, false, nil
	}
	l.counts[key]++
	return 0, true, nil
}
func (l *countedFailureLimiter) Check(_ context.Context, key string, limit int, _ time.Time) (time.Duration, bool, error) {
	return time.Minute, l.counts[key] < limit, nil
}

func (l *testLimiter) Allow(_ context.Context, key string, _ int, _ time.Time) (time.Duration, bool, error) {
	l.allows = append(l.allows, key)
	return time.Minute, !l.blocked, nil
}
func (l *testLimiter) Check(_ context.Context, _ string, _ int, _ time.Time) (time.Duration, bool, error) {
	l.checks++
	return time.Minute, !l.blocked, nil
}

type testTokens struct{ sequence int }

func (t *testTokens) IssueAccess(string, string, time.Time) (string, error) {
	return "access-token", nil
}
func (t *testTokens) NewOpaqueToken() (string, error) {
	t.sequence++
	return strings.Repeat("x", t.sequence+1), nil
}

type testPasswordVerifier struct {
	valid bool
	err   error
}

func (p testPasswordVerifier) Verify(string, string) (bool, error) { return p.valid, p.err }

func newTestService(dataModel *testModelData, limiter RateLimiter, verifier PasswordVerifier) *AuthService {
	return &AuthService{Data: dataModel, Limiter: limiter, Tokens: &testTokens{}, Passwords: verifier, Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }}
}

func TestLoginIssuesSessionForTrimmedEmailLookup(t *testing.T) {
	dataModel := &testModelData{identity: LoginIdentity{Profile: AdminProfile{ID: "admin_1"}, Credential: Credential{Hash: "argon-hash"}}}
	limiter := &testLimiter{}
	service := newTestService(dataModel, limiter, testPasswordVerifier{valid: true})
	issued, err := service.Login(context.Background(), "  OPS@EXAMPLE.TEST  ", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if issued.AccessToken != "access-token" || issued.Profile.ID != "admin_1" {
		t.Fatalf("unexpected issued session: %#v", issued)
	}
	if dataModel.findField != "email" || dataModel.findIdentifier != "OPS@EXAMPLE.TEST" {
		t.Fatalf("trimmed email lookup mismatch: field=%q identifier=%q", dataModel.findField, dataModel.findIdentifier)
	}
	if dataModel.session.AdminAccountID != "admin_1" || dataModel.session.RefreshTokenDigest == "" || dataModel.session.CSRFTokenDigest == "" {
		t.Fatalf("session digests were not persisted: %#v", dataModel.session)
	}
	if dataModel.session.RefreshTokenDigest == issued.RefreshToken || dataModel.session.CSRFTokenDigest == issued.CSRFToken {
		t.Fatal("raw token persisted instead of digest")
	}
	if limiter.checks != 1 || len(limiter.allows) != 0 {
		t.Fatalf("unexpected identifier throttle flow: %#v, checks %d", limiter.allows, limiter.checks)
	}
}

func TestLoginUsesTrimmedUsernameLookupWhenIdentifierHasNoAtSign(t *testing.T) {
	dataModel := &testModelData{identity: LoginIdentity{Profile: AdminProfile{ID: "admin_1"}, Credential: Credential{Hash: "argon-hash"}}}
	service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{valid: true})
	if _, err := service.Login(context.Background(), "  Ops.User  ", "secret"); err != nil {
		t.Fatal(err)
	}
	if dataModel.findField != "username" || dataModel.findIdentifier != "Ops.User" {
		t.Fatalf("trimmed username lookup mismatch: field=%q identifier=%q", dataModel.findField, dataModel.findIdentifier)
	}
}

func TestLoginReturnsSameGenericFailureForMissingIdentityAndWrongPassword(t *testing.T) {
	for _, test := range []struct {
		name      string
		dataModel *testModelData
		verifier  testPasswordVerifier
	}{
		{name: "missing", dataModel: &testModelData{findErr: ErrNotFound}, verifier: testPasswordVerifier{}},
		{name: "wrong password", dataModel: &testModelData{identity: LoginIdentity{Credential: Credential{Hash: "hash"}}}, verifier: testPasswordVerifier{valid: false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newTestService(test.dataModel, &testLimiter{}, test.verifier)
			_, err := service.Login(context.Background(), "operator", "wrong")
			var typed *ServiceError
			if !errors.As(err, &typed) || typed.HTTPStatus != 401 || typed.Code != "ADMB01001" {
				t.Fatalf("expected generic auth error, got %v", err)
			}
		})
	}
}

func TestLoginInvalidInputDoesNotTouchRateLimiter(t *testing.T) {
	limiter := &testLimiter{}
	service := newTestService(&testModelData{}, limiter, testPasswordVerifier{})
	_, err := service.Login(context.Background(), "  ", "secret")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 400 {
		t.Fatalf("expected validation error, got %v", err)
	}
	if len(limiter.allows) != 0 || limiter.checks != 0 {
		t.Fatal("invalid input mutated rate limit state")
	}
}

func TestLoginInfrastructureFailureMapsToTechnicalError(t *testing.T) {
	service := newTestService(&testModelData{findErr: errors.New("mongo down")}, &testLimiter{}, testPasswordVerifier{})
	_, err := service.Login(context.Background(), "operator", "secret")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 500 || typed.Code != "ADMT03001" {
		t.Fatalf("expected technical error, got %v", err)
	}
}

func TestIdentifierThrottleCountsOnlyFailedCredentialsAndBlocksSixthAttempt(t *testing.T) {
	limiter := &countedFailureLimiter{counts: make(map[string]int)}
	dataModel := &testModelData{identity: LoginIdentity{Credential: Credential{Hash: "hash"}}}
	service := newTestService(dataModel, limiter, testPasswordVerifier{valid: false})
	for attempt := 1; attempt <= IdentifierLimit; attempt++ {
		_, err := service.Login(context.Background(), "operator", "wrong")
		var typed *ServiceError
		if !errors.As(err, &typed) || typed.HTTPStatus != 401 {
			t.Fatalf("attempt %d expected 401, got %v", attempt, err)
		}
	}
	_, err := service.Login(context.Background(), "operator", "wrong")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 429 || typed.RetryAfter <= 0 {
		t.Fatalf("sixth attempt expected throttling, got %v", err)
	}
	if limiter.counts["identifier:"+utils.DigestToken("operator")] != IdentifierLimit {
		t.Fatalf("expected 5 stored failures, got %d", limiter.counts["identifier:"+utils.DigestToken("operator")])
	}
}

func TestRefreshRotatesWithinSameFamilyAndStoresDigestsOnly(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	dataModel := &testModelData{
		refreshSession: Session{ID: "session_old", FamilyID: "family_1", AdminAccountID: "admin_1", CSRFTokenDigest: utils.DigestToken("csrf-old"), ExpiresAt: now.Add(time.Hour)},
		refreshResult:  RefreshResult{Valid: true, Identity: AdminProfile{ID: "admin_1"}},
	}
	service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{})
	issued, err := service.Refresh(context.Background(), "refresh-old", "csrf-old")
	if err != nil {
		t.Fatal(err)
	}
	if issued.AccessToken != "access-token" || issued.AccessExpires != int(AccessLifetime.Seconds()) {
		t.Fatalf("unexpected refreshed session: %#v", issued)
	}
	if dataModel.rotatedCalls != 1 || dataModel.rotatedSession.FamilyID != "family_1" || dataModel.rotatedSession.RefreshTokenDigest == issued.RefreshToken || dataModel.rotatedSession.CSRFTokenDigest == issued.CSRFToken {
		t.Fatalf("refresh generation was not safely rotated: %#v", dataModel.rotatedSession)
	}
}

func TestRefreshRejectsInvalidCSRFBeforeRotation(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	dataModel := &testModelData{refreshSession: Session{ID: "session_old", FamilyID: "family_1", CSRFTokenDigest: utils.DigestToken("csrf-old"), ExpiresAt: now.Add(time.Hour)}}
	service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{})
	_, err := service.Refresh(context.Background(), "refresh-old", "csrf-invalid")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 403 || dataModel.rotatedCalls != 0 {
		t.Fatalf("invalid CSRF did not stop rotation: err=%v calls=%d", err, dataModel.rotatedCalls)
	}
}

func TestRefreshRejectsExpiredAndRevokedGenerationsWithoutReturningTokens(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		session Session
	}{
		{name: "expired", session: Session{ID: "session_old", FamilyID: "family_1", CSRFTokenDigest: utils.DigestToken("csrf"), ExpiresAt: now}},
		{name: "revoked", session: Session{ID: "session_old", FamilyID: "family_1", CSRFTokenDigest: utils.DigestToken("csrf"), ExpiresAt: now.Add(time.Hour), RevokedAt: &now}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataModel := &testModelData{refreshSession: test.session, refreshResult: RefreshResult{Valid: false}}
			service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{})
			issued, err := service.Refresh(context.Background(), "refresh-token", "csrf")
			var typed *ServiceError
			if !errors.As(err, &typed) || typed.HTTPStatus != 401 || typed.Code != "ADMB01003" {
				t.Fatalf("expected generic invalid-session response, got %v", err)
			}
			if dataModel.rotatedCalls != 1 || issued.AccessToken != "" || issued.RefreshToken != "" || issued.CSRFToken != "" {
				t.Fatalf("invalid generation produced tokens or skipped validation: issued=%#v calls=%d", issued, dataModel.rotatedCalls)
			}
		})
	}
}

func TestRefreshStoreFailureReturnsTechnicalErrorWithoutSessionTokens(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	dataModel := &testModelData{
		refreshSession: Session{ID: "session_old", FamilyID: "family_1", CSRFTokenDigest: utils.DigestToken("csrf"), ExpiresAt: now.Add(time.Hour)},
		replayErr:      errors.New("store unavailable"),
	}
	service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{})
	issued, err := service.Refresh(context.Background(), "refresh-token", "csrf")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 500 || typed.Code != "ADMT03001" {
		t.Fatalf("expected technical store error, got %v", err)
	}
	if issued.AccessToken != "" || issued.RefreshToken != "" || issued.CSRFToken != "" {
		t.Fatalf("store error returned session tokens: %#v", issued)
	}
}

func TestRefreshReplayReturnsGeneric401AndLogoutRevokesOneFamily(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	dataModel := &testModelData{
		refreshSession: Session{ID: "session_old", FamilyID: "family_1", AdminAccountID: "admin_1", CSRFTokenDigest: utils.DigestToken("csrf-old"), ExpiresAt: now.Add(time.Hour)},
		refreshResult:  RefreshResult{Replay: true},
	}
	service := newTestService(dataModel, &testLimiter{}, testPasswordVerifier{})
	_, err := service.Refresh(context.Background(), "refresh-old", "csrf-old")
	var typed *ServiceError
	if !errors.As(err, &typed) || typed.HTTPStatus != 401 || typed.Code != "ADMB01003" {
		t.Fatalf("replay error was not generic: %v", err)
	}
	if err := service.Logout(context.Background(), "family_1"); err != nil || dataModel.revokedFamily != "family_1" {
		t.Fatalf("logout did not revoke family: family=%q err=%v", dataModel.revokedFamily, err)
	}
}
