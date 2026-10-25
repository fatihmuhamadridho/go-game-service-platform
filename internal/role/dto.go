package role

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var roleIDPattern = regexp.MustCompile(`^role_[A-Za-z0-9_-]+$`)

func validRoleID(id string) bool { return len(id) <= 64 && roleIDPattern.MatchString(id) }

func normalizeCreate(input CreateRequest) (Role, error) {
	name := strings.TrimSpace(input.Name)
	code := strings.ToUpper(strings.TrimSpace(input.Code))
	if len(name) == 0 || utf8.RuneCountInString(name) > 100 || len(code) == 0 || len(code) > 50 || !codePattern.MatchString(code) {
		return Role{}, ErrInvalid
	}
	description, err := normalizeDescription(input.Description)
	if err != nil {
		return Role{}, err
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	if status == "" {
		status = "ACTIVE"
	}
	if status != "ACTIVE" && status != "INACTIVE" {
		return Role{}, ErrInvalid
	}
	permissions, err := normalizePermissions(input.Permissions)
	if err != nil {
		return Role{}, err
	}
	return Role{Name: name, Code: code, Description: description, Status: status, IsSystem: false, Permissions: permissions}, nil
}

type CreateRequest struct {
	Name        string   `json:"name"`
	Code        string   `json:"code"`
	Description *string  `json:"description"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`
}

func normalizeDescription(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if utf8.RuneCountInString(text) > 500 {
		return nil, ErrInvalid
	}
	if text == "" {
		return nil, nil
	}
	return &text, nil
}
func normalizePermissions(values []string) ([]string, error) {
	if len(values) > 100 {
		return nil, ErrInvalid
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || utf8.RuneCountInString(value) > 120 || seen[value] {
			return nil, ErrInvalid
		}
		seen[value] = true
		result = append(result, value)
	}
	return result, nil
}
