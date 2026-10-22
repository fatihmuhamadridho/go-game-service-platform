package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/bcrypt"
)

const accessTokenLifetime = 15 * time.Minute
const refreshTokenLifetime = 7 * 24 * time.Hour

type AuthPresenter struct {
	repository model.AuthRepository
}

var ErrInvalidPasswordReset = errors.New("invalid or expired password reset token")

func NewAuthPresenter(repository model.AuthRepository) *AuthPresenter {
	return &AuthPresenter{repository: repository}
}

type AuthAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type AuthenticatedAccount struct {
	AccountType model.AccountType
	Token       string
	Role        string
	Permissions []string
	Account     AuthAccount
}

type tokenClaims struct {
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
	jwt.RegisteredClaims
}

type VerifiedToken struct {
	AccountType model.AccountType
	Subject     bson.ObjectID
	Username    string
	Role        string
	Permissions []string
	ID          string
	ExpiresAt   time.Time
}

func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if JSStringLength(secret) < 32 {
		return nil, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	return []byte(secret), nil
}

func randomTokenID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func tokenDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (p *AuthPresenter) createAccessToken(account model.Account, accountType model.AccountType, authorization *model.AdminAuthorization) (string, error) {
	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}
	id, err := randomTokenID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims := tokenClaims{
		Username: account.Username,
		Role:     "CUSTOMER",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "jokihub-api", Subject: account.ID.Hex(), Audience: jwt.ClaimStrings{"jokihub-customer"},
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenLifetime)), IssuedAt: jwt.NewNumericDate(now), ID: id,
		},
	}
	if accountType == model.AdminAccountType {
		if authorization == nil {
			return "", errors.New("admin has no active role")
		}
		claims.Role = authorization.Role
		claims.Permissions = authorization.Permissions
		claims.Audience = jwt.ClaimStrings{"jokihub-admin"}
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

func (p *AuthPresenter) verifyAccessToken(ctx context.Context, raw string, accountType model.AccountType) (*VerifiedToken, error) {
	secret, err := jwtSecret()
	if err != nil {
		return nil, err
	}
	claims := &tokenClaims{}
	audience := "jokihub-customer"
	if accountType == model.AdminAccountType {
		audience = "jokihub-admin"
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	}, jwt.WithIssuer("jokihub-api"), jwt.WithAudience(audience), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || token == nil || !token.Valid || claims.Subject == "" || claims.Username == "" || claims.ID == "" {
		return nil, errors.New("invalid access token")
	}
	if accountType == model.CustomerAccount && claims.Role != "CUSTOMER" {
		return nil, errors.New("invalid customer token role")
	}
	if accountType == model.AdminAccountType && claims.Role != "ADMIN" && claims.Role != "SUPER_ADMIN" {
		return nil, errors.New("invalid admin token role")
	}
	subject, err := bson.ObjectIDFromHex(claims.Subject)
	if err != nil {
		return nil, errors.New("invalid access token subject")
	}
	revoked, err := p.repository.IsAccessTokenRevoked(ctx, tokenDigest(claims.ID))
	if err != nil || revoked {
		return nil, errors.New("access token revoked or unavailable")
	}
	expiresAt := time.Time{}
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time
	}
	return &VerifiedToken{AccountType: accountType, Subject: subject, Username: claims.Username, Role: claims.Role, Permissions: claims.Permissions, ID: claims.ID, ExpiresAt: expiresAt}, nil
}

func (p *AuthPresenter) VerifyAccessToken(ctx context.Context, raw string, accountType model.AccountType) (*VerifiedToken, error) {
	return p.verifyAccessToken(ctx, raw, accountType)
}

func (p *AuthPresenter) RevokeAccessToken(ctx context.Context, raw string, accountType model.AccountType) error {
	secret, err := jwtSecret()
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	claims := &tokenClaims{}
	audience := "jokihub-customer"
	if accountType == model.AdminAccountType {
		audience = "jokihub-admin"
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return secret, nil
	}, jwt.WithIssuer("jokihub-api"), jwt.WithAudience(audience), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || token == nil || !token.Valid || claims.ID == "" || claims.ExpiresAt == nil {
		return nil
	}
	if accountType == model.CustomerAccount && claims.Role != "CUSTOMER" {
		return nil
	}
	if accountType == model.AdminAccountType && claims.Role != "ADMIN" && claims.Role != "SUPER_ADMIN" {
		return nil
	}
	if !claims.ExpiresAt.Time.After(time.Now()) {
		return nil
	}
	return p.repository.RevokeAccessToken(ctx, tokenDigest(claims.ID), claims.ExpiresAt.Time)
}

