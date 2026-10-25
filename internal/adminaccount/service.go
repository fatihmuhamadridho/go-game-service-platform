package adminaccount

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	adminIDPattern = regexp.MustCompile(`^admin_[A-Za-z0-9_-]+$`)
	roleIDPattern  = regexp.MustCompile(`^role_[A-Za-z0-9_-]+$`)
)

type Service struct{ Data Data }

func (s *Service) List(ctx context.Context, values url.Values) (Page, error) {
	allowed := map[string]bool{"page": true, "limit": true, "search": true, "status": true, "roleId": true}
	for key, entries := range values {
		if !allowed[key] || len(entries) != 1 {
			return Page{}, ErrInvalid
		}
	}
	query := Query{Page: 1, Limit: 20}
	get := func(key string) string { return values.Get(key) }
	if values.Has("page") {
		raw := get("page")
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 9999999999 {
			return Page{}, ErrInvalid
		}
		query.Page = value
	}
	if values.Has("limit") {
		raw := get("limit")
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return Page{}, ErrInvalid
		}
		query.Limit = value
	}
	query.Search = get("search")
	if !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 100 {
		return Page{}, ErrInvalid
	}
	query.Status = get("status")
	if query.Status != "" && query.Status != "ACTIVE" && query.Status != "INACTIVE" {
		return Page{}, ErrInvalid
	}
	query.RoleID = get("roleId")
	if query.RoleID != "" && !validRoleID(query.RoleID) {
		return Page{}, ErrInvalid
	}
	return s.Data.List(ctx, query)
}

func (s *Service) Detail(ctx context.Context, id string) (Account, error) {
	if !validAdminID(id) {
		return Account{}, ErrInvalid
	}
	return s.Data.Detail(ctx, id)
}

func validAdminID(id string) bool { return len(id) <= 64 && adminIDPattern.MatchString(id) }
func validRoleID(id string) bool  { return len(id) <= 64 && roleIDPattern.MatchString(id) }

func parseQuery(raw string) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, ErrInvalid
	}
	for _, entries := range values {
		for _, value := range entries {
			if strings.ContainsRune(value, '\x00') {
				return nil, ErrInvalid
			}
		}
	}
	return values, nil
}
