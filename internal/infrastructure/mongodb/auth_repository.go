package mongodb

import (
	"context"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type AuthRepository struct {
	database *mongo.Database
}

func NewAuthRepository(database *mongo.Database) *AuthRepository {
	return &AuthRepository{database: database}
}

func accountCollection(database *mongo.Database, accountType model.AccountType) *mongo.Collection {
	if accountType == model.AdminAccountType {
		return database.Collection("admin_account")
	}
	return database.Collection("user_account")
}

func (r *AuthRepository) FindAccountByIdentifier(ctx context.Context, accountType model.AccountType, identifier string) (*model.Account, error) {
	var account model.Account
	collection := accountCollection(r.database, accountType)
	for _, field := range []string{"username", "email"} {
		filter := bson.D{{Key: field, Value: identifier}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}
		err := collection.FindOne(ctx, filter).Decode(&account)
		if err == mongo.ErrNoDocuments {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &account, nil
	}
	return nil, nil
}

func (r *AuthRepository) FindAccountByID(ctx context.Context, accountType model.AccountType, id bson.ObjectID) (*model.Account, error) {
	var account model.Account
	filter := bson.D{{Key: "_id", Value: id}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}
	if err := accountCollection(r.database, accountType).FindOne(ctx, filter).Decode(&account); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &account, nil
}

func (r *AuthRepository) CustomerRegistrationAllowed(ctx context.Context) (bool, error) {
	var settings model.PlatformSettings
	err := r.database.Collection("platform_settings").FindOne(ctx, bson.D{{Key: "singleton_key", Value: "current"}}).Decode(&settings)
	if err == mongo.ErrNoDocuments {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return settings.AllowRegistration, nil
}

func (r *AuthRepository) CreateCustomer(ctx context.Context, account model.Account) error {
	now := time.Now().UTC()
	account.ID = bson.NewObjectID()
	account.Status = "ACTIVE"
	if _, err := r.database.Collection("user_account").InsertOne(ctx, bson.D{
		{Key: "_id", Value: account.ID}, {Key: "email", Value: account.Email},
		{Key: "username", Value: account.Username}, {Key: "name", Value: account.Name},
		{Key: "password_hash", Value: account.PasswordHash}, {Key: "whatsapp", Value: account.WhatsApp},
		{Key: "status", Value: account.Status}, {Key: "joined_at", Value: now},
		{Key: "created_at", Value: now}, {Key: "updated_at", Value: now}, {Key: "deleted_at", Value: nil},
	}); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return model.ErrDuplicateAccount
		}
		return err
	}
	if _, err := r.database.Collection("member_profile").InsertOne(ctx, bson.D{
		{Key: "_id", Value: account.ID}, {Key: "tier", Value: "BRONZE"},
		{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
	}); err != nil {
		_, _ = r.database.Collection("user_account").DeleteOne(ctx, bson.D{{Key: "_id", Value: account.ID}})
		return err
	}
	return nil
}

func (r *AuthRepository) DeleteCustomer(ctx context.Context, id bson.ObjectID) error {
	_, err := r.database.Collection("user_account").DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	return err
}

func (r *AuthRepository) ResolveAdminAuthorization(ctx context.Context, adminID bson.ObjectID) (*model.AdminAuthorization, error) {
	assignments, err := findAll[bson.M](ctx, r.database.Collection("account_role"), bson.D{{Key: "admin_account_id", Value: adminID}, {Key: "revoked_at", Value: nil}})
	if err != nil {
		return nil, err
	}
	roleIDs := make([]bson.ObjectID, 0, len(assignments))
	for _, assignment := range assignments {
		if roleID, ok := assignment["role_id"].(bson.ObjectID); ok {
			roleIDs = append(roleIDs, roleID)
		}
	}
	if len(roleIDs) == 0 {
		return nil, nil
	}
	roles, err := findAll[bson.M](ctx, r.database.Collection("admin_role"), bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: roleIDs}}}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}})
	if err != nil {
		return nil, err
	}
	activeRoleIDs := make([]bson.ObjectID, 0, len(roles))
	isSuperAdmin := false
	for _, role := range roles {
		id, ok := role["_id"].(bson.ObjectID)
		if !ok {
			continue
		}
		activeRoleIDs = append(activeRoleIDs, id)
		isSystem, _ := role["is_system"].(bool)
		code, _ := role["code"].(string)
		if isSystem && code == "SUPER_ADMIN" {
			isSuperAdmin = true
		}
	}
	if len(activeRoleIDs) == 0 {
		return nil, nil
	}
	rolePermissions, err := findAll[bson.M](ctx, r.database.Collection("role_permission"), bson.D{{Key: "role_id", Value: bson.D{{Key: "$in", Value: activeRoleIDs}}}})
	if err != nil {
		return nil, err
	}
	permissionIDs := make(map[bson.ObjectID]struct{}, len(rolePermissions))
	for _, item := range rolePermissions {
		if id, ok := item["permission_id"].(bson.ObjectID); ok {
			permissionIDs[id] = struct{}{}
		}
	}
	permissionCodes := make([]string, 0, len(permissionIDs)+1)
	permissionRows, err := findAll[bson.M](ctx, r.database.Collection("permission"), bson.D{{Key: "is_active", Value: true}})
	if err != nil {
		return nil, err
	}
	for _, item := range permissionRows {
		id, idOK := item["_id"].(bson.ObjectID)
		code, codeOK := item["code"].(string)
		if _, assigned := permissionIDs[id]; idOK && codeOK && assigned {
			permissionCodes = append(permissionCodes, code)
		}
	}
	role := "ADMIN"
	if isSuperAdmin {
		role = "SUPER_ADMIN"
		permissionCodes = append(permissionCodes, "*")
	}
	return &model.AdminAuthorization{Role: role, Permissions: permissionCodes}, nil
}