func (p *AuthPresenter) Authenticate(ctx context.Context, identifier, password string, adminOnly bool) (*AuthenticatedAccount, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" || password == "" {
		return nil, nil
	}
	accountTypes := []model.AccountType{model.CustomerAccount, model.AdminAccountType}
	if adminOnly {
		accountTypes = []model.AccountType{model.AdminAccountType}
	}
	for _, accountType := range accountTypes {
		account, err := p.repository.FindAccountByIdentifier(ctx, accountType, identifier)
		if err != nil {
			return nil, err
		}
		if account == nil || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
			continue
		}
		var authorization *model.AdminAuthorization
		if accountType == model.AdminAccountType {
			authorization, err = p.repository.ResolveAdminAuthorization(ctx, account.ID)
			if err != nil {
				return nil, err
			}
			if authorization == nil {
				return nil, nil
			}
		}
		token, err := p.createAccessToken(*account, accountType, authorization)
		if err != nil {
			return nil, err
		}
		result := &AuthenticatedAccount{AccountType: accountType, Token: token, Account: AuthAccount{
			ID: account.ID.Hex(), Name: account.Name, Username: account.Username, Email: account.Email,
		}}
		if authorization != nil {
			result.Role = authorization.Role
			result.Permissions = authorization.Permissions
		}
		return result, nil
	}
	return nil, nil
}

func (p *AuthPresenter) RegisterCustomer(ctx context.Context, name, username, email, password string, whatsapp *string) (*AuthenticatedAccount, error) {
	allowed, err := p.repository.CustomerRegistrationAllowed(ctx)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrRegistrationClosed
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}
	account := model.Account{Username: username, Email: email, Name: name, PasswordHash: string(passwordHash), WhatsApp: whatsapp}
	if err := p.repository.CreateCustomer(ctx, account); err != nil {
		if errors.Is(err, model.ErrDuplicateAccount) {
			return nil, ErrAccountConflict
		}
		return nil, err
	}
	created, err := p.repository.FindAccountByIdentifier(ctx, model.CustomerAccount, username)
	if err != nil || created == nil {
		return nil, err
	}
	token, err := p.createAccessToken(*created, model.CustomerAccount, nil)
	if err != nil {
		return nil, err
	}
	return &AuthenticatedAccount{AccountType: model.CustomerAccount, Token: token, Role: "CUSTOMER", Account: AuthAccount{
		ID: created.ID.Hex(), Name: created.Name, Username: created.Username, Email: created.Email,
	}}, nil
}

var (
	ErrRegistrationClosed = errors.New("customer registration is disabled")
	ErrAccountConflict    = errors.New("username or email already exists")
)

func (p *AuthPresenter) CreateRefreshSession(ctx context.Context, accountType model.AccountType, accountID bson.ObjectID) (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(buffer)
	now := time.Now().UTC()
	session := model.RefreshSession{
		ID: bson.NewObjectID(), TokenHash: tokenDigest(value), AccountType: accountType,
		AccountID: accountID, FamilyID: bson.NewObjectID(), ExpiresAt: now.Add(refreshTokenLifetime),
		CreatedAt: now, IsRevoked: false,
	}
	return value, p.repository.CreateRefreshSession(ctx, session)
}

