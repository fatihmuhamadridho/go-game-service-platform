package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrInvalidSession = errors.New("invalid session")
	ErrRefreshReplay  = errors.New("refresh token replay detected")
	ErrInactiveAdmin  = errors.New("administrator is inactive")
	ErrInvalidRole    = errors.New("administrator has no single active role")
	ErrRateLimited    = errors.New("rate limit exceeded")
)

const (
	AccessLifetime  = 15 * time.Minute
	RefreshLifetime = 7 * 24 * time.Hour
	RateWindow      = 15 * time.Minute
	IPRequestLimit  = 20
	IdentifierLimit = 5
)

type AuthData interface {
	FindLoginIdentity(context.Context, string, string) (LoginIdentity, error)
	CreateLoginSession(context.Context, string, Session) (AdminProfile, error)
	FindSessionByRefreshDigest(context.Context, string) (Session, error)
	RotateSession(context.Context, string, string, Session, time.Time) (RefreshResult, error)
	RevokeSessionFamily(context.Context, string, string, time.Time) error
	ResolveAccess(context.Context, string, string, time.Time) (AdminProfile, bool, error)
	ValidateCSRF(context.Context, string, string) (bool, error)
	RevokeAccountSessions(context.Context, string, string, time.Time) error
}

type RateLimiter interface {
	Allow(context.Context, string, int, time.Time) (time.Duration, bool, error)
	Check(context.Context, string, int, time.Time) (time.Duration, bool, error)
}

type TokenIssuer interface {
	IssueAccess(adminID, familyID string, now time.Time) (string, error)
	NewOpaqueToken() (string, error)
}

type PasswordVerifier interface {
	Verify(encodedHash, password string) (bool, error)
}
