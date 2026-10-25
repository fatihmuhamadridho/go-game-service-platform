package game

import (
	"bytes"
	"encoding/json"
	"errors"
)

type OptionalString struct {
	Set   bool
	Value *string
}

func (value *OptionalString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed string
	if err := json.Unmarshal(data, &parsed); err != nil {
		return errors.New("must be a string or null")
	}
	value.Value = &parsed
	return nil
}

type OptionalBool struct {
	Set   bool
	Value bool
}

func (value *OptionalBool) UnmarshalJSON(data []byte) error {
	value.Set = true
	if err := json.Unmarshal(data, &value.Value); err != nil {
		return errors.New("must be a boolean")
	}
	return nil
}

type OptionalInt struct {
	Set   bool
	Value int64
}

func (value *OptionalInt) UnmarshalJSON(data []byte) error {
	value.Set = true
	var parsed json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&parsed); err != nil {
		return errors.New("must be an integer")
	}
	integer, err := parsed.Int64()
	if err != nil {
		return errors.New("must be an integer")
	}
	value.Value = integer
	return nil
}

type CreateGameRequest struct {
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	Description OptionalString `json:"description"`
	ImageURL    string         `json:"imageUrl"`
	Platform    OptionalString `json:"platform"`
	IsActive    OptionalBool   `json:"isActive"`
	IsPopular   OptionalBool   `json:"isPopular"`
	SortOrder   OptionalInt    `json:"sortOrder"`
}

type UpdateGameRequest struct {
	Name        OptionalString `json:"name"`
	Slug        OptionalString `json:"slug"`
	Description OptionalString `json:"description"`
	ImageURL    OptionalString `json:"imageUrl"`
	Platform    OptionalString `json:"platform"`
	IsActive    OptionalBool   `json:"isActive"`
	IsPopular   OptionalBool   `json:"isPopular"`
	SortOrder   OptionalInt    `json:"sortOrder"`
}

type GameListMetadata struct {
	Page       int64 `json:"page"`
	Limit      int64 `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"totalPages"`
}
