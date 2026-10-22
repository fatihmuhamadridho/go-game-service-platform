package mongodb

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type CatalogRepository struct {
	database *mongo.Database
}

func NewCatalogRepository(database *mongo.Database) *CatalogRepository {
	return &CatalogRepository{database: database}
}

func activeFilter() bson.D {
	return bson.D{{Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}
}

func findAll[T any](ctx context.Context, collection *mongo.Collection, filter any, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	cursor, err := collection.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []T
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *CatalogRepository) ListActiveGames(ctx context.Context) ([]model.GameCatalog, error) {
	return findAll[model.GameCatalog](ctx, r.database.Collection("game_catalog"), activeFilter(), options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}}))
}

func (r *CatalogRepository) FindActiveGameBySlug(ctx context.Context, slug string) (*model.GameCatalog, error) {
	var game model.GameCatalog
	filter := append(activeFilter(), bson.E{Key: "slug", Value: slug})
	if err := r.database.Collection("game_catalog").FindOne(ctx, filter).Decode(&game); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &game, nil
}

func (r *CatalogRepository) ListActiveCategories(ctx context.Context) ([]model.ServiceCategory, error) {
	return findAll[model.ServiceCategory](ctx, r.database.Collection("service_category"), activeFilter(), options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
}

func (r *CatalogRepository) ListActiveServices(ctx context.Context, gameID *bson.ObjectID) ([]model.GameService, error) {
	filter := activeFilter()
	if gameID != nil {
		filter = append(filter, bson.E{Key: "game_id", Value: *gameID})
	}
	return findAll[model.GameService](ctx, r.database.Collection("game_service"), filter)
}

func (r *CatalogRepository) GetPublicSettings(ctx context.Context) (*model.PlatformSettings, error) {
	var settings model.PlatformSettings
	if err := r.database.Collection("platform_settings").FindOne(ctx, bson.D{{Key: "singleton_key", Value: "current"}}).Decode(&settings); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &settings, nil
}

func (r *CatalogRepository) ListAdminGames(ctx context.Context) ([]model.GameCatalog, error) {
	return findAll[model.GameCatalog](ctx, r.database.Collection("game_catalog"), bson.D{{Key: "deleted_at", Value: nil}}, options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}}))
}

func (r *CatalogRepository) ListAllGames(ctx context.Context) ([]model.GameCatalog, error) {
	return findAll[model.GameCatalog](ctx, r.database.Collection("game_catalog"), bson.D{})
}

func (r *CatalogRepository) FindGameByID(ctx context.Context, id bson.ObjectID) (*model.GameCatalog, error) {
	var game model.GameCatalog
	if err := r.database.Collection("game_catalog").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&game); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &game, nil
}

func (r *CatalogRepository) GameNameOrSlugExists(ctx context.Context, name, slug string, excludingID bson.ObjectID) (bool, error) {
	rows, err := findAll[model.GameCatalog](ctx, r.database.Collection("game_catalog"), bson.D{})
	if err != nil {
		return false, err
	}
	lower := cases.Lower(language.Indonesian).String
	name = lower(strings.TrimFunc(name, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }))
	for _, row := range rows {
		if row.ID == excludingID {
			continue
		}
		rowName := lower(strings.TrimFunc(row.Name, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }))
		if rowName == name || row.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (r *CatalogRepository) CreateGame(ctx context.Context, game model.GameCatalog) (*model.GameCatalog, error) {
	now := time.Now().UTC()
	game.ID = bson.NewObjectID()
	game.CreatedAt = now
	game.UpdatedAt = now
	game.DeletedAt = nil
	_, err := r.database.Collection("game_catalog").InsertOne(ctx, bson.D{
		{Key: "_id", Value: game.ID}, {Key: "name", Value: game.Name}, {Key: "slug", Value: game.Slug},
		{Key: "service_summary", Value: game.ServiceSummary}, {Key: "artwork_key", Value: game.ArtworkKey},
		{Key: "is_active", Value: game.IsActive}, {Key: "is_popular", Value: game.IsPopular},
		{Key: "sort_order", Value: game.SortOrder}, {Key: "created_at", Value: now},
		{Key: "updated_at", Value: now}, {Key: "deleted_at", Value: nil},
	})
	if err != nil {
		return nil, err
	}
	return &game, nil
}

