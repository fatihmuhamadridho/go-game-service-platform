package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	adminAccounts  = "admin_account"
	credentials    = "admin_credential"
	adminSessions  = "admin_session"
	adminRoles     = "admin_role"
	accountRoles   = "account_role"
	permissions    = "permission"
	rolePermission = "role_permission"
	rateWindows    = "rate_limit_windows"
)

type MongoAdminAuthRepository struct {
	database *mongo.Database
}

func NewMongoAdminAuthRepository(database *mongo.Database) *MongoAdminAuthRepository {
	return &MongoAdminAuthRepository{database: database}
}

type accountDocument struct {
	ID        string     `bson:"admin_account_id"`
	Username  string     `bson:"username"`
	Name      string     `bson:"name"`
	Email     string     `bson:"email"`
	Status    string     `bson:"status"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

type credentialDocument struct {
	AccountID string `bson:"admin_account_id"`
	Hash      string `bson:"password_hash"`
}

type roleDocument struct {
	ID        string     `bson:"role_id"`
	Name      string     `bson:"name"`
	Code      string     `bson:"code"`
	Status    string     `bson:"status"`
	System    bool       `bson:"is_system"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

type assignmentDocument struct {
	AccountID string     `bson:"admin_account_id"`
	RoleID    string     `bson:"role_id"`
	RevokedAt *time.Time `bson:"revoked_at"`
}

type permissionLink struct {
	PermissionID string `bson:"permission_id"`
}

type permissionDocument struct {
	Key    string `bson:"permission_key"`
	Active bool   `bson:"is_active"`
}

type sessionDocument struct {
	ID                 string     `bson:"session_id"`
	FamilyID           string     `bson:"family_id"`
	AdminAccountID     string     `bson:"admin_account_id"`
	RefreshTokenDigest string     `bson:"refresh_token_digest"`
	CSRFTokenDigest    string     `bson:"csrf_token_digest"`
	CreatedAt          time.Time  `bson:"created_at"`
	ExpiresAt          time.Time  `bson:"expires_at"`
	RotatedAt          *time.Time `bson:"rotated_at"`
	RevokedAt          *time.Time `bson:"revoked_at"`
	RevocationReason   string     `bson:"revocation_reason,omitempty"`
}

func (r *MongoAdminAuthRepository) EnsureIndexes(ctx context.Context) error {
	for _, index := range authIndexModels() {
		if _, err := r.database.Collection(index.collection).Indexes().CreateMany(ctx, index.models); err != nil {
			return fmt.Errorf("create %s indexes: %w", index.collection, err)
		}
	}
	return nil
}

type collectionIndexModels struct {
	collection string
	models     []mongo.IndexModel
}

func authIndexModels() []collectionIndexModels {
	return []collectionIndexModels{
		{adminAccounts, []mongo.IndexModel{
			{Keys: bson.D{{Key: "admin_account_id", Value: 1}}, Options: options.Index().SetName("uq_admin_account_id").SetUnique(true)},
			{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetName("uq_admin_account_username_ci").SetUnique(true).SetCollation(&options.Collation{Locale: "en", Strength: 2})},
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetName("uq_admin_account_email_ci").SetUnique(true).SetCollation(&options.Collation{Locale: "en", Strength: 2})},
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "username", Value: 1}, {Key: "admin_account_id", Value: 1}}, Options: options.Index().SetName("ix_admin_account_list").SetPartialFilterExpression(bson.D{{Key: "deleted_at", Value: nil}})},
		}},
		{credentials, []mongo.IndexModel{
			{Keys: bson.D{{Key: "credential_id", Value: 1}}, Options: options.Index().SetName("uq_admin_credential_id").SetUnique(true)},
			{Keys: bson.D{{Key: "admin_account_id", Value: 1}}, Options: options.Index().SetName("uq_admin_credential_account").SetUnique(true)},
		}},
		{adminSessions, []mongo.IndexModel{
			{Keys: bson.D{{Key: "session_id", Value: 1}}, Options: options.Index().SetName("uq_admin_session_id").SetUnique(true)},
			{Keys: bson.D{{Key: "refresh_token_digest", Value: 1}}, Options: options.Index().SetName("uq_admin_session_refresh_digest").SetUnique(true)},
			{Keys: bson.D{{Key: "family_id", Value: 1}, {Key: "revoked_at", Value: 1}, {Key: "rotated_at", Value: 1}}, Options: options.Index().SetName("ix_admin_session_family_active")},
			{Keys: bson.D{{Key: "admin_account_id", Value: 1}, {Key: "revoked_at", Value: 1}, {Key: "expires_at", Value: 1}}, Options: options.Index().SetName("ix_admin_session_account_active")},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetName("ttl_admin_session_expiry").SetExpireAfterSeconds(0)},
		}},
		{accountRoles, []mongo.IndexModel{
			{Keys: bson.D{{Key: "account_role_id", Value: 1}}, Options: options.Index().SetName("uq_account_role_id").SetUnique(true)},
			{Keys: bson.D{{Key: "admin_account_id", Value: 1}}, Options: options.Index().SetName("uq_account_role_admin_active").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "revoked_at", Value: nil}})},
			{Keys: bson.D{{Key: "role_id", Value: 1}, {Key: "admin_account_id", Value: 1}}, Options: options.Index().SetName("ix_account_role_role_active").SetPartialFilterExpression(bson.D{{Key: "revoked_at", Value: nil}})},
		}},
		{adminRoles, []mongo.IndexModel{
			{Keys: bson.D{{Key: "role_id", Value: 1}}, Options: options.Index().SetName("uq_admin_role_id").SetUnique(true)},
			{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetName("uq_admin_role_name_active").SetUnique(true).SetCollation(&options.Collation{Locale: "en", Strength: 2}).SetPartialFilterExpression(bson.D{{Key: "deleted_at", Value: nil}})},
			{Keys: bson.D{{Key: "code", Value: 1}}, Options: options.Index().SetName("uq_admin_role_code_active").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "deleted_at", Value: nil}})},
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "name", Value: 1}, {Key: "role_id", Value: 1}}, Options: options.Index().SetName("ix_admin_role_list").SetPartialFilterExpression(bson.D{{Key: "deleted_at", Value: nil}})},
		}},
		{rolePermission, []mongo.IndexModel{{Keys: bson.D{{Key: "role_id", Value: 1}, {Key: "permission_id", Value: 1}}, Options: options.Index().SetName("uq_role_permission_pair").SetUnique(true)}}},
		{permissions, []mongo.IndexModel{
			{Keys: bson.D{{Key: "permission_id", Value: 1}}, Options: options.Index().SetName("uq_permission_id").SetUnique(true)},
			{Keys: bson.D{{Key: "permission_key", Value: 1}}, Options: options.Index().SetName("uq_permission_key").SetUnique(true)},
			{Keys: bson.D{{Key: "is_active", Value: 1}, {Key: "permission_key", Value: 1}}, Options: options.Index().SetName("ix_permission_active_key")},
		}},
		{rateWindows, []mongo.IndexModel{{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetName("ttl_rate_limit_expiry").SetExpireAfterSeconds(0)}}},
	}
}

