package mongodb

import (
	"context"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type AdminMemberRepository struct{ database *mongo.Database }

func NewAdminMemberRepository(database *mongo.Database) *AdminMemberRepository {
	return &AdminMemberRepository{database: database}
}

func (r *AdminMemberRepository) ListUsers(ctx context.Context) ([]model.Account, error) {
	return findAll[model.Account](ctx, r.database.Collection("user_account"), bson.D{{Key: "deleted_at", Value: nil}})
}

func (r *AdminMemberRepository) ListMemberProfiles(ctx context.Context) ([]model.MemberProfile, error) {
	return findAll[model.MemberProfile](ctx, r.database.Collection("member_profile"), bson.D{})
}

func (r *AdminMemberRepository) ListAllMemberOrders(ctx context.Context) ([]model.Order, error) {
	return findAll[model.Order](ctx, r.database.Collection("customer_order"), bson.D{})
}

func (r *AdminMemberRepository) FindUserByID(ctx context.Context, id bson.ObjectID) (*model.Account, error) {
	var user model.Account
	err := r.database.Collection("user_account").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *AdminMemberRepository) FindUserByUsernameOrEmail(ctx context.Context, username, email string) (*model.Account, error) {
	conditions := bson.A{}
	if username != "" {
		conditions = append(conditions, bson.D{{Key: "username", Value: username}})
	}
	if email != "" {
		conditions = append(conditions, bson.D{{Key: "email", Value: email}})
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	var user model.Account
	filter := bson.D{{Key: "$or", Value: conditions}}
	err := r.database.Collection("user_account").FindOne(ctx, filter).Decode(&user)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *AdminMemberRepository) FindMemberProfile(ctx context.Context, id bson.ObjectID) (*model.MemberProfile, error) {
	var profile model.MemberProfile
	err := r.database.Collection("member_profile").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&profile)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *AdminMemberRepository) ListOrdersByUser(ctx context.Context, id bson.ObjectID) ([]model.Order, error) {
	return findAll[model.Order](ctx, r.database.Collection("customer_order"), bson.D{{Key: "user_id", Value: id}})
}

func (r *AdminMemberRepository) UpdateUser(ctx context.Context, id bson.ObjectID, patch map[string]any) error {
	patch["updated_at"] = time.Now().UTC()
	_, err := r.database.Collection("user_account").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: patch}})
	return err
}

func (r *AdminMemberRepository) CreateUserStatusHistory(ctx context.Context, row model.UserStatusHistory) error {
	if row.ID.IsZero() {
		row.ID = bson.NewObjectID()
	}
	_, err := r.database.Collection("user_status_history").InsertOne(ctx, row)
	return err
}

var _ model.AdminMemberRepository = (*AdminMemberRepository)(nil)