func (r *CatalogRepository) UpdateGame(ctx context.Context, id bson.ObjectID, patch model.GamePatch) error {
	updates := bson.D{{Key: "updated_at", Value: time.Now().UTC()}}
	if patch.Name != nil {
		updates = append(updates, bson.E{Key: "name", Value: *patch.Name})
	}
	if patch.Slug != nil {
		updates = append(updates, bson.E{Key: "slug", Value: *patch.Slug})
	}
	if patch.ServiceSummary.Set {
		updates = append(updates, bson.E{Key: "service_summary", Value: patch.ServiceSummary.Value})
	}
	if patch.ArtworkKey.Set {
		updates = append(updates, bson.E{Key: "artwork_key", Value: patch.ArtworkKey.Value})
	}
	if patch.IsActive != nil {
		updates = append(updates, bson.E{Key: "is_active", Value: *patch.IsActive})
		if !*patch.IsActive {
			updates = append(updates, bson.E{Key: "is_popular", Value: false})
		}
	}
	if patch.IsPopular != nil {
		updates = append(updates, bson.E{Key: "is_popular", Value: *patch.IsPopular})
	}
	if patch.SortOrder != nil {
		updates = append(updates, bson.E{Key: "sort_order", Value: *patch.SortOrder})
	}
	_, err := r.database.Collection("game_catalog").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: updates}})
	return err
}

func (r *CatalogRepository) SoftDeleteGame(ctx context.Context, id bson.ObjectID, deletedAt time.Time) error {
	_, err := r.database.Collection("game_catalog").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{
		{Key: "deleted_at", Value: deletedAt}, {Key: "is_active", Value: false}, {Key: "is_popular", Value: false}, {Key: "updated_at", Value: deletedAt},
	}}})
	return err
}

func (r *CatalogRepository) CountActiveServices(ctx context.Context, gameID bson.ObjectID) (int, error) {
	count, err := r.database.Collection("game_service").CountDocuments(ctx, bson.D{{Key: "game_id", Value: gameID}, {Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}})
	return int(count), err
}

func (r *CatalogRepository) ListAllCategories(ctx context.Context) ([]model.ServiceCategory, error) {
	return findAll[model.ServiceCategory](ctx, r.database.Collection("service_category"), bson.D{})
}
func (r *CatalogRepository) FindCategoryByID(ctx context.Context, id bson.ObjectID) (*model.ServiceCategory, error) {
	var row model.ServiceCategory
	err := r.database.Collection("service_category").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}