func (r *MongoAdminAuthRepository) FindLoginIdentity(ctx context.Context, field, identifier string) (LoginIdentity, error) {
	if err := ValidateIdentifierKind(field, identifier); err != nil {
		return LoginIdentity{}, err
	}
	filter := bson.D{{Key: field, Value: identifier}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}
	var account accountDocument
	err := r.database.Collection(adminAccounts).FindOne(ctx, filter, options.FindOne().SetCollation(&options.Collation{Locale: "en", Strength: 2})).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return LoginIdentity{}, ErrNotFound
	}
	if err != nil {
		return LoginIdentity{}, err
	}
	var credential credentialDocument
	err = r.database.Collection(credentials).FindOne(ctx, bson.D{{Key: "admin_account_id", Value: account.ID}}).Decode(&credential)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return LoginIdentity{}, ErrNotFound
	}
	if err != nil {
		return LoginIdentity{}, err
	}
	profile, err := r.resolveProfile(ctx, account.ID)
	if err != nil {
		return LoginIdentity{}, err
	}
	return LoginIdentity{Profile: profile, Credential: Credential{AccountID: credential.AccountID, Hash: credential.Hash}}, nil
}

func (r *MongoAdminAuthRepository) resolveProfile(ctx context.Context, accountID string) (AdminProfile, error) {
	var account accountDocument
	err := r.database.Collection(adminAccounts).FindOne(ctx, bson.D{{Key: "admin_account_id", Value: accountID}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return AdminProfile{}, ErrInactiveAdmin
	}
	if err != nil {
		return AdminProfile{}, err
	}
	assignments, err := r.database.Collection(accountRoles).Find(ctx, bson.D{{Key: "admin_account_id", Value: accountID}, {Key: "revoked_at", Value: nil}})
	if err != nil {
		return AdminProfile{}, err
	}
	var activeAssignments []assignmentDocument
	if err := assignments.All(ctx, &activeAssignments); err != nil {
		return AdminProfile{}, err
	}
	if len(activeAssignments) != 1 {
		return AdminProfile{}, ErrInvalidRole
	}
	var role roleDocument
	err = r.database.Collection(adminRoles).FindOne(ctx, bson.D{{Key: "role_id", Value: activeAssignments[0].RoleID}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}).Decode(&role)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return AdminProfile{}, ErrInvalidRole
	}
	if err != nil {
		return AdminProfile{}, err
	}
	profile := AdminProfile{ID: account.ID, Username: account.Username, Name: account.Name, Email: account.Email,
		Role: Role{ID: role.ID, Name: role.Name, Code: role.Code, System: role.System}}
	links, err := r.database.Collection(rolePermission).Find(ctx, bson.D{{Key: "role_id", Value: role.ID}})
	if err != nil {
		return AdminProfile{}, err
	}
	var linked []permissionLink
	if err := links.All(ctx, &linked); err != nil {
		return AdminProfile{}, err
	}
	if len(linked) == 0 {
		return profile, nil
	}
	ids := make(bson.A, 0, len(linked))
	for _, link := range linked {
		ids = append(ids, link.PermissionID)
	}
	cursor, err := r.database.Collection(permissions).Find(ctx, bson.D{{Key: "permission_id", Value: bson.D{{Key: "$in", Value: ids}}}, {Key: "is_active", Value: true}})
	if err != nil {
		return AdminProfile{}, err
	}
	var active []permissionDocument
	if err := cursor.All(ctx, &active); err != nil {
		return AdminProfile{}, err
	}
	for _, permission := range active {
		profile.Perms = append(profile.Perms, permission.Key)
	}
	return profile, nil
}

