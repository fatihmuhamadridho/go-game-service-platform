package role

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	Data Data
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) List(ctx context.Context, values map[string][]string) (Page, error) {
	query := Query{SortBy: "createdAt", SortOrder: "DESC", Page: 1, Limit: 10}
	get := func(key string) string {
		if len(values[key]) == 0 {
			return ""
		}
		return values[key][0]
	}
	query.Search = strings.TrimSpace(get("search"))
	if utf8.RuneCountInString(query.Search) > 100 {
		return Page{}, ErrInvalid
	}
	query.Status = strings.ToUpper(strings.TrimSpace(get("status")))
	if query.Status != "" && query.Status != "ACTIVE" && query.Status != "INACTIVE" {
		return Page{}, ErrInvalid
	}
	if v := get("sortBy"); v != "" {
		query.SortBy = v
	}
	if query.SortBy != "name" && query.SortBy != "createdAt" && query.SortBy != "updatedAt" {
		return Page{}, ErrInvalid
	}
	if v := get("sortOrder"); v != "" {
		query.SortOrder = strings.ToUpper(v)
	}
	if query.SortOrder != "ASC" && query.SortOrder != "DESC" {
		return Page{}, ErrInvalid
	}
	if v := get("page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 9999999999 {
			return Page{}, ErrInvalid
		}
		query.Page = n
	}
	if v := get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			return Page{}, ErrInvalid
		}
		query.Limit = n
	}
	return s.Data.List(ctx, query)
}
func (s *Service) Detail(ctx context.Context, id string) (Role, error) {
	if !validRoleID(id) {
		return Role{}, ErrInvalid
	}
	return s.Data.Detail(ctx, id)
}
func (s *Service) Create(ctx context.Context, input CreateRequest) error {
	role, err := normalizeCreate(input)
	if err != nil {
		return err
	}
	role.ID, err = newID()
	if err != nil {
		return err
	}
	role.CreatedAt = s.now()
	role.UpdatedAt = role.CreatedAt
	return s.Data.Create(ctx, role)
}
func (s *Service) Update(ctx context.Context, id string, changes Changes) error {
	if !validRoleID(id) {
		return ErrInvalid
	}
	count := 0
	if changes.Name != nil {
		value := strings.TrimSpace(*changes.Name)
		if len(value) == 0 || utf8.RuneCountInString(value) > 100 {
			return ErrInvalid
		}
		changes.Name = &value
		count++
	}
	if changes.Code != nil {
		value := strings.ToUpper(strings.TrimSpace(*changes.Code))
		if len(value) == 0 || len(value) > 50 || !codePattern.MatchString(value) {
			return ErrInvalid
		}
		changes.Code = &value
		count++
	}
	if changes.Description != nil {
		value, err := normalizeDescription(*changes.Description)
		if err != nil {
			return err
		}
		changes.Description = &value
		count++
	}
	if changes.Status != nil {
		value := strings.ToUpper(strings.TrimSpace(*changes.Status))
		if value != "ACTIVE" && value != "INACTIVE" {
			return ErrInvalid
		}
		changes.Status = &value
		count++
	}
	if changes.Permissions != nil {
		value, err := normalizePermissions(*changes.Permissions)
		if err != nil || len(value) == 0 {
			return ErrInvalid
		}
		changes.Permissions = &value
		count++
	}
	if count == 0 {
		return ErrInvalid
	}
	return s.Data.Update(ctx, id, changes, s.now())
}
func (s *Service) Delete(ctx context.Context, id string) error {
	if !validRoleID(id) {
		return ErrInvalid
	}
	return s.Data.Delete(ctx, id, s.now())
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate role id: %w", err)
	}
	return "role_" + strings.ToUpper(hex.EncodeToString(b[:])), nil
}

func businessCode(err error) (string, int) {
	switch {
	case errors.Is(err, ErrInvalid):
		return "ROLB02001", 400
	case errors.Is(err, ErrNotFound):
		return "ROLB02004", 404
	case errors.Is(err, ErrConflict):
		return "ROLB02002", 409
	case errors.Is(err, ErrProtected):
		return "ROLB02005", 409
	case errors.Is(err, ErrAssigned):
		return "ROLB02006", 409
	case errors.Is(err, ErrPermission):
		return "ROLB02003", 400
	default:
		return "ROLT03001", 500
	}
}
