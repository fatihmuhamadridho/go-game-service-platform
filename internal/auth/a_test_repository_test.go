package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestAdminAuthMongoTransactionsIndexesAndReplayRevocation(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URL")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URL to a replica-set MongoDB URI to run MongoDB model integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("admin_auth_test_%d", time.Now().UnixNano())
	database := client.Database(name)
	defer database.Drop(context.Background())
	dataModel := NewMongoAdminAuthRepository(database)
	if err := dataModel.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	indexCursor, err := database.Collection(adminSessions).Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []bson.M
	if err := indexCursor.All(ctx, &indexes); err != nil {
		t.Fatal(err)
	}
	foundTTL := false
	for _, index := range indexes {
		if index["name"] == "ttl_admin_session_expiry" {
			foundTTL = true
		}
	}
	if !foundTTL {
		t.Fatal("session TTL index was not created")
	}

	now := time.Now().UTC()
	role := bson.D{{Key: "role_id", Value: "role_super_admin"}, {Key: "name", Value: "Super Admin"}, {Key: "code", Value: "SUPER_ADMIN"}, {Key: "status", Value: "ACTIVE"}, {Key: "is_system", Value: true}, {Key: "deleted_at", Value: nil}}
	account := bson.D{{Key: "admin_account_id", Value: "admin_integration"}, {Key: "username", Value: "integration"}, {Key: "name", Value: "Integration Admin"}, {Key: "email", Value: "integration@example.test"}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}
	if _, err := database.Collection(adminRoles).InsertOne(ctx, role); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(adminAccounts).InsertOne(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(credentials).InsertOne(ctx, bson.D{{Key: "credential_id", Value: "credential_integration"}, {Key: "admin_account_id", Value: "admin_integration"}, {Key: "password_hash", Value: "$argon2id$test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(accountRoles).InsertOne(ctx, bson.D{{Key: "account_role_id", Value: "account_role_integration"}, {Key: "admin_account_id", Value: "admin_integration"}, {Key: "role_id", Value: "role_super_admin"}, {Key: "assigned_at", Value: now}, {Key: "revoked_at", Value: nil}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(accountRoles).InsertOne(ctx, bson.D{{Key: "account_role_id", Value: "account_role_duplicate"}, {Key: "admin_account_id", Value: "admin_integration"}, {Key: "role_id", Value: "role_super_admin"}, {Key: "assigned_at", Value: now}, {Key: "revoked_at", Value: nil}}); err == nil {
		t.Fatal("partial unique index accepted a second active role")
	}
	if _, err := database.Collection(adminAccounts).InsertOne(ctx, bson.D{{Key: "admin_account_id", Value: "admin_duplicate"}, {Key: "username", Value: "INTEGRATION"}, {Key: "email", Value: "other@example.test"}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}); err == nil {
		t.Fatal("case-insensitive account index accepted duplicate username")
	}

	identity, err := dataModel.FindLoginIdentity(ctx, "email", "INTEGRATION@EXAMPLE.TEST")
	if err != nil || identity.Profile.ID != "admin_integration" {
		t.Fatalf("case-insensitive identity lookup failed: %#v, %v", identity, err)
	}
	first := Session{ID: "session_first", FamilyID: "family_integration", AdminAccountID: "admin_integration", RefreshTokenDigest: "refresh_digest_first", CSRFTokenDigest: "csrf_digest_first", CreatedAt: now, ExpiresAt: now.Add(RefreshLifetime)}
	profile, err := dataModel.CreateLoginSession(ctx, "admin_integration", first)
	if err != nil || profile.Role.Code != "SUPER_ADMIN" {
		t.Fatalf("transactional session creation failed: %#v, %v", profile, err)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, first.FamilyID, first.AdminAccountID, now); err != nil || !active {
		t.Fatalf("active family was not resolved: active=%v err=%v", active, err)
	}

	next := Session{ID: "session_next", FamilyID: first.FamilyID, AdminAccountID: first.AdminAccountID, RefreshTokenDigest: "refresh_digest_next", CSRFTokenDigest: "csrf_digest_next", CreatedAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Minute + RefreshLifetime)}
	rotated, err := dataModel.RotateSession(ctx, first.ID, first.CSRFTokenDigest, next, now.Add(time.Minute))
	if err != nil || !rotated.Valid || rotated.Identity.ID != "admin_integration" {
		t.Fatalf("refresh rotation failed: %#v, %v", rotated, err)
	}
	replayed, err := dataModel.RotateSession(ctx, first.ID, first.CSRFTokenDigest, Session{}, now.Add(2*time.Minute))
	if err != nil || !replayed.Replay {
		t.Fatalf("refresh replay was not detected: %#v, %v", replayed, err)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, first.FamilyID, first.AdminAccountID, now.Add(2*time.Minute)); err != nil || active {
		t.Fatalf("replay did not revoke family: active=%v err=%v", active, err)
	}

	_, err = database.Collection(adminSessions).InsertOne(ctx, bson.D{{Key: "session_id", Value: "session_duplicate"}, {Key: "refresh_token_digest", Value: first.RefreshTokenDigest}})
	if err == nil {
		t.Fatal("unique refresh digest index accepted duplicate")
	}
	var duplicate mongo.WriteException
	if !errors.As(err, &duplicate) {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}

	for attempt := 0; attempt < IdentifierLimit; attempt++ {
		if _, allowed, err := dataModel.Allow(ctx, "test:identifier", IdentifierLimit, now); err != nil || !allowed {
			t.Fatalf("failure window denied attempt %d: allowed=%v err=%v", attempt+1, allowed, err)
		}
	}
	retry, allowed, err := dataModel.Allow(ctx, "test:identifier", IdentifierLimit, now)
	if err != nil || allowed || retry <= 0 {
		t.Fatalf("failure window did not throttle: allowed=%v retry=%v err=%v", allowed, retry, err)
	}
	if _, allowed, err := dataModel.Allow(ctx, "test:identifier", IdentifierLimit, now.Add(16*time.Minute)); err != nil || !allowed {
		t.Fatalf("expired rolling window did not reopen: allowed=%v err=%v", allowed, err)
	}

	secondFamily := Session{ID: "session_offboard", FamilyID: "family_offboard", AdminAccountID: "admin_integration", RefreshTokenDigest: "refresh_digest_offboard", CSRFTokenDigest: "csrf_digest_offboard", CreatedAt: now, ExpiresAt: now.Add(RefreshLifetime)}
	if _, err := dataModel.CreateLoginSession(ctx, "admin_integration", secondFamily); err != nil {
		t.Fatal(err)
	}
	if err := dataModel.RevokeAccountSessions(ctx, "admin_integration", "OFFBOARDING", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, secondFamily.FamilyID, secondFamily.AdminAccountID, now.Add(4*time.Minute)); err != nil || active {
		t.Fatalf("offboarding did not revoke active family: active=%v err=%v", active, err)
	}
}
