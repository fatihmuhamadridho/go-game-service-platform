package auth

import "time"

type Role struct {
	ID     string `json:"id" bson:"role_id"`
	Name   string `json:"name" bson:"name"`
	Code   string `json:"code" bson:"code"`
	System bool   `json:"system" bson:"is_system"`
}

type AdminProfile struct {
	ID       string   `json:"id" bson:"admin_account_id"`
	Username string   `json:"username" bson:"username"`
	Name     string   `json:"name" bson:"name"`
	Email    string   `json:"email" bson:"email"`
	Role     Role     `json:"role" bson:"role"`
	Perms    []string `json:"-" bson:"permissions"`
}

type Credential struct {
	AccountID string `json:"-" bson:"admin_account_id"`
	Hash      string `json:"-" bson:"password_hash"`
}

type Session struct {
	ID                 string     `json:"-" bson:"session_id"`
	FamilyID           string     `json:"-" bson:"family_id"`
	AdminAccountID     string     `json:"-" bson:"admin_account_id"`
	RefreshTokenDigest string     `json:"-" bson:"refresh_token_digest"`
	CSRFTokenDigest    string     `json:"-" bson:"csrf_token_digest"`
	CreatedAt          time.Time  `json:"-" bson:"created_at"`
	ExpiresAt          time.Time  `json:"-" bson:"expires_at"`
	RotatedAt          *time.Time `json:"-" bson:"rotated_at"`
	RevokedAt          *time.Time `json:"-" bson:"revoked_at"`
	RevocationReason   string     `json:"-" bson:"revocation_reason,omitempty"`
}

type LoginIdentity struct {
	Profile    AdminProfile `json:"profile"`
	Credential Credential   `json:"-"`
}

type RefreshResult struct {
	Identity AdminProfile `json:"identity"`
	Replay   bool         `json:"replay"`
	Valid    bool         `json:"valid"`
}
