package operation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	operationRecordCollection   = "operation_records"
	operationSettingsCollection = "application_settings"
	orderCollection             = "orders"
)

// MongoStore persists operational API records independently of the handler
// process. It intentionally does not seed payment or bank account details.
type MongoStore struct{ db *mongo.Database }

func NewMongoStore(db *mongo.Database) *MongoStore { return &MongoStore{db: db} }

func (s *MongoStore) EnsureIndexes(ctx context.Context) error {
	_, err := s.db.Collection(operationRecordCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "bucket", Value: 1}, {Key: "id", Value: 1}},
		Options: options.Index().SetName("uq_operation_bucket_record_id").SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create operation record index: %w", err)
	}
	_, err = s.db.Collection(operationRecordCollection).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "bucket", Value: 1}, {Key: "data.isPrimary", Value: 1}},
		Options: options.Index().SetName("uq_primary_bank_account").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "bucket", Value: "bankAccounts"}, {Key: "data.isPrimary", Value: true}}),
	})
	if err != nil {
		return fmt.Errorf("create primary bank account index: %w", err)
	}
	_, err = s.db.Collection(orderCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "order.orderNumber", Value: 1}}, Options: options.Index().SetName("uq_order_number").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "order.orderNumber", Value: bson.D{{Key: "$type", Value: "string"}}}})},
		{Keys: bson.D{{Key: "idempotencyKey", Value: 1}}, Options: options.Index().SetName("uq_order_idempotency_key").SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "order.id", Value: 1}}, Options: options.Index().SetName("ix_order_id")},
	})
	if err != nil {
		return fmt.Errorf("create operation order indexes: %w", err)
	}
	return nil
}

func (s *MongoStore) GetSettings(ctx context.Context) (map[string]any, error) {
	var row struct {
		Data map[string]any `bson:"data"`
	}
	err := s.db.Collection(operationSettingsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: "general"}}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return map[string]any{}, nil
	}
	return row.Data, err
}

func (s *MongoStore) SaveSettings(ctx context.Context, settings map[string]any) error {
	_, err := s.db.Collection(operationSettingsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: "general"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: settings}, {Key: "updatedAt", Value: time.Now().UTC()}}}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (s *MongoStore) SeedDefaults(ctx context.Context, settings map[string]any, about map[string]any) error {
	_, err := s.db.Collection(operationSettingsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: "general"}},
		bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "data", Value: settings}, {Key: "updatedAt", Value: time.Now().UTC()}}}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("seed settings defaults: %w", err)
	}
	if about != nil {
		_, err = s.db.Collection(operationRecordCollection).UpdateOne(ctx,
			bson.D{{Key: "bucket", Value: "pages"}, {Key: "id", Value: about["id"]}},
			bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "bucket", Value: "pages"}, {Key: "id", Value: about["id"]}, {Key: "data", Value: about}}}},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			return fmt.Errorf("seed about page: %w", err)
		}
	}
	if err := s.MigrateLegacyBrand(ctx); err != nil {
		return fmt.Errorf("migrate legacy site brand: %w", err)
	}
	return nil
}

// MigrateLegacyBrand updates only the previous default siteName and the known
// seeded About record. Custom site names and all other admin-authored pages are
// left untouched.
func (s *MongoStore) MigrateLegacyBrand(ctx context.Context) error {
	_, err := s.db.Collection(operationSettingsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: "general"}, {Key: "data.siteName", Value: bson.D{{Key: "$in", Value: bson.A{"JokiHub", "jokihub", "JOKIHUB"}}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "data.siteName", Value: "Bongastore"}, {Key: "updatedAt", Value: time.Now().UTC()}}}},
	)
	if err != nil {
		return fmt.Errorf("migrate default site name: %w", err)
	}

	filter := bson.D{{Key: "bucket", Value: "pages"}, {Key: "id", Value: "page-about"}}
	var record struct {
		Data map[string]any `bson:"data"`
	}
	err = s.db.Collection(operationRecordCollection).FindOne(ctx, filter).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read seeded About page: %w", err)
	}
	updates := aboutBrandMigrationUpdates(record.Data)
	if len(updates) == 0 {
		return nil
	}
	updates = append(updates, bson.E{Key: "data.updatedAt", Value: time.Now().UTC().Format(time.RFC3339)})
	if _, err := s.db.Collection(operationRecordCollection).UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: updates}}); err != nil {
		return fmt.Errorf("update seeded About page: %w", err)
	}
	return nil
}

func aboutBrandMigrationUpdates(data map[string]any) bson.D {
	updates := bson.D{}
	replacer := strings.NewReplacer("JokiHub", "Bongastore", "jokihub", "bongastore", "JOKIHUB", "BONGASTORE")
	for _, key := range []string{"title", "summary", "content"} {
		value, ok := data[key].(string)
		if !ok {
			continue
		}
		updated := replacer.Replace(value)
		if updated != value {
			updates = append(updates, bson.E{Key: "data." + key, Value: updated})
		}
	}
	return updates
}

func (s *MongoStore) List(ctx context.Context, bucket string) ([]map[string]any, error) {
	cursor, err := s.db.Collection(operationRecordCollection).Find(ctx, bson.D{{Key: "bucket", Value: bucket}}, options.Find().SetSort(bson.D{{Key: "data.updatedAt", Value: -1}, {Key: "id", Value: 1}}).SetLimit(10000))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var records []struct {
		Data map[string]any `bson:"data"`
	}
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if record.Data != nil {
			items = append(items, record.Data)
		}
	}
	return items, nil
}

