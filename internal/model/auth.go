package model

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrDuplicateAccount = errors.New("account identifier already exists")

type AccountType string

const (
	CustomerAccount  AccountType = "CUSTOMER"
	AdminAccountType AccountType = "ADMIN"
)

type Account struct {
	ID           bson.ObjectID `bson:"_id"`
	Username     string        `bson:"username"`
	Email        string        `bson:"email"`
	Name         string        `bson:"name"`
	PasswordHash string        `bson:"password_hash"`
	WhatsApp     *string       `bson:"whatsapp"`
	Status       string        `bson:"status"`
	JoinedAt     time.Time     `bson:"joined_at"`
	CreatedAt    time.Time     `bson:"created_at"`
	UpdatedAt    time.Time     `bson:"updated_at"`
	DeletedAt    *time.Time    `bson:"deleted_at"`
}

type MemberProfile struct {
	UserID bson.ObjectID `bson:"_id"`
	Tier   string        `bson:"tier"`
}
type UserStatusHistory struct {
	ID        bson.ObjectID  `bson:"_id"`
	UserID    bson.ObjectID  `bson:"user_id"`
	OldStatus *string        `bson:"old_status"`
	NewStatus string         `bson:"new_status"`
	ChangedBy *bson.ObjectID `bson:"changed_by"`
	Reason    *string        `bson:"reason"`
	ChangedAt time.Time      `bson:"changed_at"`
}

type AdminMemberRepository interface {
	ListUsers(context.Context) ([]Account, error)
	ListMemberProfiles(context.Context) ([]MemberProfile, error)
	ListAllMemberOrders(context.Context) ([]Order, error)
	FindUserByID(context.Context, bson.ObjectID) (*Account, error)
	FindUserByUsernameOrEmail(context.Context, string, string) (*Account, error)
	FindMemberProfile(context.Context, bson.ObjectID) (*MemberProfile, error)
	ListOrdersByUser(context.Context, bson.ObjectID) ([]Order, error)
	UpdateUser(context.Context, bson.ObjectID, map[string]any) error
	CreateUserStatusHistory(context.Context, UserStatusHistory) error
}

type AdminRole struct {
	ID          bson.ObjectID `bson:"_id"`
	Name        string        `bson:"name"`
	Code        string        `bson:"code"`
	Status      string        `bson:"status"`
	Description *string       `bson:"description"`
	IsSystem    bool          `bson:"is_system"`
	CreatedAt   time.Time     `bson:"created_at"`
	UpdatedAt   time.Time     `bson:"updated_at"`
	DeletedAt   *time.Time    `bson:"deleted_at"`
}
type Permission struct {
	ID          bson.ObjectID `bson:"_id"`
	Code        string        `bson:"code"`
	Name        string        `bson:"name"`
	Description string        `bson:"description"`
	IsSensitive bool          `bson:"is_sensitive"`
	IsActive    bool          `bson:"is_active"`
	CreatedAt   time.Time     `bson:"created_at"`
	UpdatedAt   time.Time     `bson:"updated_at"`
}
type RolePermission struct {
	ID           bson.ObjectID  `bson:"_id"`
	RoleID       bson.ObjectID  `bson:"role_id"`
	PermissionID bson.ObjectID  `bson:"permission_id"`
	GrantedBy    *bson.ObjectID `bson:"granted_by"`
	CreatedAt    time.Time      `bson:"created_at"`
}
type AccountRole struct {
	ID             bson.ObjectID  `bson:"_id"`
	AdminAccountID bson.ObjectID  `bson:"admin_account_id"`
	RoleID         bson.ObjectID  `bson:"role_id"`
	AssignedBy     *bson.ObjectID `bson:"assigned_by"`
	AssignedAt     time.Time      `bson:"assigned_at"`
	RevokedAt      *time.Time     `bson:"revoked_at"`
}
type AdminRoleRepository interface {
	ListRoles(context.Context) ([]AdminRole, error)
	FindRole(context.Context, bson.ObjectID) (*AdminRole, error)
	CreateRole(context.Context, AdminRole) error
	UpdateRole(context.Context, bson.ObjectID, map[string]any) error
	SoftDeleteRole(context.Context, bson.ObjectID, time.Time) error
	ListPermissions(context.Context) ([]Permission, error)
	EnsurePermissions(context.Context, []Permission) ([]Permission, error)
	ListRolePermissions(context.Context) ([]RolePermission, error)
	ReplaceRolePermissions(context.Context, bson.ObjectID, []Permission, bson.ObjectID, time.Time) error
	ListAccountRoles(context.Context) ([]AccountRole, error)
	CountAssignedAccountsForRole(context.Context, bson.ObjectID) (int, error)
	RecordAdminRoleActivity(context.Context, ActivityContext, string, bson.ObjectID, map[string]any) error
}
type AdminAccountRepository interface {
	ListAdminAccounts(context.Context) ([]Account, error)
	FindAdminAccount(context.Context, bson.ObjectID) (*Account, error)
	FindAdminAccountByUsernameOrEmail(context.Context, string, string, ...bson.ObjectID) (*Account, error)
	UpdateAdminAccount(context.Context, bson.ObjectID, map[string]any) error
	SoftDeleteAdminAccount(context.Context, bson.ObjectID, time.Time) error
	ListRoleLinks(context.Context) ([]AccountRole, error)
	ReplaceAccountRoles(context.Context, bson.ObjectID, []bson.ObjectID, bson.ObjectID, time.Time) error
	RevokeAccountRoles(context.Context, bson.ObjectID, time.Time) error
}