func (r *MongoAdminAuthRepository) CreateLoginSession(ctx context.Context, accountID string, session Session) (AdminProfile, error) {
	databaseSession, err := r.database.Client().StartSession()
	if err != nil {
		return AdminProfile{}, err
	}
	defer databaseSession.EndSession(ctx)
	result, err := databaseSession.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		profile, err := r.resolveProfile(transactionContext, accountID)
		if err != nil {
			return nil, err
		}
		updated, err := r.database.Collection(adminAccounts).UpdateOne(transactionContext,
			bson.D{{Key: "admin_account_id", Value: accountID}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "last_active", Value: session.CreatedAt}}}})
		if err != nil {
			return nil, err
		}
		if updated.MatchedCount != 1 {
			return nil, ErrInactiveAdmin
		}
		if _, err := r.database.Collection(adminSessions).InsertOne(transactionContext, toSessionDocument(session)); err != nil {
			return nil, err
		}
		return profile, nil
	})
	if err != nil {
		return AdminProfile{}, err
	}
	profile, ok := result.(AdminProfile)
	if !ok {
		return AdminProfile{}, errors.New("unexpected login transaction result")
	}
	return profile, nil
}

func (r *MongoAdminAuthRepository) FindSessionByRefreshDigest(ctx context.Context, tokenDigest string) (Session, error) {
	var document sessionDocument
	err := r.database.Collection(adminSessions).FindOne(ctx, bson.D{{Key: "refresh_token_digest", Value: tokenDigest}}).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return fromSessionDocument(document), nil
}