func (r *AuthRepository) CreateRefreshSession(ctx context.Context, session model.RefreshSession) error {
	_, err := r.database.Collection("auth_refresh_session").InsertOne(ctx, session)
	return err
}

func (r *AuthRepository) FindRefreshSession(ctx context.Context, hash string, accountType model.AccountType) (*model.RefreshSession, error) {
	var session model.RefreshSession
	if err := r.database.Collection("auth_refresh_session").FindOne(ctx, bson.D{{Key: "token_hash", Value: hash}, {Key: "account_type", Value: accountType}}).Decode(&session); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (r *AuthRepository) ConsumeRefreshSession(ctx context.Context, id, nextID, familyID bson.ObjectID, now time.Time) (bool, error) {
	result, err := r.database.Collection("auth_refresh_session").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "family_id", Value: familyID}, {Key: "is_revoked", Value: false}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "is_revoked", Value: true}, {Key: "revoked_at", Value: now}, {Key: "last_used_at", Value: now}, {Key: "replaced_by_id", Value: nextID}}}},
	)
	if err != nil {
		return false, err
	}
	return result.MatchedCount == 1, nil
}

func (r *AuthRepository) RevokeRefreshSession(ctx context.Context, id bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("auth_refresh_session").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "is_revoked", Value: false}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "is_revoked", Value: true}, {Key: "revoked_at", Value: now}}}},
	)
	return err
}

func (r *AuthRepository) RevokeRefreshFamily(ctx context.Context, accountType model.AccountType, accountID, familyID bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("auth_refresh_session").UpdateMany(ctx,
		bson.D{{Key: "account_type", Value: accountType}, {Key: "account_id", Value: accountID}, {Key: "family_id", Value: familyID}, {Key: "is_revoked", Value: false}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "is_revoked", Value: true}, {Key: "revoked_at", Value: now}}}},
	)
	return err
}

func (r *AuthRepository) RevokeAccountRefreshSessions(ctx context.Context, accountType model.AccountType, accountID bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("auth_refresh_session").UpdateMany(ctx,
		bson.D{{Key: "account_type", Value: accountType}, {Key: "account_id", Value: accountID}, {Key: "is_revoked", Value: false}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "is_revoked", Value: true}, {Key: "revoked_at", Value: now}}}},
	)
	return err
}

