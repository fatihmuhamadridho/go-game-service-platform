package game

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	gameCollection     = "game_catalog"
	gameRateCollection = "rate_limit_windows"
	gameRetention      = 60 * 24 * time.Hour
)

type MongoGameRepository struct {
	database *mongo.Database
}

func NewMongoGameRepository(database *mongo.Database) *MongoGameRepository {
	return &MongoGameRepository{database: database}
}

func (r *MongoGameRepository) EnsureIndexes(ctx context.Context) error {
	models := gameIndexModels()
	if _, err := r.database.Collection(gameCollection).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create game catalog indexes: %w", err)
	}
	return nil
}

func gameIndexModels() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "game_id", Value: 1}}, Options: options.Index().SetName("uq_game_catalog_id").SetUnique(true)},
		{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetName("uq_game_catalog_slug").SetUnique(true)},
		{Keys: bson.D{{Key: "sort_order", Value: 1}}, Options: options.Index().SetName("uq_game_catalog_sort_order").SetUnique(true)},
		{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetName("uq_game_catalog_name_ci").SetUnique(true).SetCollation(&options.Collation{Locale: "en", Strength: 2})},
		{Keys: bson.D{{Key: "is_active", Value: 1}, {Key: "is_popular", Value: 1}, {Key: "sort_order", Value: 1}, {Key: "name", Value: 1}, {Key: "game_id", Value: 1}}, Options: options.Index().SetName("ix_game_catalog_admin_list")},
		{Keys: bson.D{{Key: "platform", Value: 1}, {Key: "is_active", Value: 1}, {Key: "sort_order", Value: 1}}, Options: options.Index().SetName("ix_game_catalog_platform").SetPartialFilterExpression(bson.D{{Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}})},
		{Keys: bson.D{{Key: "is_active", Value: 1}, {Key: "is_popular", Value: 1}, {Key: "sort_order", Value: 1}}, Options: options.Index().SetName("ix_game_catalog_popular").SetPartialFilterExpression(bson.D{{Key: "is_active", Value: true}, {Key: "is_popular", Value: true}, {Key: "deleted_at", Value: nil}})},
		{Keys: bson.D{{Key: "deleted_at", Value: 1}}, Options: options.Index().SetName("ttl_game_catalog_deleted_at").SetExpireAfterSeconds(int32(gameRetention.Seconds()))},
	}
}

func (r *MongoGameRepository) List(ctx context.Context, filter GameFilter) (GamePage, error) {
	query := bson.D{{Key: "deleted_at", Value: nil}}
	if filter.Query != "" {
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(filter.Query), Options: "i"}
		query = append(query, bson.E{Key: "$or", Value: bson.A{bson.D{{Key: "name", Value: pattern}}, bson.D{{Key: "slug", Value: pattern}}}})
	}
	if filter.IsActive != nil {
		query = append(query, bson.E{Key: "is_active", Value: *filter.IsActive})
	}
	if filter.IsPopular != nil {
		query = append(query, bson.E{Key: "is_popular", Value: *filter.IsPopular})
	}
	collection := r.database.Collection(gameCollection)
	total, err := collection.CountDocuments(ctx, query)
	if err != nil {
		return GamePage{}, err
	}
	cursor, err := collection.Find(ctx, query, options.Find().SetSort(bson.D{{Key: "sort_order", Value: 1}, {Key: "name", Value: 1}, {Key: "game_id", Value: 1}}).SetSkip((filter.Page-1)*filter.Limit).SetLimit(filter.Limit).SetCollation(&options.Collation{Locale: "en", Strength: 2}))
	if err != nil {
		return GamePage{}, err
	}
	defer cursor.Close(ctx)
	items := make([]Game, 0)
	if err := cursor.All(ctx, &items); err != nil {
		return GamePage{}, err
	}
	pages := int64(0)
	if total > 0 {
		pages = (total + filter.Limit - 1) / filter.Limit
	}
	return GamePage{Items: items, Page: filter.Page, Limit: filter.Limit, Total: total, TotalPages: pages}, nil
}

func (r *MongoGameRepository) FindByID(ctx context.Context, id string) (Game, error) {
	var game Game
	err := r.database.Collection(gameCollection).FindOne(ctx, bson.D{{Key: "game_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&game)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Game{}, ErrGameNotFound
	}
	return game, err
}

func (r *MongoGameRepository) FindPublic(ctx context.Context, filter bson.D) (*mongo.Cursor, error) {
	return r.database.Collection(gameCollection).Find(ctx, publicGameQuery(filter))
}

func publicGameQuery(filter bson.D) bson.D {
	visible := bson.D{{Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}
	if len(filter) == 0 {
		return visible
	}
	return bson.D{{Key: "$and", Value: bson.A{visible, filter}}}
}

func (r *MongoGameRepository) Create(ctx context.Context, game Game, allocateSortOrder bool) error {
	collection := r.database.Collection(gameCollection)
	for attempt := 0; attempt < 64; attempt++ {
		if allocateSortOrder {
			var last Game
			err := collection.FindOne(ctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "sort_order", Value: -1}}).SetProjection(bson.D{{Key: "sort_order", Value: 1}})).Decode(&last)
			if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
				return err
			}
			game.SortOrder = 0
			if err == nil {
				if last.SortOrder >= maximumSortOrder {
					return ErrGameConflict
				}
				game.SortOrder = last.SortOrder + 1
			}
		}
		_, err := collection.InsertOne(ctx, game)
		if err == nil {
			return nil
		}
		if !isDuplicateKey(err) {
			return err
		}
		if !allocateSortOrder || !duplicateIndex(err, "uq_game_catalog_sort_order") {
			return ErrGameConflict
		}
	}
	return ErrGameConflict
}