func (r *MongoAdminAuthRepository) RotateSession(ctx context.Context, sessionID, csrfDigest string, next Session, now time.Time) (RefreshResult, error) {
	databaseSession, err := r.database.Client().StartSession()
	if err != nil {
		return RefreshResult{}, err
	}
	defer databaseSession.EndSession(ctx)
	result, err := databaseSession.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		var previous sessionDocument
		err := r.database.Collection(adminSessions).FindOne(transactionContext, bson.D{{Key: "session_id", Value: sessionID}}).Decode(&previous)
		if errors.Is(err, mongo.ErrNoDocuments) {
			return RefreshResult{Valid: false}, nil
		}
		if err != nil {
			return nil, err
		}
		if previous.RotatedAt != nil && previous.ExpiresAt.After(now) {
			if _, err := r.database.Collection(adminSessions).UpdateMany(transactionContext,
				bson.D{{Key: "family_id", Value: previous.FamilyID}, {Key: "revoked_at", Value: nil}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: now}, {Key: "revocation_reason", Value: "REPLAY"}}}}); err != nil {
				return nil, err
			}
			return RefreshResult{Replay: true}, nil
		}
		if previous.RevokedAt != nil || !previous.ExpiresAt.After(now) || !constantDigestEqual(previous.CSRFTokenDigest, csrfDigest) {
			return RefreshResult{Valid: false}, nil
		}
		profile, err := r.resolveProfile(transactionContext, previous.AdminAccountID)
		if err != nil {
			if errors.Is(err, ErrInactiveAdmin) {
				if _, revokeErr := r.database.Collection(adminSessions).UpdateMany(transactionContext,
					bson.D{{Key: "admin_account_id", Value: previous.AdminAccountID}, {Key: "revoked_at", Value: nil}},
					bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: now}, {Key: "revocation_reason", Value: "ACCOUNT_INACTIVE"}}}}); revokeErr != nil {
					return nil, revokeErr
				}
				return RefreshResult{Valid: false}, nil
			}
			if errors.Is(err, ErrInvalidRole) {
				if _, revokeErr := r.database.Collection(adminSessions).UpdateMany(transactionContext,
					bson.D{{Key: "family_id", Value: previous.FamilyID}, {Key: "revoked_at", Value: nil}},
					bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: now}, {Key: "revocation_reason", Value: "ROLE_INACTIVE"}}}}); revokeErr != nil {
					return nil, revokeErr
				}
				return RefreshResult{Valid: false}, nil
			}
			return nil, err
		}
		updated, err := r.database.Collection(adminSessions).UpdateOne(transactionContext,
			bson.D{{Key: "session_id", Value: sessionID}, {Key: "rotated_at", Value: nil}, {Key: "revoked_at", Value: nil}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "rotated_at", Value: now}}}})
		if err != nil {
			return nil, err
		}
		if updated.ModifiedCount != 1 {
			return RefreshResult{Replay: true}, nil
		}
		if _, err := r.database.Collection(adminSessions).InsertOne(transactionContext, toSessionDocument(next)); err != nil {
			return nil, err
		}
		return RefreshResult{Identity: profile, Valid: true}, nil
	})
	if err != nil {
		return RefreshResult{}, err
	}
	refreshResult, ok := result.(RefreshResult)
	if !ok {
		return RefreshResult{}, errors.New("unexpected refresh transaction result")
	}
	return refreshResult, nil
}

func constantDigestEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (r *MongoAdminAuthRepository) RevokeSessionFamily(ctx context.Context, familyID, reason string, now time.Time) error {
	return r.revokeSessionsTransaction(ctx, bson.D{{Key: "family_id", Value: familyID}, {Key: "revoked_at", Value: nil}}, reason, now)
}

func (r *MongoAdminAuthRepository) ResolveAccess(ctx context.Context, familyID, adminID string, now time.Time) (AdminProfile, bool, error) {
	databaseSession, err := r.database.Client().StartSession()
	if err != nil {
		return AdminProfile{}, false, err
	}
	defer databaseSession.EndSession(ctx)
	result, err := databaseSession.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		count, err := r.database.Collection(adminSessions).CountDocuments(transactionContext, bson.D{
			{Key: "family_id", Value: familyID}, {Key: "admin_account_id", Value: adminID},
			{Key: "rotated_at", Value: nil}, {Key: "revoked_at", Value: nil}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now}}},
		})
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return accessResolution{}, nil
		}
		profile, err := r.resolveProfile(transactionContext, adminID)
		if errors.Is(err, ErrInactiveAdmin) || errors.Is(err, ErrInvalidRole) {
			reason := "ROLE_INACTIVE"
			if errors.Is(err, ErrInactiveAdmin) {
				reason = "ACCOUNT_INACTIVE"
			}
			if _, revokeErr := r.database.Collection(adminSessions).UpdateMany(transactionContext,
				bson.D{{Key: "admin_account_id", Value: adminID}, {Key: "revoked_at", Value: nil}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: now}, {Key: "revocation_reason", Value: reason}}}}); revokeErr != nil {
				return nil, revokeErr
			}
			return accessResolution{}, nil
		}
		if err != nil {
			return nil, err
		}
		return accessResolution{Profile: profile, Active: true}, nil
	})
	if err != nil {
		return AdminProfile{}, false, err
	}
	resolution, ok := result.(accessResolution)
	if !ok {
		return AdminProfile{}, false, errors.New("unexpected access transaction result")
	}
	return resolution.Profile, resolution.Active, nil
}

type accessResolution struct {
	Profile AdminProfile
	Active  bool
}

func (r *MongoAdminAuthRepository) RevokeAccountSessions(ctx context.Context, accountID, reason string, now time.Time) error {
	return r.revokeSessionsTransaction(ctx, bson.D{{Key: "admin_account_id", Value: accountID}, {Key: "revoked_at", Value: nil}}, reason, now)
}

