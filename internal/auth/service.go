package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

type AuthService struct {
	Data      AuthData
	Limiter   RateLimiter
	Tokens    TokenIssuer
	Passwords PasswordVerifier
	Now       func() time.Time
}

type IssuedSession struct {
	Profile       AdminProfile
	AccessToken   string
	RefreshToken  string
	CSRFToken     string
	AccessExpires int
}

type RefreshedSession struct {
	AccessToken   string
	RefreshToken  string
	CSRFToken     string
	AccessExpires int
}

type ServiceError struct {
	Code       string
	HTTPStatus int
	RetryAfter time.Duration
	Cause      error
}

func (e *ServiceError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}

func (e *ServiceError) Unwrap() error { return e.Cause }

func serviceError(code string, status int, cause error) error {
	return &ServiceError{Code: code, HTTPStatus: status, Cause: cause}
}

func (s *AuthService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *AuthService) allow(ctx context.Context, key string, limit int, now time.Time) error {
	retry, allowed, err := s.Limiter.Allow(ctx, key, limit, now)
	if err != nil {
		return serviceError("ADMT03001", 500, err)
	}
	if !allowed {
		return &ServiceError{Code: "ADMB02002", HTTPStatus: 429, RetryAfter: retry}
	}
	return nil
}

func (s *AuthService) check(ctx context.Context, key string, limit int, now time.Time) error {
	retry, allowed, err := s.Limiter.Check(ctx, key, limit, now)
	if err != nil {
		return serviceError("ADMT03001", 500, err)
	}
	if !allowed {
		return &ServiceError{Code: "ADMB02002", HTTPStatus: 429, RetryAfter: retry}
	}
	return nil
}

func (s *AuthService) recordFailure(ctx context.Context, key string, now time.Time) error {
	return s.allow(ctx, key, IdentifierLimit, now)
}

func (s *AuthService) Login(ctx context.Context, identifier, password string) (IssuedSession, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || len([]rune(identifier)) > 254 || password == "" || len([]rune(password)) > 128 {
		return IssuedSession{}, serviceError("ADMB02001", 400, errors.New("invalid login request"))
	}
	now := s.now()
	key := "identifier:" + utils.DigestToken(NormalizeIdentifier(identifier))
	if err := s.check(ctx, key, IdentifierLimit, now); err != nil {
		return IssuedSession{}, err
	}
	field := "username"
	if strings.Contains(identifier, "@") {
		field = "email"
	}
	identity, err := s.Data.FindLoginIdentity(ctx, field, identifier)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalidRole) && !errors.Is(err, ErrInactiveAdmin) {
			return IssuedSession{}, serviceError("ADMT03001", 500, err)
		}
		if _, verifyErr := s.Passwords.Verify("", password); verifyErr != nil {
			return IssuedSession{}, serviceError("ADMT03001", 500, verifyErr)
		}
		if throttleErr := s.recordFailure(ctx, key, now); throttleErr != nil {
			return IssuedSession{}, throttleErr
		}
		return IssuedSession{}, serviceError("ADMB01001", 401, errors.New("invalid credentials"))
	}
	valid, err := s.Passwords.Verify(identity.Credential.Hash, password)
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	if !valid {
		if throttleErr := s.recordFailure(ctx, key, now); throttleErr != nil {
			return IssuedSession{}, throttleErr
		}
		return IssuedSession{}, serviceError("ADMB01001", 401, errors.New("invalid credentials"))
	}
	refresh, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	csrf, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	familyID, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	access, err := s.Tokens.IssueAccess(identity.Profile.ID, familyID, now)
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	sessionID, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	sessionID = "session_" + sessionID
	session := Session{
		ID: sessionID, FamilyID: familyID, AdminAccountID: identity.Profile.ID,
		RefreshTokenDigest: utils.DigestToken(refresh), CSRFTokenDigest: utils.DigestToken(csrf),
		CreatedAt: now, ExpiresAt: now.Add(RefreshLifetime),
	}
	profile, err := s.Data.CreateLoginSession(ctx, identity.Profile.ID, session)
	if err != nil {
		return IssuedSession{}, serviceError("ADMT03001", 500, err)
	}
	return IssuedSession{Profile: profile, AccessToken: access, RefreshToken: refresh, CSRFToken: csrf, AccessExpires: int(AccessLifetime.Seconds())}, nil
}

func (s *AuthService) Refresh(ctx context.Context, refreshToken, csrfToken string) (RefreshedSession, error) {
	if refreshToken == "" || csrfToken == "" || len(csrfToken) > 128 {
		return RefreshedSession{}, serviceError("ADMB01004", 403, errors.New("invalid csrf token"))
	}
	now := s.now()
	old, err := s.Data.FindSessionByRefreshDigest(ctx, utils.DigestToken(refreshToken))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return RefreshedSession{}, serviceError("ADMB01003", 401, ErrInvalidSession)
		}
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	if !constantStringEqual(old.CSRFTokenDigest, utils.DigestToken(csrfToken)) {
		return RefreshedSession{}, serviceError("ADMB01004", 403, errors.New("invalid csrf token"))
	}
	newRefresh, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	newCSRF, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	newSessionID, err := s.Tokens.NewOpaqueToken()
	if err != nil {
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	newSessionID = "session_" + newSessionID
	newSession := Session{
		ID: newSessionID, FamilyID: old.FamilyID, AdminAccountID: old.AdminAccountID,
		RefreshTokenDigest: utils.DigestToken(newRefresh), CSRFTokenDigest: utils.DigestToken(newCSRF),
		CreatedAt: now, ExpiresAt: now.Add(RefreshLifetime),
	}
	result, err := s.Data.RotateSession(ctx, old.ID, utils.DigestToken(csrfToken), newSession, now)
	if err != nil {
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	if result.Replay || !result.Valid {
		return RefreshedSession{}, serviceError("ADMB01003", 401, ErrInvalidSession)
	}
	access, err := s.Tokens.IssueAccess(result.Identity.ID, old.FamilyID, now)
	if err != nil {
		_ = s.Data.RevokeSessionFamily(ctx, old.FamilyID, "TOKEN_ISSUE_FAILED", now)
		return RefreshedSession{}, serviceError("ADMT03001", 500, err)
	}
	return RefreshedSession{AccessToken: access, RefreshToken: newRefresh, CSRFToken: newCSRF, AccessExpires: int(AccessLifetime.Seconds())}, nil
}

func (s *AuthService) Logout(ctx context.Context, familyID string) error {
	if familyID == "" {
		return nil
	}
	if err := s.Data.RevokeSessionFamily(ctx, familyID, "LOGOUT", s.now()); err != nil {
		return serviceError("ADMT03001", 500, err)
	}
	return nil
}

func constantStringEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func NormalizeIdentifier(identifier string) string {
	return strings.ToLower(strings.TrimSpace(identifier))
}

func ValidateIdentifierKind(field, identifier string) error {
	if (field != "email" && field != "username") || identifier == "" {
		return fmt.Errorf("invalid identifier lookup")
	}
	if field == "username" && strings.Contains(identifier, "@") {
		return fmt.Errorf("username must not contain @")
	}
	return nil
}
