package adminaccount

import (
	"context"
	"regexp"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	accountCollection    = "admin_account"
	assignmentCollection = "account_role"
	roleCollection       = "admin_role"
)

type MongoRepository struct{ database *mongo.Database }

func NewMongoRepository(database *mongo.Database) *MongoRepository {
	return &MongoRepository{database: database}
}

func (r *MongoRepository) List(ctx context.Context, query Query) (Page, error) {
	filter := bson.D{{Key: "deleted_at", Value: nil}}
	if query.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: query.Status})
	}
	if query.Search != "" {
		pattern := ".*" + regexp.QuoteMeta(query.Search) + ".*"
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "username", Value: bson.Regex{Pattern: pattern, Options: "i"}}},
			bson.D{{Key: "name", Value: bson.Regex{Pattern: pattern, Options: "i"}}},
			bson.D{{Key: "email", Value: bson.Regex{Pattern: pattern, Options: "i"}}},
		}})
	}

	valid := bson.D{{Key: "active_assignment_count", Value: 1}, {Key: "active_role_count", Value: 1}}
	invalid := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "active_assignment_count", Value: bson.D{{Key: "$ne", Value: 1}}}},
		bson.D{{Key: "active_role_count", Value: bson.D{{Key: "$ne", Value: 1}}}},
	}}}
	matchingItems := mongo.Pipeline{{{Key: "$match", Value: valid}}}
	if query.RoleID != "" {
		matchingItems = append(matchingItems, bson.D{{Key: "$match", Value: bson.D{{Key: "role.role_id", Value: query.RoleID}}}})
	}
	matchingItems = append(matchingItems,
		bson.D{{Key: "$skip", Value: int64(query.Page-1) * int64(query.Limit)}},
		bson.D{{Key: "$limit", Value: int64(query.Limit)}},
		projectAccountStage(),
	)
	matchingCount := mongo.Pipeline{{{Key: "$match", Value: valid}}}
	if query.RoleID != "" {
		matchingCount = append(matchingCount, bson.D{{Key: "$match", Value: bson.D{{Key: "role.role_id", Value: query.RoleID}}}})
	}
	matchingCount = append(matchingCount, bson.D{{Key: "$count", Value: "total"}})
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$sort", Value: bson.D{{Key: "username", Value: 1}, {Key: "admin_account_id", Value: 1}}}},
	}
	pipeline = append(pipeline, roleJoinStages()...)
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.D{
		{Key: "invalid", Value: mongo.Pipeline{{{Key: "$match", Value: invalid}}, {{Key: "$limit", Value: 1}}}},
		{Key: "items", Value: matchingItems},
		{Key: "count", Value: matchingCount},
	}}})

	type counted struct {
		Total int64 `bson:"total"`
	}
	var result []struct {
		Invalid []bson.Raw `bson:"invalid"`
		Items   []Account  `bson:"items"`
		Count   []counted  `bson:"count"`
	}
	if err := r.aggregate(ctx, pipeline, &result); err != nil {
		return Page{}, err
	}
	if len(result) == 0 {
		return Page{Accounts: []Account{}, Pagination: Pagination{Page: query.Page, Limit: query.Limit}}, nil
	}
	if len(result[0].Invalid) > 0 {
		return Page{}, ErrRoleInvariant
	}
	page := Page{Accounts: []Account{}, Pagination: Pagination{Page: query.Page, Limit: query.Limit}}
	if result[0].Items != nil {
		page.Accounts = result[0].Items
	}
	if len(result[0].Count) > 0 {
		page.Pagination.Total = result[0].Count[0].Total
		page.Pagination.TotalPages = int((result[0].Count[0].Total + int64(query.Limit) - 1) / int64(query.Limit))
	}
	return page, nil
}

func (r *MongoRepository) Detail(ctx context.Context, id string) (Account, error) {
	pipeline := mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "admin_account_id", Value: id}, {Key: "deleted_at", Value: nil}}}}}
	pipeline = append(pipeline, roleJoinStages()...)
	pipeline = append(pipeline, projectAccountStage())
	var accounts []Account
	if err := r.aggregate(ctx, pipeline, &accounts); err != nil {
		return Account{}, err
	}
	if len(accounts) == 0 {
		return Account{}, ErrNotFound
	}
	if len(accounts) != 1 || accounts[0].ActiveAssignmentCount != 1 || accounts[0].ActiveRoleCount != 1 {
		return Account{}, ErrRoleInvariant
	}
	return accounts[0], nil
}

func (r *MongoRepository) aggregate(ctx context.Context, pipeline mongo.Pipeline, destination any) error {
	cursor, err := r.database.Collection(accountCollection).Aggregate(ctx, pipeline)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	if err := cursor.All(ctx, destination); err != nil {
		return err
	}
	return cursor.Err()
}

func roleJoinStages() mongo.Pipeline {
	assignmentExpr := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$admin_account_id", "$$accountID"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$revoked_at", nil}}},
	}}}
	assignmentPipeline := mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: assignmentExpr}}}}}
	assignmentLookup := bson.D{{Key: "$lookup", Value: bson.D{
		{Key: "from", Value: assignmentCollection},
		{Key: "let", Value: bson.D{{Key: "accountID", Value: "$admin_account_id"}}},
		{Key: "pipeline", Value: assignmentPipeline},
		{Key: "as", Value: "active_assignments"},
	}}}
	assignmentFields := bson.D{{Key: "$set", Value: bson.D{
		{Key: "active_assignment_count", Value: bson.D{{Key: "$size", Value: "$active_assignments"}}},
		{Key: "assigned_role_id", Value: bson.D{{Key: "$arrayElemAt", Value: bson.A{"$active_assignments.role_id", 0}}}},
	}}}

	roleExpr := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$role_id", "$$roleID"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$status", "ACTIVE"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$deleted_at", nil}}},
	}}}
	rolePipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: roleExpr}}}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "role_id", Value: 1}, {Key: "name", Value: 1}, {Key: "code", Value: 1}, {Key: "is_system", Value: 1}}}},
	}
	roleLookup := bson.D{{Key: "$lookup", Value: bson.D{
		{Key: "from", Value: roleCollection},
		{Key: "let", Value: bson.D{{Key: "roleID", Value: "$assigned_role_id"}}},
		{Key: "pipeline", Value: rolePipeline},
		{Key: "as", Value: "active_roles"},
	}}}
	roleFields := bson.D{{Key: "$set", Value: bson.D{
		{Key: "active_role_count", Value: bson.D{{Key: "$size", Value: "$active_roles"}}},
		{Key: "role", Value: bson.D{{Key: "$arrayElemAt", Value: bson.A{"$active_roles", 0}}}},
	}}}

	return mongo.Pipeline{assignmentLookup, assignmentFields, roleLookup, roleFields}
}

func projectAccountStage() bson.D {
	return bson.D{{Key: "$project", Value: bson.D{
		{Key: "admin_account_id", Value: 1}, {Key: "username", Value: 1}, {Key: "name", Value: 1},
		{Key: "email", Value: 1}, {Key: "status", Value: 1}, {Key: "last_active", Value: 1}, {Key: "updated_at", Value: 1},
		{Key: "role", Value: 1}, {Key: "active_assignment_count", Value: 1}, {Key: "active_role_count", Value: 1},
	}}}
}
