package game

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrGameNotFound = errors.New("game not found")
	ErrGameConflict = errors.New("game uniqueness conflict")
	ErrGameState    = errors.New("game state conflict")
	ErrInvalidGame  = errors.New("invalid game")
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

const maximumSortOrder int64 = 2147483647

type Game struct {
	ID          string     `json:"id" bson:"game_id"`
	Name        string     `json:"name" bson:"name"`
	Slug        string     `json:"slug" bson:"slug"`
	Description *string    `json:"description" bson:"description"`
	ImageURL    string     `json:"imageUrl" bson:"image_url"`
	Platform    *string    `json:"platform" bson:"platform"`
	IsActive    bool       `json:"isActive" bson:"is_active"`
	IsPopular   bool       `json:"isPopular" bson:"is_popular"`
	SortOrder   int64      `json:"sortOrder" bson:"sort_order"`
	CreatedAt   time.Time  `json:"createdAt" bson:"created_at"`
	UpdatedAt   time.Time  `json:"updatedAt" bson:"updated_at"`
	DeletedAt   *time.Time `json:"-" bson:"deleted_at"`
}

type GameFilter struct {
	Query     string
	IsActive  *bool
	IsPopular *bool
	Page      int64
	Limit     int64
}

type GamePage struct {
	Items      []Game
	Page       int64
	Limit      int64
	Total      int64
	TotalPages int64
}

func NewGameID() (string, error) {
	var raw [16]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate game id entropy: %w", err)
	}
	return "game_" + encodeULID(raw[:]), nil
}

func ValidateGameID(id string) error {
	if len(id) != 31 || !strings.HasPrefix(id, "game_") || !validULIDAlphabet(id[5:]) {
		return ErrInvalidGame
	}
	return nil
}

func ValidateGame(game *Game) error {
	game.Name = strings.TrimSpace(game.Name)
	game.Slug = strings.ToLower(strings.TrimSpace(game.Slug))
	if game.Name == "" || len([]rune(game.Name)) > 100 || len(game.Slug) == 0 || len(game.Slug) > 100 || !slugPattern.MatchString(game.Slug) {
		return ErrInvalidGame
	}
	if game.Description != nil && len([]rune(*game.Description)) > 500 {
		return ErrInvalidGame
	}
	if len(game.ImageURL) == 0 || len(game.ImageURL) > 2048 || !validImageURL(game.ImageURL) {
		return ErrInvalidGame
	}
	if game.Platform != nil && *game.Platform != "MOBILE" && *game.Platform != "PC" && *game.Platform != "CONSOLE" {
		return ErrInvalidGame
	}
	if game.SortOrder < 0 || game.SortOrder > maximumSortOrder || game.IsPopular && !game.IsActive {
		return ErrInvalidGame
	}
	return nil
}

func validImageURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func encodeULID(value []byte) string {
	number := new(big.Int).SetBytes(value)
	base := big.NewInt(32)
	encoded := make([]byte, 26)
	for index := len(encoded) - 1; index >= 0; index-- {
		digit := new(big.Int)
		number.QuoRem(number, base, digit)
		encoded[index] = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"[digit.Int64()]
	}
	return string(encoded)
}

func validULIDAlphabet(value string) bool {
	decoded, err := crockfordDecode(value)
	return err == nil && len(decoded) == 16
}

func crockfordDecode(value string) ([]byte, error) {
	// The first digit carries only three significant bits in canonical ULIDs.
	if len(value) != 26 || value[0] > '7' {
		return nil, ErrInvalidGame
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", char) {
			return nil, ErrInvalidGame
		}
	}
	number := new(big.Int)
	for _, char := range value {
		index := strings.IndexRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", char)
		number.Lsh(number, 5)
		number.Or(number, big.NewInt(int64(index)))
	}
	bytes := number.Bytes()
	if len(bytes) > 16 {
		return nil, ErrInvalidGame
	}
	result := make([]byte, 16)
	copy(result[16-len(bytes):], bytes)
	return result, nil
}
