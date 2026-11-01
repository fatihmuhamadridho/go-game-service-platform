package catalog

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
	categoryCollection = "service_categories"
	serviceCollection  = "game_services"
)

type Store interface {
	ListCategories(context.Context, bool) ([]Category, error)
	CategoryByID(context.Context, string) (Category, error)
	CategoryBySlug(context.Context, string) (Category, error)
	CreateCategory(context.Context, Category) error
	UpdateCategory(context.Context, string, Category) (bool, error)
	DeleteCategory(context.Context, string) (bool, error)
	CountServicesForCategory(context.Context, string) (int64, error)
	ListServices(context.Context, string, bool) ([]Service, error)
	ListServicesForCategory(context.Context, string, bool) ([]Service, error)
	FindActiveServiceForCheckout(context.Context, string, string) (Service, error)
	CreateService(context.Context, Service) error
	UpdateService(context.Context, string, Service) (bool, error)
	DeleteService(context.Context, string) (bool, error)
}

type MongoRepository struct{ database *mongo.Database }

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{database: database}
}

func (r *MongoRepository) EnsureIndexes(ctx context.Context) error {
	_, err := r.database.Collection(categoryCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "category_id", Value: 1}}, Options: options.Index().SetName("uq_service_category_id").SetUnique(true)},
		{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetName("uq_service_category_slug").SetUnique(true)},
		{Keys: bson.D{{Key: "is_active", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("ix_service_category_public")},
	})
	if err != nil {
		return fmt.Errorf("create service category indexes: %w", err)
	}
	_, err = r.database.Collection(serviceCollection).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "service_id", Value: 1}}, Options: options.Index().SetName("uq_game_service_id").SetUnique(true)},
		{Keys: bson.D{{Key: "category_id", Value: 1}, {Key: "is_active", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("ix_game_service_category")},
		{Keys: bson.D{{Key: "game_id", Value: 1}, {Key: "is_active", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetName("ix_game_service_game")},
	})
	if err != nil {
		return fmt.Errorf("create game service indexes: %w", err)
	}
	return nil
}

func (r *MongoRepository) ListCategories(ctx context.Context, activeOnly bool) ([]Category, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}}
	if activeOnly {
		filter = append(filter, bson.E{Key: "is_active", Value: true})
	}
	cursor, err := r.database.Collection(categoryCollection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "category_id", Value: 1}}).SetLimit(1000).SetCollation(&options.Collation{Locale: "id", Strength: 2}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := make([]Category, 0)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MongoRepository) CategoryByID(ctx context.Context, id string) (Category, error) {
	var category Category
	err := r.database.Collection(categoryCollection).FindOne(ctx, bson.D{{Key: "category_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&category)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Category{}, ErrCategoryNotFound
	}
	return category, err
}

func (r *MongoRepository) CategoryBySlug(ctx context.Context, slug string) (Category, error) {
	var category Category
	err := r.database.Collection(categoryCollection).FindOne(ctx, bson.D{{Key: "slug", Value: strings.ToLower(slug)}, {Key: "deleted_at", Value: nil}}).Decode(&category)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Category{}, ErrCategoryNotFound
	}
	return category, err
}

func (r *MongoRepository) CreateCategory(ctx context.Context, category Category) error {
	if category.ID == "" {
		id, err := NewID("cat")
		if err != nil {
			return err
		}
		category.ID = id
	}
	_, err := r.database.Collection(categoryCollection).InsertOne(ctx, category)
	if isDuplicateKey(err) {
		return ErrCategoryConflict
	}
	return err
}

func (r *MongoRepository) UpdateCategory(ctx context.Context, id string, category Category) (bool, error) {
	result, err := r.database.Collection(categoryCollection).UpdateOne(ctx, bson.D{{Key: "category_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: category.Name}, {Key: "slug", Value: category.Slug}, {Key: "description", Value: category.Description}, {Key: "is_active", Value: category.IsActive}, {Key: "updated_at", Value: category.UpdatedAt}}}})
	if isDuplicateKey(err) {
		return false, ErrCategoryConflict
	}
	if err != nil {
		return false, err
	}
	return result.MatchedCount == 1, nil
}

func (r *MongoRepository) DeleteCategory(ctx context.Context, id string) (bool, error) {
	count, err := r.CountServicesForCategory(ctx, id)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, ErrCategoryInUse
	}
	now := time.Now().UTC()
	result, err := r.database.Collection(categoryCollection).UpdateOne(ctx,
		bson.D{{Key: "category_id", Value: id}, {Key: "deleted_at", Value: nil}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "is_active", Value: false}, {Key: "deleted_at", Value: now}, {Key: "updated_at", Value: now}}}},
	)
	return result.MatchedCount == 1, err
}

func (r *MongoRepository) CountServicesForCategory(ctx context.Context, id string) (int64, error) {
	return r.database.Collection(serviceCollection).CountDocuments(ctx, bson.D{{Key: "category_id", Value: id}})
}

