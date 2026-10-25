package publicgame

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	gameCollection = "game_catalog"
	rateCollection = "rate_limit_windows"
)

type Data interface {
	List(context.Context, Filter) (Page, error)
	Allow(context.Context, string, time.Time) (time.Duration, bool, error)
}

type MongoRepository struct {
	database *mongo.Database
}

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{database: database}
}

// EnsureIndexes creates the public sort index. Game management already creates
// the shared popular, platform, name, and slug indexes, while auth creates the
// shared rate-limit TTL index.
func (r *MongoRepository) EnsureIndexes(ctx context.Context) error {
	models := []mongo.IndexModel{
		{Keys: bson.D{{Key: "is_active", Value: 1}, {Key: "deleted_at", Value: 1}, {Key: "sort_order", Value: 1}, {Key: "game_id", Value: 1}}, Options: options.Index().SetName("ix_game_catalog_public_sort").SetPartialFilterExpression(bson.D{{Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}})},
	}
	if _, err := r.database.Collection(gameCollection).Indexes().CreateMany(ctx, models); err != nil {
		return err
	}
	return nil
}

func (r *MongoRepository) List(ctx context.Context, filter Filter) (Page, error) {
	query := bson.D{{Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}
	if filter.Popular {
		query = append(query, bson.E{Key: "is_popular", Value: true})
	}
	if filter.Platform != "" {
		query = append(query, bson.E{Key: "platform", Value: filter.Platform})
	}
	if filter.Search != "" {
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(filter.Search), Options: "i"}
		query = append(query, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "name", Value: pattern}},
			bson.D{{Key: "slug", Value: pattern}},
		}})
	}
	collection := r.database.Collection(gameCollection)
	total, err := collection.CountDocuments(ctx, query)
	if err != nil {
		return Page{}, err
	}
	sort := bson.D{{Key: "sort_order", Value: 1}, {Key: "game_id", Value: 1}}
	if filter.Sort == "POPULAR" {
		sort = bson.D{{Key: "is_popular", Value: -1}, {Key: "sort_order", Value: 1}, {Key: "game_id", Value: 1}}
	} else if filter.Sort == "NAME_ASC" {
		sort = bson.D{{Key: "name", Value: 1}, {Key: "game_id", Value: 1}}
	}
	projection := bson.D{
		{Key: "game_id", Value: 1}, {Key: "name", Value: 1}, {Key: "slug", Value: 1},
		{Key: "description", Value: 1}, {Key: "image_url", Value: 1}, {Key: "platform", Value: 1},
		{Key: "sort_order", Value: 1},
	}
	if !filter.Popular {
		projection = append(projection, bson.E{Key: "is_popular", Value: 1})
	}
	findOptions := options.Find().SetSort(sort).SetSkip((filter.Page - 1) * filter.Limit).SetLimit(filter.Limit).SetProjection(projection)
	if filter.Sort == "NAME_ASC" {
		findOptions.SetCollation(&options.Collation{Locale: "id", Strength: 2})
	}
	cursor, err := collection.Find(ctx, query, findOptions)
	if err != nil {
		return Page{}, err
	}
	defer cursor.Close(ctx)
	items := make([]Game, 0)
	if err := cursor.All(ctx, &items); err != nil {
		return Page{}, err
	}
	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + filter.Limit - 1) / filter.Limit
	}
	return Page{Items: items, Page: filter.Page, Limit: filter.Limit, Total: total, TotalPages: totalPages}, nil
}

type rateWindow struct {
	Events []time.Time `bson:"events"`
}

func (r *MongoRepository) Allow(ctx context.Context, key string, now time.Time) (time.Duration, bool, error) {
	const limit = 1200
	window := time.Minute
	collection := r.database.Collection(rateCollection)
	_, err := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: key}}, bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "events", Value: bson.A{}}, {Key: "expires_at", Value: now.Add(window)}}}}, options.UpdateOne().SetUpsert(true))
	if err != nil && !duplicateKey(err) {
		return 0, false, err
	}
	start := now.Add(-window)
	filtered := bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$events", bson.A{}}}}},
		{Key: "as", Value: "event"}, {Key: "cond", Value: bson.D{{Key: "$gt", Value: bson.A{"$$event", start}}}},
	}}}
	filter := bson.D{{Key: "_id", Value: key}, {Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{bson.D{{Key: "$size", Value: filtered}}, limit}}}}}
	pipeline := mongo.Pipeline{{{Key: "$set", Value: bson.D{
		{Key: "events", Value: bson.D{{Key: "$concatArrays", Value: bson.A{filtered, bson.A{now}}}}},
		{Key: "expires_at", Value: now.Add(window)},
	}}}}
	var result rateWindow
	err = collection.FindOneAndUpdate(ctx, filter, pipeline, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return 0, true, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return 0, false, err
	}
	var full rateWindow
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

func duplicateKey(err error) bool {
	var write mongo.WriteException
	var command mongo.CommandError
	return errors.As(err, &write) && len(write.WriteErrors) > 0 && write.WriteErrors[0].Code == 11000 || errors.As(err, &command) && command.Code == 11000
}
