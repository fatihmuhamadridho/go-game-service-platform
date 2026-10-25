package role

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid    = errors.New("invalid role request")
	ErrNotFound   = errors.New("role not found")
	ErrConflict   = errors.New("role conflict")
	ErrProtected  = errors.New("system role is protected")
	ErrAssigned   = errors.New("role is assigned to an active account")
	ErrPermission = errors.New("permission is unknown or inactive")
)

type Role struct {
	ID          string     `json:"id" bson:"role_id"`
	Name        string     `json:"name" bson:"name"`
	Code        string     `json:"code" bson:"code"`
	Description *string    `json:"description" bson:"description"`
	Status      string     `json:"status" bson:"status"`
	IsSystem    bool       `json:"isSystem" bson:"is_system"`
	Permissions []string   `json:"permissions" bson:"-"`
	CreatedAt   time.Time  `json:"createdAt" bson:"created_at"`
	UpdatedAt   time.Time  `json:"updatedAt" bson:"updated_at"`
	DeletedAt   *time.Time `json:"-" bson:"deleted_at,omitempty"`
}

type Query struct {
	Search, Status, SortBy, SortOrder string
	Page, Limit                       int
}
type Page struct {
	Roles      []Role     `json:"data"`
	Pagination Pagination `json:"pagination"`
}
type Pagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"totalPages"`
}

type Changes struct {
	Name        *string   `json:"name"`
	Code        *string   `json:"code"`
	Description **string  `json:"description"`
	Status      *string   `json:"status"`
	Permissions *[]string `json:"permissions"`
}

type Data interface {
	List(context.Context, Query) (Page, error)
	Detail(context.Context, string) (Role, error)
	Create(context.Context, Role) error
	Update(context.Context, string, Changes, time.Time) error
	Delete(context.Context, string, time.Time) error
}

type RateLimiter interface {
	AllowWindow(context.Context, string, int, time.Duration, time.Time) (time.Duration, bool, error)
}
