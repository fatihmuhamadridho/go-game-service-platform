package catalog

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	ErrCategoryNotFound = errors.New("service category not found")
	ErrServiceNotFound  = errors.New("service not found")
	ErrCategoryConflict = errors.New("service category slug already exists")
	ErrServiceConflict  = errors.New("service already exists")
	ErrCategoryInUse    = errors.New("service category is in use")
	ErrInvalidCatalog   = errors.New("invalid catalog data")
	slugPattern         = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type Category struct {
	ID          string     `json:"id" bson:"category_id"`
	Name        string     `json:"name" bson:"name"`
	Slug        string     `json:"slug" bson:"slug"`
	Description string     `json:"description" bson:"description"`
	IsActive    bool       `json:"active" bson:"is_active"`
	CreatedAt   time.Time  `json:"createdAt" bson:"created_at"`
	UpdatedAt   time.Time  `json:"updatedAt" bson:"updated_at"`
	DeletedAt   *time.Time `json:"-" bson:"deleted_at,omitempty"`
}

type Service struct {
	ID          string    `json:"id" bson:"service_id"`
	Name        string    `json:"name" bson:"name"`
	GameID      string    `json:"gameId" bson:"game_id"`
	CategoryID  string    `json:"categoryId" bson:"category_id"`
	PriceAmount *int64    `json:"price" bson:"price_amount"`
	Estimate    string    `json:"estimate" bson:"estimate_text"`
	IsActive    bool      `json:"active" bson:"is_active"`
	CreatedAt   time.Time `json:"createdAt" bson:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" bson:"updated_at"`
}

type CategoryRequest struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	IsActive    bool    `json:"isActive"`
}

type ServiceRequest struct {
	Name          string `json:"name"`
	GameID        string `json:"gameId"`
	CategoryID    string `json:"categoryId"`
	PriceAmount   *int64 `json:"priceAmount"`
	IsCustomPrice bool   `json:"isCustomPrice"`
	EstimateText  string `json:"estimateText"`
	IsActive      bool   `json:"isActive"`
}

type ServiceView struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	GameID     string    `json:"gameId"`
	Game       string    `json:"game"`
	CategoryID string    `json:"categoryId"`
	Category   string    `json:"category"`
	Price      *int64    `json:"price"`
	Estimate   string    `json:"estimate"`
	Active     bool      `json:"active"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type CategoryView struct {
	Category
	ServiceCount int `json:"serviceCount"`
}

type GameOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

type CategorySummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type PublicService struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Price        *int64 `json:"price"`
	CustomPrice  bool   `json:"customPrice"`
	Estimate     string `json:"estimate"`
	Illustrative bool   `json:"illustrative,omitempty"`
}

func NewID(prefix string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(entropy[:]), nil
}

func ValidateCategoryRequest(request CategoryRequest) (Category, error) {
	name := strings.TrimSpace(request.Name)
	slug := strings.ToLower(strings.TrimSpace(request.Slug))
	if name == "" || len([]rune(name)) > 100 || slug == "" || len(slug) > 100 || !slugPattern.MatchString(slug) {
		return Category{}, ErrInvalidCatalog
	}
	description := ""
	if request.Description != nil {
		description = strings.TrimSpace(*request.Description)
		if len([]rune(description)) > 500 {
			return Category{}, ErrInvalidCatalog
		}
	}
	return Category{Name: name, Slug: slug, Description: description, IsActive: request.IsActive}, nil
}

func ValidateServiceRequest(request ServiceRequest) (Service, error) {
	name := strings.TrimSpace(request.Name)
	estimate := strings.TrimSpace(request.EstimateText)
	if name == "" || len([]rune(name)) > 120 || request.GameID == "" || len(request.GameID) > 100 || request.CategoryID == "" || len(request.CategoryID) > 100 || len([]rune(estimate)) > 200 {
		return Service{}, ErrInvalidCatalog
	}
	price := request.PriceAmount
	if request.IsCustomPrice {
		price = nil
	} else if price == nil || *price < 1 || *price > 1_000_000_000_000 {
		return Service{}, ErrInvalidCatalog
	}
	return Service{Name: name, GameID: request.GameID, CategoryID: request.CategoryID, PriceAmount: price, Estimate: estimate, IsActive: request.IsActive}, nil
}
