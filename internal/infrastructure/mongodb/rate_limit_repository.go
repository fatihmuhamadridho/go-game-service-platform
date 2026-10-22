package mongodb

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type rateLimitCounter struct {
	Key       string    `bson:"bucket_key"`
	Count     int32     `bson:"request_count"`
	ExpiresAt time.Time `bson:"expires_at"`
	CreatedAt time.Time `bson:"created_at"`
}

type RateLimitRepository struct{ db *mongo.Database }

func NewRateLimitRepository(db *mongo.Database) *RateLimitRepository {
	return &RateLimitRepository{db: db}
}

func (r *RateLimitRepository) Increment(ctx context.Context, key string, expires time.Time) (int64, error) {
	return RateLimitCounter(ctx, r.db, key, expires)
}

func RateLimitCounter(ctx context.Context, db *mongo.Database, key string, expires time.Time) (int64, error) {
	c := db.Collection("auth_rate_limit_bucket")
	_, err := c.UpdateOne(ctx, bson.D{{Key: "bucket_key", Value: key}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "request_count", Value: 1}}}, {Key: "$setOnInsert", Value: bson.D{{Key: "created_at", Value: time.Now().UTC()}, {Key: "expires_at", Value: expires}}}}, optionsUpsert())
	if mongo.IsDuplicateKeyError(err) {
		_, err = c.UpdateOne(ctx, bson.D{{Key: "bucket_key", Value: key}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "request_count", Value: 1}}}})
	}
	if err != nil {
		return 0, err
	}
	var bucket rateLimitCounter
	err = c.FindOne(ctx, bson.D{{Key: "bucket_key", Value: key}}).Decode(&bucket)
	return int64(bucket.Count), err
}

func optionsUpsert() *options.UpdateOneOptionsBuilder { return options.UpdateOne().SetUpsert(true) }