func (r *CatalogRepository) CountServicesByCategory(ctx context.Context, id bson.ObjectID, includeDeleted bool) (int, error) {
	filter := bson.D{{Key: "category_id", Value: id}}
	if !includeDeleted {
		filter = append(filter, bson.E{Key: "deleted_at", Value: nil})
	}
	count, err := r.database.Collection("game_service").CountDocuments(ctx, filter)
	return int(count), err
}
func (r *CatalogRepository) CategoryNameOrSlugExists(ctx context.Context, name, slug string, excluding bson.ObjectID) (bool, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}, {Key: "_id", Value: bson.D{{Key: "$ne", Value: excluding}}}, {Key: "$or", Value: bson.A{bson.D{{Key: "name", Value: bson.D{{Key: "$regex", Value: "^" + regexp.QuoteMeta(strings.TrimSpace(name)) + "$"}, {Key: "$options", Value: "i"}}}}, bson.D{{Key: "slug", Value: slug}}}}}
	count, err := r.database.Collection("service_category").CountDocuments(ctx, filter)
	return count > 0, err
}
func (r *CatalogRepository) CreateCategory(ctx context.Context, row model.ServiceCategory) error {
	_, err := r.database.Collection("service_category").InsertOne(ctx, row)
	return err
}
func (r *CatalogRepository) UpdateCategory(ctx context.Context, id bson.ObjectID, patch map[string]any) error {
	patch["updated_at"] = time.Now().UTC()
	_, err := r.database.Collection("service_category").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: patch}})
	return err
}
func (r *CatalogRepository) SoftDeleteCategory(ctx context.Context, id bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("service_category").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}, {Key: "is_active", Value: false}, {Key: "updated_at", Value: now}}}})
	return err
}
func (r *CatalogRepository) ListAllServices(ctx context.Context) ([]model.GameService, error) {
	return findAll[model.GameService](ctx, r.database.Collection("game_service"), bson.D{})
}
func (r *CatalogRepository) FindServiceByID(ctx context.Context, id bson.ObjectID) (*model.GameService, error) {
	var row model.GameService
	err := r.database.Collection("game_service").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}
func (r *CatalogRepository) CreateService(ctx context.Context, row model.GameService) error {
	_, err := r.database.Collection("game_service").InsertOne(ctx, row)
	return err
}
func (r *CatalogRepository) UpdateService(ctx context.Context, id bson.ObjectID, patch map[string]any) error {
	patch["updated_at"] = time.Now().UTC()
	_, err := r.database.Collection("game_service").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: patch}})
	return err
}
func (r *CatalogRepository) SoftDeleteService(ctx context.Context, id bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("game_service").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}, {Key: "is_active", Value: false}, {Key: "updated_at", Value: now}}}})
	return err
}

func (r *CatalogRepository) RecordCatalogActivity(ctx context.Context, actor model.ActivityContext, action, targetType string, targetID bson.ObjectID, changedFields map[string]any) error {
	var account model.Account
	var actorID any
	var actorName, actorEmail any
	if !actor.ActorID.IsZero() {
		if err := r.database.Collection("admin_account").FindOne(ctx, bson.D{{Key: "_id", Value: actor.ActorID}}).Decode(&account); err == nil {
			actorID, actorName, actorEmail = account.ID, account.Name, account.Email
		}
	}
	label := map[string]string{
		"GAME_CREATED": "Membuat game", "GAME_UPDATED": "Memperbarui game", "GAME_DELETED": "Menghapus game",
	}[action]
	if label == "" {
		label = strings.ToLower(strings.ReplaceAll(action, "_", " "))
	}
	_, err := r.database.Collection("activity_log").InsertOne(ctx, bson.D{
		{Key: "_id", Value: bson.NewObjectID()}, {Key: "actor_admin_id", Value: actorID},
		{Key: "actor_name_snapshot", Value: actorName}, {Key: "actor_email_snapshot", Value: actorEmail},
		{Key: "action_code", Value: action}, {Key: "action_label", Value: label}, {Key: "category", Value: "CATALOG"},
		{Key: "target_type", Value: targetType}, {Key: "target_id", Value: targetID}, {Key: "target_reference", Value: targetID.Hex()},
		{Key: "result", Value: "SUCCESS"}, {Key: "failure_code", Value: nil}, {Key: "reason", Value: nil},
		{Key: "changed_fields", Value: changedFields}, {Key: "request_id", Value: nullIfEmpty(actor.RequestID)},
		{Key: "source_ip", Value: nullIfEmpty(actor.SourceIP)}, {Key: "user_agent", Value: nullIfEmpty(actor.UserAgent)},
		{Key: "occurred_at", Value: time.Now().UTC()},
	})
	return err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var _ model.CatalogRepository = (*CatalogRepository)(nil)
var _ model.AdminGameRepository = (*CatalogRepository)(nil)