type AdminAuthorization struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type RefreshSession struct {
	ID          bson.ObjectID  `bson:"_id"`
	TokenHash   string         `bson:"token_hash"`
	AccountType AccountType    `bson:"account_type"`
	AccountID   bson.ObjectID  `bson:"account_id"`
	FamilyID    bson.ObjectID  `bson:"family_id"`
	ExpiresAt   time.Time      `bson:"expires_at"`
	CreatedAt   time.Time      `bson:"created_at"`
	IsRevoked   bool           `bson:"is_revoked"`
	LastUsedAt  *time.Time     `bson:"last_used_at"`
	RevokedAt   *time.Time     `bson:"revoked_at"`
	ReplacedBy  *bson.ObjectID `bson:"replaced_by_id"`
}

type PasswordResetToken struct {
	ID        bson.ObjectID `bson:"_id"`
	TokenHash string        `bson:"token_hash"`
	TokenType string        `bson:"token_type"`
	AccountID bson.ObjectID `bson:"account_id"`
	ExpiresAt time.Time     `bson:"expires_at"`
	CreatedAt time.Time     `bson:"created_at"`
	UsedAt    *time.Time    `bson:"used_at"`
}

type AuthRepository interface {
	FindAccountByIdentifier(context.Context, AccountType, string) (*Account, error)
	FindAccountByID(context.Context, AccountType, bson.ObjectID) (*Account, error)
	CustomerRegistrationAllowed(context.Context) (bool, error)
	CreateCustomer(context.Context, Account) error
	DeleteCustomer(context.Context, bson.ObjectID) error
	ResolveAdminAuthorization(context.Context, bson.ObjectID) (*AdminAuthorization, error)
	CreateRefreshSession(context.Context, RefreshSession) error
	FindRefreshSession(context.Context, string, AccountType) (*RefreshSession, error)
	ConsumeRefreshSession(context.Context, bson.ObjectID, bson.ObjectID, bson.ObjectID, time.Time) (bool, error)
	RevokeRefreshSession(context.Context, bson.ObjectID, time.Time) error
	RevokeRefreshFamily(context.Context, AccountType, bson.ObjectID, bson.ObjectID, time.Time) error
	RevokeAccountRefreshSessions(context.Context, AccountType, bson.ObjectID, time.Time) error
	RevokeAccessToken(context.Context, string, time.Time) error
	IsAccessTokenRevoked(context.Context, string) (bool, error)
	FindCustomerByEmail(context.Context, string) (*Account, error)
	CreatePasswordResetToken(context.Context, PasswordResetToken) error
	FindPasswordResetToken(context.Context, string) (*PasswordResetToken, error)
	ConsumePasswordResetToken(context.Context, bson.ObjectID, time.Time) (bool, error)
	SetCustomerPassword(context.Context, bson.ObjectID, string, time.Time) error
	RevokeCustomerRefreshSessions(context.Context, bson.ObjectID, time.Time) error
	InvalidatePasswordResetTokens(context.Context, bson.ObjectID, time.Time) error
	InvalidatePasswordResetToken(context.Context, string, time.Time) error
}

type RateLimitRepository interface {
	Increment(context.Context, string, time.Time) (int64, error)
}