func (r *MongoGameRepository) Update(ctx context.Context, id string, _ Game, changes map[string]any, updatedAt time.Time) (bool, error) {
	if len(changes) == 0 {
		return false, nil
	}
	filter := bson.D{{Key: "game_id", Value: id}, {Key: "deleted_at", Value: nil}}
	stateGuard := false
	if isPopular, ok := changes["is_popular"].(bool); ok && isPopular {
		if isActive, setsActive := changes["is_active"].(bool); !setsActive || !isActive {
			filter = append(filter, bson.E{Key: "is_active", Value: true})
			stateGuard = true
		}
	}
	if isActive, ok := changes["is_active"].(bool); ok && !isActive {
		if isPopular, setsPopular := changes["is_popular"].(bool); !setsPopular || isPopular {
			filter = append(filter, bson.E{Key: "is_popular", Value: bson.D{{Key: "$ne", Value: true}}})
			stateGuard = true
		}
	}
	changes["updated_at"] = updatedAt
	result, err := r.database.Collection(gameCollection).UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: changes}})
	if err != nil {
		if isDuplicateKey(err) {
			return false, ErrGameConflict
		}
		return false, err
	}
	if result.MatchedCount == 0 {
		if stateGuard {
			var exists Game
			findErr := r.database.Collection(gameCollection).FindOne(ctx, bson.D{{Key: "game_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&exists)
			if findErr == nil {
				return false, ErrGameState
			}
			if !errors.Is(findErr, mongo.ErrNoDocuments) {
				return false, findErr
			}
		}
		return false, ErrGameNotFound
	}
	return result.ModifiedCount > 0, nil
}

func (r *MongoGameRepository) Delete(ctx context.Context, id string, now time.Time) (bool, error) {
	result, err := r.database.Collection(gameCollection).UpdateOne(ctx,
		bson.D{{Key: "game_id", Value: id}, {Key: "deleted_at", Value: nil}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: now}, {Key: "is_active", Value: false}, {Key: "is_popular", Value: false}, {Key: "updated_at", Value: now}}}},
	)
	if err != nil {
		return false, err
	}
	if result.MatchedCount == 1 {
		return true, nil
	}
	var existing Game
	err = r.database.Collection(gameCollection).FindOne(ctx, bson.D{{Key: "game_id", Value: id}}).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, ErrGameNotFound
	}
	if err != nil {
		return false, err
	}
	if existing.DeletedAt != nil {
		return false, nil
	}
	return false, ErrGameNotFound
}

type gameRateWindow struct {
	Events []time.Time `bson:"events"`
}

func (r *MongoGameRepository) Allow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (time.Duration, bool, error) {
	collection := r.database.Collection(gameRateCollection)
	_, err := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: key}}, bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "events", Value: bson.A{}}, {Key: "expires_at", Value: now.Add(window)}}}}, options.UpdateOne().SetUpsert(true))
	if err != nil && !isDuplicateKey(err) {
		return 0, false, err
	}
	start := now.Add(-window)
	filtered := bson.D{{Key: "$filter", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$events", bson.A{}}}}}, {Key: "as", Value: "event"}, {Key: "cond", Value: bson.D{{Key: "$gt", Value: bson.A{"$$event", start}}}}}}}
	filter := bson.D{{Key: "_id", Value: key}, {Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{bson.D{{Key: "$size", Value: filtered}}, limit}}}}}
	pipeline := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "events", Value: bson.D{{Key: "$concatArrays", Value: bson.A{filtered, bson.A{now}}}}}, {Key: "expires_at", Value: now.Add(window)}}}}}
	var result gameRateWindow
	err = collection.FindOneAndUpdate(ctx, filter, pipeline, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return 0, true, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return 0, false, err
	}
	var full gameRateWindow
	if err := collection.FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&full); err != nil {
		return 0, false, err
	}
	oldest := now
	for _, event := range full.Events {
		if event.After(start) && event.Before(oldest) {
			oldest = event
		}
	}
	retry := oldest.Add(window).Sub(now)
	if retry < time.Second {
		retry = time.Second
	}
	return retry, false, nil
}

func isDuplicateKey(err error) bool {
	var write mongo.WriteException
	var command mongo.CommandError
	return errors.As(err, &write) && len(write.WriteErrors) > 0 && write.WriteErrors[0].Code == 11000 || errors.As(err, &command) && command.Code == 11000
}

func duplicateIndex(err error, name string) bool {
	return isDuplicateKey(err) && strings.Contains(err.Error(), name)
}
