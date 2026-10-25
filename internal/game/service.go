package game

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	gameSuccessCode      = "GAMS00000"
	gameInvalidCode      = "GAMB02001"
	gameConflictCode     = "GAMB02002"
	gamePopularStateCode = "GAMB02003"
	gameNotFoundCode     = "GAMB02004"
	gameTechnicalCode    = "GAMT03001"
	gameUnauthorizedCode = "GAMB01001"
	gameForbiddenCode    = "GAMB01002"
	gameRateLimitCode    = "GAMB02005"
	gameReadIPLimit      = 1200
	gameReadAdminLimit   = 600
	gameWriteIPLimit     = 1200
	gameWriteAdminLimit  = 60
	gameRateLimitWindow  = time.Minute
)

type GameData interface {
	List(context.Context, GameFilter) (GamePage, error)
	FindByID(context.Context, string) (Game, error)
	Create(context.Context, Game, bool) error
	Update(context.Context, string, Game, map[string]any, time.Time) (bool, error)
	Delete(context.Context, string, time.Time) (bool, error)
	Allow(context.Context, string, int, time.Duration, time.Time) (time.Duration, bool, error)
}

type GameService struct {
	Data GameData
	Now  func() time.Time
}

type GameError struct {
	Code       string
	HTTPStatus int
	RetryAfter time.Duration
	Cause      error
}

func (e *GameError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}

func (e *GameError) Unwrap() error { return e.Cause }

