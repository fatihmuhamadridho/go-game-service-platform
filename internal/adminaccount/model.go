package adminaccount

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid       = errors.New("invalid administrator account request")
	ErrNotFound      = errors.New("administrator account not found")
	ErrRoleInvariant = errors.New("administrator account must have exactly one active role")
)

type RoleSummary struct {
	ID     string `json:"id" bson:"role_id"`
	Name   string `json:"name" bson:"name"`
	Code   string `json:"code" bson:"code"`
	System bool   `json:"system" bson:"is_system"`
}

// Account contains only the directory projection. The BSON aggregation also
// projects these fields explicitly so credential/session documents never join
// or enter the response path.
type Account struct {
	ID                    string      `json:"id" bson:"admin_account_id"`
	Username              string      `json:"username" bson:"username"`
	Name                  string      `json:"name" bson:"name"`
	Email                 string      `json:"email" bson:"email"`
	Status                string      `json:"status" bson:"status"`
	Role                  RoleSummary `json:"role" bson:"role"`
	LastActive            *time.Time  `json:"lastActive" bson:"last_active"`
	UpdatedAt             time.Time   `json:"updatedAt" bson:"updated_at"`
	ActiveAssignmentCount int         `json:"-" bson:"active_assignment_count"`
	ActiveRoleCount       int         `json:"-" bson:"active_role_count"`
}

type Query struct {
	Page   int
	Limit  int
	Search string
	Status string
	RoleID string
}

type Pagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

type Page struct {
	Accounts   []Account
	Pagination Pagination
}

type Data interface {
	List(context.Context, Query) (Page, error)
	Detail(context.Context, string) (Account, error)
}

type RateLimiter interface {
	AllowWindow(context.Context, string, int, time.Duration, time.Time) (time.Duration, bool, error)
}
