package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TokenManager struct {
	Secret   []byte
	Issuer   string
	Audience string
}

type AccessClaims struct {
	FamilyID string `json:"sid"`
	jwt.RegisteredClaims
}

func (m TokenManager) IssueAccess(adminID, familyID string, now time.Time) (string, error) {
	if len(m.Secret) < 32 || m.Issuer == "" || m.Audience == "" {
		return "", errors.New("JWT signing configuration invalid")
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	claims := AccessClaims{
		FamilyID: familyID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: adminID, Issuer: m.Issuer, Audience: jwt.ClaimStrings{m.Audience},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			ID: id,
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.Secret)
}

func (m TokenManager) ParseAccess(raw string, now time.Time) (*AccessClaims, error) {
	claims := &AccessClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return m.Secret, nil
	}, jwt.WithIssuer(m.Issuer), jwt.WithAudience(m.Audience), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || token == nil || !token.Valid || claims.Subject == "" || claims.FamilyID == "" || claims.ID == "" || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return nil, errors.New("invalid access token")
	}
	return claims, nil
}

func (TokenManager) NewOpaqueToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
