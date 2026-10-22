package controller

import (
	"context"
	"errors"
	"regexp"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type AdminGameError struct {
	Code    string
	Message string
	Status  int
}

func (e *AdminGameError) Error() string { return e.Message }

type AdminGame struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	ServiceSummary *string   `json:"serviceSummary"`
	ArtworkKey     *string   `json:"artworkKey"`
	ServiceCount   int       `json:"serviceCount"`
	Active         bool      `json:"active"`
	Popular        bool      `json:"popular"`
	SortOrder      int64     `json:"sortOrder"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type AdminGameInput struct {
	Name           *string
	Slug           *string
	ServiceSummary model.OptionalString
	ArtworkKey     model.OptionalString
	IsActive       *bool
	IsPopular      *bool
	SortOrder      *int64
}

type AdminGamePresenter struct {
	repository model.AdminGameRepository
}

func NewAdminGamePresenter(repository model.AdminGameRepository) *AdminGamePresenter {
	return &AdminGamePresenter{repository: repository}
}

var (
	adminGameSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	artworkKeyPattern    = regexp.MustCompile(`(?i)^[a-z0-9_-]{1,160}$`)
	artworkPathPattern   = regexp.MustCompile(`(?i)^/images/games/(?:[a-z0-9_-]+/)*[a-z0-9_-]+\.(?:avif|webp|png|jpe?g)$`)
)

func (p *AdminGamePresenter) List(ctx context.Context) ([]AdminGame, error) {
	games, err := p.repository.ListAdminGames(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AdminGame, 0, len(games))
	for _, game := range games {
		count, err := p.repository.CountActiveServices(ctx, game.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, serializeAdminGame(game, count))
	}
	return result, nil
}

func (p *AdminGamePresenter) Get(ctx context.Context, rawID string) (AdminGame, error) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return AdminGame{}, adminGameFailure("GAMB02001", "ID game tidak valid.", 400)
	}
	game, err := p.repository.FindGameByID(ctx, id)
	if err != nil {
		return AdminGame{}, err
	}
	if game == nil || game.DeletedAt != nil {
		return AdminGame{}, adminGameFailure("GAMB02004", "Game tidak ditemukan.", 404)
	}
	count, err := p.repository.CountActiveServices(ctx, id)
	if err != nil {
		return AdminGame{}, err
	}
	return serializeAdminGame(*game, count), nil
}

func (p *AdminGamePresenter) Create(ctx context.Context, input AdminGameInput, audit model.ActivityContext) (AdminGame, error) {
	if input.Name == nil || input.Slug == nil || !validGameName(*input.Name) || !validGameSlug(*input.Slug) || !validArtwork(input.ArtworkKey) || !validText(input.ServiceSummary, 160) || (input.SortOrder != nil && *input.SortOrder < 0) {
		return AdminGame{}, adminGameFailure("GAMB02001", "Data game tidak valid.", 400)
	}
	name, slug := JSTrim(*input.Name), *input.Slug
	duplicate, err := p.repository.GameNameOrSlugExists(ctx, name, slug, bson.NilObjectID)
	if err != nil {
		return AdminGame{}, err
	}
	if duplicate {
		return AdminGame{}, adminGameFailure("GAMB02002", "Nama atau slug game sudah digunakan.", 409)
	}
	active, popular, order := false, false, int64(0)
	if input.IsActive != nil {
		active = *input.IsActive
	}
	if input.IsPopular != nil {
		popular = *input.IsPopular
	}
	if input.SortOrder != nil {
		order = *input.SortOrder
	}
	if popular && !active {
		return AdminGame{}, adminGameFailure("GAMB02003", "Game nonaktif tidak dapat ditandai populer.", 400)
	}
	game, err := p.repository.CreateGame(ctx, model.GameCatalog{
		Name: name, Slug: slug, ServiceSummary: normalizedString(input.ServiceSummary), ArtworkKey: normalizedString(input.ArtworkKey),
		IsActive: active, IsPopular: popular, SortOrder: order,
	})
	if err != nil {
		return AdminGame{}, err
	}
	_ = p.repository.RecordCatalogActivity(ctx, audit, "GAME_CREATED", "GAME", game.ID, map[string]any{"name": name, "slug": slug})
	return serializeAdminGame(*game, 0), nil
}

func (p *AdminGamePresenter) Update(ctx context.Context, rawID string, input AdminGameInput, audit model.ActivityContext) (AdminGame, error) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return AdminGame{}, adminGameFailure("GAMB02001", "ID game tidak valid.", 400)
	}
	if input.Name == nil && input.Slug == nil && !input.ServiceSummary.Set && !input.ArtworkKey.Set && input.IsActive == nil && input.IsPopular == nil && input.SortOrder == nil {
		return AdminGame{}, adminGameFailure("GAMB02001", "Data game tidak valid.", 400)
	}
	if (input.Name != nil && !validGameName(*input.Name)) || (input.Slug != nil && !validGameSlug(*input.Slug)) || !validArtwork(input.ArtworkKey) || !validText(input.ServiceSummary, 160) || (input.SortOrder != nil && *input.SortOrder < 0) {
		return AdminGame{}, adminGameFailure("GAMB02001", "Data game tidak valid.", 400)
	}
	game, err := p.repository.FindGameByID(ctx, id)
	if err != nil {
		return AdminGame{}, err
	}
	if game == nil || game.DeletedAt != nil {
		return AdminGame{}, adminGameFailure("GAMB02004", "Game tidak ditemukan.", 404)
	}
	name, slug := game.Name, game.Slug
	if input.Name != nil {
		name = JSTrim(*input.Name)
	}
	if input.Slug != nil {
		slug = *input.Slug
	}
	duplicate, err := p.repository.GameNameOrSlugExists(ctx, name, slug, id)
	if err != nil {
		return AdminGame{}, err
	}
	if duplicate {
		return AdminGame{}, adminGameFailure("GAMB02002", "Nama atau slug game sudah digunakan.", 409)
	}
	active, popular := game.IsActive, game.IsPopular
	if input.IsActive != nil {
		active = *input.IsActive
	}
	if input.IsPopular != nil {
		popular = *input.IsPopular
	}
	if !active && popular {
		return AdminGame{}, adminGameFailure("GAMB02003", "Game nonaktif tidak dapat ditandai populer.", 400)
	}
	patch := model.GamePatch{Name: input.Name, Slug: input.Slug, ServiceSummary: input.ServiceSummary, ArtworkKey: input.ArtworkKey, IsActive: input.IsActive, IsPopular: input.IsPopular, SortOrder: input.SortOrder}
	if patch.ServiceSummary.Set {
		patch.ServiceSummary.Value = normalizedString(patch.ServiceSummary)
	}
	if patch.ArtworkKey.Set {
		patch.ArtworkKey.Value = normalizedString(patch.ArtworkKey)
	}
	if err := p.repository.UpdateGame(ctx, id, patch); err != nil {
		return AdminGame{}, err
	}
	changed := map[string]any{}
	if input.Name != nil {
		changed["name"] = name
	}
	if input.Slug != nil {
		changed["slug"] = slug
	}
	if input.ServiceSummary.Set {
		changed["service_summary"] = input.ServiceSummary.Value
	}
	if input.ArtworkKey.Set {
		changed["artwork_key"] = input.ArtworkKey.Value
	}
	if input.IsActive != nil {
		changed["is_active"] = *input.IsActive
		if !*input.IsActive {
			changed["is_popular"] = false
		}
	}
	if input.IsPopular != nil {
		changed["is_popular"] = *input.IsPopular
	}
	if input.SortOrder != nil {
		changed["sort_order"] = *input.SortOrder
	}
	changed["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = p.repository.RecordCatalogActivity(ctx, audit, "GAME_UPDATED", "GAME", id, changed)
	return p.Get(ctx, rawID)
}

func (p *AdminGamePresenter) Delete(ctx context.Context, rawID string, audit model.ActivityContext) (map[string]any, error) {
	id, err := bson.ObjectIDFromHex(rawID)
	if err != nil {
		return nil, adminGameFailure("GAMB02001", "ID game tidak valid.", 400)
	}
	game, err := p.repository.FindGameByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if game == nil || game.DeletedAt != nil {
		return nil, adminGameFailure("GAMB02004", "Game tidak ditemukan.", 404)
	}
	deletedAt := time.Now().UTC()
	if err := p.repository.SoftDeleteGame(ctx, id, deletedAt); err != nil {
		return nil, err
	}
	_ = p.repository.RecordCatalogActivity(ctx, audit, "GAME_DELETED", "GAME", id, map[string]any{"deleted_at": deletedAt.Format(time.RFC3339Nano)})
	return map[string]any{"id": id.Hex(), "deletedAt": deletedAt.Format(time.RFC3339Nano)}, nil
}

func serializeAdminGame(game model.GameCatalog, serviceCount int) AdminGame {
	return AdminGame{ID: game.ID.Hex(), Name: game.Name, Slug: game.Slug, ServiceSummary: game.ServiceSummary, ArtworkKey: game.ArtworkKey, ServiceCount: serviceCount, Active: game.IsActive, Popular: game.IsPopular, SortOrder: game.SortOrder, UpdatedAt: game.UpdatedAt}
}

func validGameName(value string) bool {
	value = JSTrim(value)
	return value != "" && JSStringLength(value) <= 100
}
func validGameSlug(value string) bool {
	return len(value) <= 100 && adminGameSlugPattern.MatchString(value)
}
func validArtwork(value model.OptionalString) bool {
	if !value.Set || value.Value == nil {
		return true
	}
	text := JSTrim(*value.Value)
	return JSStringLength(text) <= 160 && (artworkPathPattern.MatchString(text) || artworkKeyPattern.MatchString(text))
}
func validText(value model.OptionalString, max int) bool {
	return !value.Set || value.Value == nil || JSStringLength(JSTrim(*value.Value)) <= max
}
func adminGameFailure(code, message string, status int) error {
	return &AdminGameError{Code: code, Message: message, Status: status}
}

func ToAdminGameError(err error) *AdminGameError {
	var target *AdminGameError
	if errors.As(err, &target) {
		return target
	}
	return &AdminGameError{Code: "GAMT03001", Message: "Layanan katalog game sedang tidak tersedia.", Status: 500}
}

func AdminPermissionAllowed(claims *VerifiedToken, required string) bool {
	if claims == nil || claims.AccountType != model.AdminAccountType {
		return false
	}
	if claims.Role == "SUPER_ADMIN" || containsPermission(claims.Permissions, "*") {
		return true
	}
	return containsPermission(claims.Permissions, required)
}

func containsPermission(values []string, required string) bool {
	for _, value := range values {
		if value == required {
			return true
		}
	}
	return false
}

func CurrentRequestAudit(claims *VerifiedToken, requestID, sourceIP, userAgent string) model.ActivityContext {
	audit := model.ActivityContext{RequestID: requestID, SourceIP: sourceIP, UserAgent: userAgent}
	if claims != nil {
		audit.ActorID = claims.Subject
	}
	return audit
}

func normalizedString(value model.OptionalString) *string {
	if !value.Set || value.Value == nil {
		return nil
	}
	trimmed := JSTrim(*value.Value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