func (p *AuthPresenter) RotateRefreshSession(ctx context.Context, raw string, accountType model.AccountType) (string, string, *AuthenticatedAccount, error) {
	session, err := p.repository.FindRefreshSession(ctx, tokenDigest(raw), accountType)
	if err != nil || session == nil {
		return "", "", nil, err
	}
	now := time.Now().UTC()
	if session.IsRevoked {
		return "", "", nil, p.repository.RevokeAccountRefreshSessions(ctx, accountType, session.AccountID, now)
	}
	if !session.ExpiresAt.After(now) {
		return "", "", nil, p.repository.RevokeRefreshSession(ctx, session.ID, now)
	}
	account, err := p.repository.FindAccountByID(ctx, accountType, session.AccountID)
	if err != nil || account == nil {
		_ = p.repository.RevokeRefreshFamily(ctx, accountType, session.AccountID, session.FamilyID, now)
		return "", "", nil, err
	}
	var authorization *model.AdminAuthorization
	if accountType == model.AdminAccountType {
		authorization, err = p.repository.ResolveAdminAuthorization(ctx, account.ID)
		if err != nil || authorization == nil {
			_ = p.repository.RevokeRefreshFamily(ctx, accountType, session.AccountID, session.FamilyID, now)
			return "", "", nil, err
		}
	}
	accessToken, err := p.createAccessToken(*account, accountType, authorization)
	if err != nil {
		return "", "", nil, err
	}
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", nil, err
	}
	nextToken := base64.RawURLEncoding.EncodeToString(buffer)
	nextID := bson.NewObjectID()
	if err := p.repository.CreateRefreshSession(ctx, model.RefreshSession{
		ID: nextID, TokenHash: tokenDigest(nextToken), AccountType: accountType, AccountID: account.ID,
		FamilyID: session.FamilyID, ExpiresAt: now.Add(refreshTokenLifetime), CreatedAt: now,
	}); err != nil {
		return "", "", nil, err
	}
	consumed, err := p.repository.ConsumeRefreshSession(ctx, session.ID, nextID, session.FamilyID, now)
	if err != nil || !consumed {
		_ = p.repository.RevokeAccountRefreshSessions(ctx, accountType, session.AccountID, now)
		return "", "", nil, err
	}
	authenticated := &AuthenticatedAccount{AccountType: accountType, Token: accessToken, Account: AuthAccount{
		ID: account.ID.Hex(), Name: account.Name, Username: account.Username, Email: account.Email,
	}}
	if authorization != nil {
		authenticated.Role = authorization.Role
		authenticated.Permissions = authorization.Permissions
	}
	return accessToken, nextToken, authenticated, nil
}

func (p *AuthPresenter) RevokeRefreshToken(ctx context.Context, raw string, accountType model.AccountType) error {
	if raw == "" {
		return nil
	}
	session, err := p.repository.FindRefreshSession(ctx, tokenDigest(raw), accountType)
	if err != nil || session == nil {
		return err
	}
	return p.repository.RevokeRefreshFamily(ctx, accountType, session.AccountID, session.FamilyID, time.Now().UTC())
}

func (p *AuthPresenter) AccountByToken(ctx context.Context, raw string, accountType model.AccountType) (*model.Account, *VerifiedToken, error) {
	claims, err := p.verifyAccessToken(ctx, raw, accountType)
	if err != nil {
		return nil, nil, err
	}
	account, err := p.repository.FindAccountByID(ctx, accountType, claims.Subject)
	return account, claims, err
}

func (p *AuthPresenter) RequestPasswordReset(ctx context.Context, email string, lifetime time.Duration) (string, error) {
	account, err := p.repository.FindCustomerByEmail(ctx, email)
	if err != nil || account == nil {
		return "", err
	}
	if err = p.repository.InvalidatePasswordResetTokens(ctx, account.ID, time.Now().UTC()); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	if err = p.repository.CreatePasswordResetToken(ctx, model.PasswordResetToken{ID: bson.NewObjectID(), TokenHash: tokenDigest(token), TokenType: "PASSWORD_RESET", AccountID: account.ID, ExpiresAt: now.Add(lifetime), CreatedAt: now}); err != nil {
		return "", err
	}
	return token, nil
}

func (p *AuthPresenter) ResetCustomerPassword(ctx context.Context, raw, password string) error {
	record, err := p.repository.FindPasswordResetToken(ctx, tokenDigest(raw))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if record == nil || record.UsedAt != nil || !record.ExpiresAt.After(now) {
		return ErrInvalidPasswordReset
	}
	account, err := p.repository.FindAccountByID(ctx, model.CustomerAccount, record.AccountID)
	if err != nil {
		return err
	}
	if account == nil {
		return ErrInvalidPasswordReset
	}
	consumed, err := p.repository.ConsumePasswordResetToken(ctx, record.ID, now)
	if err != nil {
		return err
	}
	if !consumed {
		return ErrInvalidPasswordReset
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	if err = p.repository.RevokeCustomerRefreshSessions(ctx, account.ID, now); err != nil {
		return err
	}
	if err = p.repository.SetCustomerPassword(ctx, account.ID, string(hash), now); err != nil {
		return err
	}
	return p.repository.InvalidatePasswordResetTokens(ctx, account.ID, now)
}

func (p *AuthPresenter) InvalidatePasswordResetToken(ctx context.Context, raw string) error {
	return p.repository.InvalidatePasswordResetToken(ctx, tokenDigest(raw), time.Now().UTC())
}