func (r *MongoRepository) ListServices(ctx context.Context, gameID string, activeOnly bool) ([]Service, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}}
	if gameID != "" {
		filter = append(filter, bson.E{Key: "game_id", Value: gameID})
	}
	if activeOnly {
		filter = append(filter, bson.E{Key: "is_active", Value: true})
	}
	cursor, err := r.database.Collection(serviceCollection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "service_id", Value: 1}}).SetLimit(5000).SetCollation(&options.Collation{Locale: "id", Strength: 2}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := make([]Service, 0)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MongoRepository) ListServicesForCategory(ctx context.Context, categoryID string, activeOnly bool) ([]Service, error) {
	filter := bson.D{{Key: "category_id", Value: categoryID}, {Key: "deleted_at", Value: nil}}
	if activeOnly {
		filter = append(filter, bson.E{Key: "is_active", Value: true})
	}
	cursor, err := r.database.Collection(serviceCollection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}, {Key: "service_id", Value: 1}}).SetLimit(5000))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := make([]Service, 0)
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *MongoRepository) FindActiveServiceForCheckout(ctx context.Context, gameID, serviceID string) (Service, error) {
	filter := bson.D{{Key: "service_id", Value: serviceID}, {Key: "is_active", Value: true}}
	if gameID != "" {
		filter = append(filter, bson.E{Key: "game_id", Value: gameID})
	}
	var service Service
	err := r.database.Collection(serviceCollection).FindOne(ctx, filter).Decode(&service)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Service{}, ErrServiceNotFound
	}
	if err != nil {
		return Service{}, err
	}
	category, err := r.CategoryByID(ctx, service.CategoryID)
	if errors.Is(err, ErrCategoryNotFound) || err == nil && !category.IsActive {
		return Service{}, ErrServiceNotFound
	}
	if err != nil {
		return Service{}, err
	}
	return service, nil
}

func (r *MongoRepository) CreateService(ctx context.Context, service Service) error {
	category, err := r.CategoryByID(ctx, service.CategoryID)
	if err != nil {
		return err
	}
	if !category.IsActive {
		return ErrInvalidCatalog
	}
	if service.ID == "" {
		id, err := NewID("svc")
		if err != nil {
			return err
		}
		service.ID = id
	}
	_, err = r.database.Collection(serviceCollection).InsertOne(ctx, service)
	if isDuplicateKey(err) {
		return ErrServiceConflict
	}
	if err != nil {
		return err
	}
	category, err = r.CategoryByID(ctx, service.CategoryID)
	if err != nil || !category.IsActive {
		_, _ = r.database.Collection(serviceCollection).DeleteOne(ctx, bson.D{{Key: "service_id", Value: service.ID}})
		if err != nil {
			return err
		}
		return ErrInvalidCatalog
	}
	return nil
}

func (r *MongoRepository) UpdateService(ctx context.Context, id string, service Service) (bool, error) {
	var previous Service
	if err := r.database.Collection(serviceCollection).FindOne(ctx, bson.D{{Key: "service_id", Value: id}, {Key: "deleted_at", Value: nil}}).Decode(&previous); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}
	category, err := r.CategoryByID(ctx, service.CategoryID)
	if err != nil {
		return false, err
	}
	if !category.IsActive {
		return false, ErrInvalidCatalog
	}
	result, err := r.database.Collection(serviceCollection).UpdateOne(ctx, bson.D{{Key: "service_id", Value: id}}, bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: service.Name}, {Key: "game_id", Value: service.GameID}, {Key: "category_id", Value: service.CategoryID}, {Key: "price_amount", Value: service.PriceAmount}, {Key: "estimate_text", Value: service.Estimate}, {Key: "is_active", Value: service.IsActive}, {Key: "updated_at", Value: service.UpdatedAt}}}})
	if isDuplicateKey(err) {
		return false, ErrServiceConflict
	}
	if err != nil {
		return false, err
	}
	if result.MatchedCount == 0 {
		return false, nil
	}
	category, err = r.CategoryByID(ctx, service.CategoryID)
	if err != nil || !category.IsActive {
		_, restoreErr := r.database.Collection(serviceCollection).UpdateOne(ctx, bson.D{{Key: "service_id", Value: id}}, bson.D{{Key: "$set", Value: bson.D{{Key: "name", Value: previous.Name}, {Key: "game_id", Value: previous.GameID}, {Key: "category_id", Value: previous.CategoryID}, {Key: "price_amount", Value: previous.PriceAmount}, {Key: "estimate_text", Value: previous.Estimate}, {Key: "is_active", Value: previous.IsActive}, {Key: "updated_at", Value: previous.UpdatedAt}}}})
		if restoreErr != nil {
			return false, restoreErr
		}
		if err != nil {
			return false, err
		}
		return false, ErrInvalidCatalog
	}
	return true, nil
}

func (r *MongoRepository) DeleteService(ctx context.Context, id string) (bool, error) {
	result, err := r.database.Collection(serviceCollection).DeleteOne(ctx, bson.D{{Key: "service_id", Value: id}})
	return result.DeletedCount == 1, err
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var write mongo.WriteException
	var command mongo.CommandError
	return errors.As(err, &write) && len(write.WriteErrors) > 0 && write.WriteErrors[0].Code == 11000 || errors.As(err, &command) && command.Code == 11000
}