func (s *MongoStore) Get(ctx context.Context, bucket, id string) (map[string]any, error) {
	var record struct {
		Data map[string]any `bson:"data"`
	}
	err := s.db.Collection(operationRecordCollection).FindOne(ctx, bson.D{{Key: "bucket", Value: bucket}, {Key: "id", Value: id}}).Decode(&record)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return record.Data, err
}

func (s *MongoStore) Put(ctx context.Context, bucket, id string, data map[string]any) error {
	_, err := s.db.Collection(operationRecordCollection).UpdateOne(ctx,
		bson.D{{Key: "bucket", Value: bucket}, {Key: "id", Value: id}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "bucket", Value: bucket}, {Key: "id", Value: id}, {Key: "data", Value: data}}}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (s *MongoStore) Delete(ctx context.Context, bucket, id string) error {
	_, err := s.db.Collection(operationRecordCollection).DeleteOne(ctx, bson.D{{Key: "bucket", Value: bucket}, {Key: "id", Value: id}})
	return err
}

func (s *MongoStore) ClearPrimaryBankAccounts(ctx context.Context) error {
	_, err := s.db.Collection(operationRecordCollection).UpdateMany(ctx,
		bson.D{{Key: "bucket", Value: "bankAccounts"}, {Key: "data.isPrimary", Value: true}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "data.isPrimary", Value: false}}}},
	)
	return err
}

func (s *MongoStore) CreateOrder(ctx context.Context, entry map[string]any, key, hash string) (map[string]any, bool, error) {
	order, _ := entry["order"].(map[string]any)
	doc := bson.M{"order": order, "payment": entry["payment"], "trackingToken": entry["trackingToken"], "createdAt": time.Now().UTC()}
	if key != "" {
		doc["idempotencyKey"] = key
		doc["requestHash"] = hash
	}
	_, err := s.db.Collection(orderCollection).InsertOne(ctx, doc)
	if err == nil {
		return nil, false, nil
	}
	if !isMongoDuplicate(err) || key == "" {
		return nil, false, err
	}
	var existing bson.M
	if findErr := s.db.Collection(orderCollection).FindOne(ctx, bson.D{{Key: "idempotencyKey", Value: key}}).Decode(&existing); findErr != nil {
		return nil, false, err
	}
	if existing["requestHash"] != hash {
		return nil, false, ErrIdempotencyConflict
	}
	return normalizeOrderDoc(existing), true, nil
}

func (s *MongoStore) FindOrder(ctx context.Context, filter bson.D) (map[string]any, error) {
	var doc bson.M
	err := s.db.Collection(orderCollection).FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return normalizeOrderDoc(doc), err
}

func (s *MongoStore) ListOrders(ctx context.Context) ([]map[string]any, error) {
	cursor, err := s.db.Collection(orderCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetLimit(10000))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		entries = append(entries, normalizeOrderDoc(doc))
	}
	return entries, nil
}

func (s *MongoStore) SaveOrder(ctx context.Context, entry map[string]any) error {
	order, _ := entry["order"].(map[string]any)
	if order == nil {
		return errors.New("order record missing")
	}
	_, err := s.db.Collection(orderCollection).UpdateOne(ctx,
		bson.D{{Key: "order.id", Value: order["id"]}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "order", Value: order}, {Key: "payment", Value: entry["payment"]}, {Key: "trackingToken", Value: entry["trackingToken"]}, {Key: "updatedAt", Value: time.Now().UTC()}}}},
	)
	return err
}

func (s *MongoStore) TransitionOrder(ctx context.Context, entry map[string]any, expectedPayment, expectedOrder string) (bool, error) {
	order, _ := entry["order"].(map[string]any)
	if order == nil {
		return false, errors.New("order record missing")
	}
	filter := bson.D{{Key: "order.id", Value: order["id"]}}
	if expectedPayment != "" {
		filter = append(filter, bson.E{Key: "order.paymentStatus", Value: expectedPayment})
	}
	if expectedOrder != "" {
		filter = append(filter, bson.E{Key: "order.orderStatus", Value: expectedOrder})
	}
	result, err := s.db.Collection(orderCollection).UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "order", Value: order}, {Key: "payment", Value: entry["payment"]}, {Key: "updatedAt", Value: time.Now().UTC()}}}})
	return result != nil && result.MatchedCount == 1, err
}

var ErrIdempotencyConflict = errors.New("idempotency key payload conflict")

func normalizeOrderDoc(doc bson.M) map[string]any {
	return map[string]any{"order": normalizeMap(doc["order"]), "payment": normalizeMap(doc["payment"]), "trackingToken": doc["trackingToken"]}
}

func normalizeMap(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	case bson.M:
		return map[string]any(v)
	default:
		return map[string]any{}
	}
}

func isMongoDuplicate(err error) bool {
	var write mongo.WriteException
	var command mongo.CommandError
	return errors.As(err, &write) && len(write.WriteErrors) > 0 && write.WriteErrors[0].Code == 11000 || errors.As(err, &command) && command.Code == 11000
}