func (r *AuthRepository) RevokeAccessToken(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := r.database.Collection("auth_revoked_access_token").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}},
		bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "expires_at", Value: expiresAt}, {Key: "revoked_at", Value: time.Now().UTC()}}}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (r *AuthRepository) IsAccessTokenRevoked(ctx context.Context, id string) (bool, error) {
	count, err := r.database.Collection("auth_revoked_access_token").CountDocuments(ctx, bson.D{{Key: "_id", Value: id}})
	return count > 0, err
}

func (r *AuthRepository) FindCustomerByEmail(ctx context.Context, email string) (*model.Account, error) {
	var account model.Account
	err := r.database.Collection("user_account").FindOne(ctx, bson.D{{Key: "email", Value: email}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}).Decode(&account)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *AuthRepository) CreatePasswordResetToken(ctx context.Context, token model.PasswordResetToken) error {
	_, err := r.database.Collection("auth_action_token").InsertOne(ctx, token)
	return err
}

func (r *AuthRepository) FindPasswordResetToken(ctx context.Context, hash string) (*model.PasswordResetToken, error) {
	var token model.PasswordResetToken
	err := r.database.Collection("auth_action_token").FindOne(ctx, bson.D{{Key: "token_hash", Value: hash}, {Key: "token_type", Value: "PASSWORD_RESET"}}).Decode(&token)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *AuthRepository) ConsumePasswordResetToken(ctx context.Context, id bson.ObjectID, now time.Time) (bool, error) {
	result, err := r.database.Collection("auth_action_token").UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "token_type", Value: "PASSWORD_RESET"}, {Key: "used_at", Value: nil}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "used_at", Value: now}}}},
	)
	if err != nil {
		return false, err
	}
	return result.ModifiedCount == 1, nil
}

func (r *AuthRepository) SetCustomerPassword(ctx context.Context, id bson.ObjectID, passwordHash string, now time.Time) error {
	_, err := r.database.Collection("user_account").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "password_hash", Value: passwordHash}, {Key: "updated_at", Value: now}}}})
	return err
}

func (r *AuthRepository) RevokeCustomerRefreshSessions(ctx context.Context, id bson.ObjectID, now time.Time) error {
	_, err := r.database.Collection("auth_refresh_session").UpdateMany(ctx, bson.D{{Key: "account_type", Value: model.CustomerAccount}, {Key: "account_id", Value: id}, {Key: "is_revoked", Value: false}}, bson.D{{Key: "$set", Value: bson.D{{Key: "is_revoked", Value: true}, {Key: "revoked_at", Value: now}}}})
	return err
}

func (r *AuthRepository) InvalidatePasswordResetTokens(ctx context.Context, id bson.ObjectID, now time.Time) error {
	c := r.database.Collection("auth_action_token")
	_, err := c.DeleteMany(ctx, bson.D{{Key: "account_id", Value: id}, {Key: "token_type", Value: "PASSWORD_RESET"}, {Key: "expires_at", Value: bson.D{{Key: "$lte", Value: now}}}})
	if err != nil {
		return err
	}
	_, err = c.UpdateMany(ctx, bson.D{{Key: "account_id", Value: id}, {Key: "token_type", Value: "PASSWORD_RESET"}, {Key: "used_at", Value: nil}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}}}, bson.D{{Key: "$set", Value: bson.D{{Key: "used_at", Value: now}}}})
	return err
}

func (r *AuthRepository) InvalidatePasswordResetToken(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := r.database.Collection("auth_action_token").UpdateOne(ctx,
		bson.D{{Key: "token_hash", Value: tokenHash}, {Key: "token_type", Value: "PASSWORD_RESET"}, {Key: "used_at", Value: nil}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "used_at", Value: now}}}},
	)
	return err
}

var _ model.AuthRepository = (*AuthRepository)(nil)