func (r *MongoAdminAuthRepository) revokeSessionsTransaction(ctx context.Context, filter bson.D, reason string, now time.Time) error {
	databaseSession, err := r.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer databaseSession.EndSession(ctx)
	_, err = databaseSession.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		_, err := r.database.Collection(adminSessions).UpdateMany(transactionContext, filter,
			bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: now}, {Key: "revocation_reason", Value: reason}}}})
		return nil, err
	})
	return err
}

func (r *MongoAdminAuthRepository) ValidateCSRF(ctx context.Context, familyID, csrfDigest string) (bool, error) {
	count, err := r.database.Collection(adminSessions).CountDocuments(ctx, bson.D{
		{Key: "family_id", Value: familyID}, {Key: "rotated_at", Value: nil},
		{Key: "revoked_at", Value: nil}, {Key: "expires_at", Value: bson.D{{Key: "$gt", Value: time.Now().UTC()}}},
		{Key: "csrf_token_digest", Value: csrfDigest},
	})
	return count == 1, err
}

type rateWindowDocument struct {
	ID        string      `bson:"_id"`
	Events    []time.Time `bson:"events"`
	ExpiresAt time.Time   `bson:"expires_at"`
}

func (r *MongoAdminAuthRepository) Allow(ctx context.Context, key string, limit int, now time.Time) (time.Duration, bool, error) {
	return r.AllowWindow(ctx, key, limit, RateWindow, now)
}

func (r *MongoAdminAuthRepository) AllowWindow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (time.Duration, bool, error) {
	collection := r.database.Collection(rateWindows)
	_, err := collection.UpdateOne(ctx, bson.D{{Key: "_id", Value: key}}, bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "events", Value: bson.A{}}, {Key: "expires_at", Value: now.Add(window)}}}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		var writeError mongo.WriteException
		var commandError mongo.CommandError
		duplicateKey := errors.As(err, &writeError) && len(writeError.WriteErrors) > 0 && writeError.WriteErrors[0].Code == 11000
		duplicateKey = duplicateKey || (errors.As(err, &commandError) && commandError.Code == 11000)
		if !duplicateKey {
			return 0, false, err
		}
	}
	start := now.Add(-window)
	filtered := bson.D{{Key: "$filter", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$events", bson.A{}}}}}, {Key: "as", Value: "event"}, {Key: "cond", Value: bson.D{{Key: "$gt", Value: bson.A{"$$event", start}}}}}}}
	filter := bson.D{{Key: "_id", Value: key}, {Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{bson.D{{Key: "$size", Value: filtered}}, limit}}}}}
	pipeline := mongo.Pipeline{{{Key: "$set", Value: bson.D{
		{Key: "events", Value: bson.D{{Key: "$concatArrays", Value: bson.A{filtered, bson.A{now}}}}},
		{Key: "expires_at", Value: now.Add(window)},
	}}}}
	var result rateWindowDocument
	err = collection.FindOneAndUpdate(ctx, filter, pipeline, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if err == nil {
		return 0, true, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return 0, false, err
	}
	var full rateWindowDocument
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

func (r *MongoAdminAuthRepository) Check(ctx context.Context, key string, limit int, now time.Time) (time.Duration, bool, error) {
	var window rateWindowDocument
	err := r.database.Collection(rateWindows).FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&window)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	start := now.Add(-RateWindow)
	active := 0
	oldest := now
	for _, event := range window.Events {
		if event.After(start) {
			active++
			if event.Before(oldest) {
				oldest = event
			}
		}
	}
	if active < limit {
		return 0, true, nil
	}
	retry := oldest.Add(RateWindow).Sub(now)
	if retry < time.Second {
		retry = time.Second
	}
	return retry, false, nil
}

func toSessionDocument(session Session) sessionDocument {
	return sessionDocument{ID: session.ID, FamilyID: session.FamilyID, AdminAccountID: session.AdminAccountID,
		RefreshTokenDigest: session.RefreshTokenDigest, CSRFTokenDigest: session.CSRFTokenDigest,
		CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt, RotatedAt: session.RotatedAt,
		RevokedAt: session.RevokedAt, RevocationReason: session.RevocationReason}
}

func fromSessionDocument(document sessionDocument) Session {
	return Session{ID: document.ID, FamilyID: document.FamilyID, AdminAccountID: document.AdminAccountID,
		RefreshTokenDigest: document.RefreshTokenDigest, CSRFTokenDigest: document.CSRFTokenDigest,
		CreatedAt: document.CreatedAt, ExpiresAt: document.ExpiresAt, RotatedAt: document.RotatedAt,
		RevokedAt: document.RevokedAt, RevocationReason: document.RevocationReason}
}