func (s *GameService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *GameService) List(ctx context.Context, filter GameFilter) (GamePage, error) {
	if filter.Page == 0 {
		filter.Page = 1
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	filter.Query = strings.TrimSpace(filter.Query)
	if filter.Page < 1 || filter.Page > 999999999 || filter.Limit < 1 || filter.Limit > 100 || len([]rune(filter.Query)) > 100 {
		return GamePage{}, gameError(gameInvalidCode, 400, nil)
	}
	page, err := s.Data.List(ctx, filter)
	if err != nil {
		return GamePage{}, gameError(gameTechnicalCode, 500, err)
	}
	return page, nil
}

func (s *GameService) Detail(ctx context.Context, id string) (Game, error) {
	if err := ValidateGameID(id); err != nil {
		return Game{}, gameError(gameInvalidCode, 400, nil)
	}
	game, err := s.Data.FindByID(ctx, id)
	if errors.Is(err, ErrGameNotFound) {
		return Game{}, gameError(gameNotFoundCode, 404, nil)
	}
	if err != nil {
		return Game{}, gameError(gameTechnicalCode, 500, err)
	}
	return game, nil
}

func (s *GameService) Create(ctx context.Context, request CreateGameRequest, isSystemSuperAdmin bool) error {
	if request.Name == "" || request.Slug == "" || request.ImageURL == "" {
		return gameError(gameInvalidCode, 400, nil)
	}
	if request.IsActive.Set && !isSystemSuperAdmin {
		return gameError(gameForbiddenCode, 403, nil)
	}
	if request.IsPopular.Set && !isSystemSuperAdmin {
		return gameError(gameForbiddenCode, 403, nil)
	}
	game := Game{Name: request.Name, Slug: request.Slug, ImageURL: request.ImageURL}
	if request.Description.Set {
		game.Description = request.Description.Value
	}
	if request.Platform.Set {
		game.Platform = request.Platform.Value
	}
	if request.IsActive.Set {
		game.IsActive = request.IsActive.Value
	}
	if request.IsPopular.Set {
		game.IsPopular = request.IsPopular.Value
	}
	if game.IsPopular && !game.IsActive {
		return gameError(gamePopularStateCode, 400, nil)
	}
	if request.SortOrder.Set {
		game.SortOrder = request.SortOrder.Value
	}
	if err := ValidateGame(&game); err != nil {
		return gameError(gameInvalidCode, 400, err)
	}
	id, err := NewGameID()
	if err != nil {
		return gameError(gameTechnicalCode, 500, err)
	}
	game.ID = id
	game.CreatedAt = s.now()
	game.UpdatedAt = game.CreatedAt
	if err := s.Data.Create(ctx, game, !request.SortOrder.Set); err != nil {
		if errors.Is(err, ErrGameConflict) {
			return gameError(gameConflictCode, 409, nil)
		}
		return gameError(gameTechnicalCode, 500, err)
	}
	return nil
}

func (s *GameService) Update(ctx context.Context, id string, request UpdateGameRequest, isSystemSuperAdmin bool) error {
	if err := ValidateGameID(id); err != nil {
		return gameError(gameInvalidCode, 400, nil)
	}
	if !hasMutableField(request) {
		return gameError(gameInvalidCode, 400, nil)
	}
	if (request.IsActive.Set || request.IsPopular.Set) && !isSystemSuperAdmin {
		return gameError(gameForbiddenCode, 403, nil)
	}
	current, err := s.Data.FindByID(ctx, id)
	if errors.Is(err, ErrGameNotFound) {
		return gameError(gameNotFoundCode, 404, nil)
	}
	if err != nil {
		return gameError(gameTechnicalCode, 500, err)
	}
	updated := current
	changes := make(map[string]any)
	if request.Name.Set {
		if request.Name.Value == nil {
			return gameError(gameInvalidCode, 400, nil)
		}
		updated.Name = *request.Name.Value
		changes["name"] = strings.TrimSpace(*request.Name.Value)
	}
	if request.Slug.Set {
		if request.Slug.Value == nil {
			return gameError(gameInvalidCode, 400, nil)
		}
		updated.Slug = strings.ToLower(strings.TrimSpace(*request.Slug.Value))
		changes["slug"] = updated.Slug
	}
	if request.Description.Set {
		updated.Description = request.Description.Value
		changes["description"] = request.Description.Value
	}
	if request.ImageURL.Set {
		if request.ImageURL.Value == nil {
			return gameError(gameInvalidCode, 400, nil)
		}
		updated.ImageURL = *request.ImageURL.Value
		changes["image_url"] = updated.ImageURL
	}
	if request.Platform.Set {
		updated.Platform = request.Platform.Value
		changes["platform"] = request.Platform.Value
	}
	if request.IsActive.Set {
		updated.IsActive = request.IsActive.Value
		changes["is_active"] = updated.IsActive
	}
	if request.IsPopular.Set {
		updated.IsPopular = request.IsPopular.Value
		changes["is_popular"] = updated.IsPopular
	}
	if request.SortOrder.Set {
		updated.SortOrder = request.SortOrder.Value
		changes["sort_order"] = updated.SortOrder
	}
	if err := ValidateGame(&updated); err != nil {
		if updated.IsPopular && !updated.IsActive {
			return gameError(gamePopularStateCode, 400, nil)
		}
		return gameError(gameInvalidCode, 400, err)
	}
	if equalGame(current, updated) {
		return nil
	}
	if _, err := s.Data.Update(ctx, id, updated, changes, s.now()); err != nil {
		if errors.Is(err, ErrGameNotFound) {
			return gameError(gameNotFoundCode, 404, nil)
		}
		if errors.Is(err, ErrGameState) {
			return gameError(gamePopularStateCode, 400, nil)
		}
		if errors.Is(err, ErrGameConflict) {
			return gameError(gameConflictCode, 409, nil)
		}
		return gameError(gameTechnicalCode, 500, err)
	}
	return nil
}

func (s *GameService) Delete(ctx context.Context, id string) error {
	if err := ValidateGameID(id); err != nil {
		return gameError(gameInvalidCode, 400, nil)
	}
	if _, err := s.Data.Delete(ctx, id, s.now()); err != nil {
		if errors.Is(err, ErrGameNotFound) {
			return gameError(gameNotFoundCode, 404, nil)
		}
		return gameError(gameTechnicalCode, 500, err)
	}
	return nil
}

func hasMutableField(request UpdateGameRequest) bool {
	return request.Name.Set || request.Slug.Set || request.Description.Set || request.ImageURL.Set || request.Platform.Set || request.IsActive.Set || request.IsPopular.Set || request.SortOrder.Set
}

func equalGame(left, right Game) bool {
	return left.Name == right.Name && left.Slug == right.Slug && sameString(left.Description, right.Description) && left.ImageURL == right.ImageURL && sameString(left.Platform, right.Platform) && left.IsActive == right.IsActive && left.IsPopular == right.IsPopular && left.SortOrder == right.SortOrder
}

func sameString(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func gameError(code string, status int, cause error) error {
	return &GameError{Code: code, HTTPStatus: status, Cause: cause}
}
