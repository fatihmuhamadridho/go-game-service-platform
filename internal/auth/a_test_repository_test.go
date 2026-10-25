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

func TestAuthIndexPlanCoversAuthenticationAndRoleERDs(t *testing.T) {
	expected := map[string]map[string]bool{
		adminAccounts:  {"uq_admin_account_id": true, "uq_admin_account_username_ci": true, "uq_admin_account_email_ci": true, "ix_admin_account_list": true},
		credentials:    {"uq_admin_credential_id": true, "uq_admin_credential_account": true},
		adminSessions:  {"uq_admin_session_id": true, "uq_admin_session_refresh_digest": true, "ix_admin_session_family_active": true, "ix_admin_session_account_active": true, "ttl_admin_session_expiry": true},
		accountRoles:   {"uq_account_role_id": true, "uq_account_role_admin_active": true, "ix_account_role_role_active": true},
		adminRoles:     {"uq_admin_role_id": true, "uq_admin_role_name_active": true, "uq_admin_role_code_active": true, "ix_admin_role_list": true},
		rolePermission: {"uq_role_permission_pair": true},
		permissions:    {"uq_permission_id": true, "uq_permission_key": true, "ix_permission_active_key": true},
		rateWindows:    {"ttl_rate_limit_expiry": true},
	}
	resolvedByName := make(map[string]options.IndexOptions)
	for _, collection := range authIndexModels() {
		want, ok := expected[collection.collection]
		if !ok {
			t.Fatalf("unexpected indexed collection %q", collection.collection)
		}
		got := make(map[string]bool)
		for _, model := range collection.models {
			if model.Options == nil {
				t.Fatalf("index in %s has no explicit stable name", collection.collection)
			}
			resolved := options.IndexOptions{}
			for _, apply := range model.Options.List() {
				if err := apply(&resolved); err != nil {
					t.Fatalf("resolve index options: %v", err)
				}
			}
			if resolved.Name == nil {
				t.Fatalf("index in %s has no explicit stable name", collection.collection)
			}
			got[*resolved.Name] = true
			resolvedByName[*resolved.Name] = resolved
		}
		if len(got) != len(want) {
			t.Errorf("%s indexes = %v; want %v", collection.collection, got, want)
			continue
		}
		for name := range want {
			if !got[name] {
				t.Errorf("%s missing index %s", collection.collection, name)
			}
		}
		delete(expected, collection.collection)
	}
	if len(expected) != 0 {
		t.Errorf("index plan missing collections: %v", expected)
	}
	for _, name := range []string{
		"uq_admin_account_id", "uq_admin_account_username_ci", "uq_admin_account_email_ci",
		"uq_admin_credential_id", "uq_admin_credential_account", "uq_admin_session_id",
		"uq_admin_session_refresh_digest", "uq_account_role_id", "uq_account_role_admin_active",
		"uq_admin_role_id", "uq_admin_role_name_active", "uq_admin_role_code_active",
		"uq_role_permission_pair", "uq_permission_id", "uq_permission_key",
	} {
		optionsFound, ok := resolvedByName[name]
		if !ok || optionsFound.Unique == nil || !*optionsFound.Unique {
			t.Errorf("index %s must be unique", name)
		}
	}
	for _, name := range []string{"uq_admin_account_username_ci", "uq_admin_account_email_ci", "uq_admin_role_name_active"} {
		indexOptions, ok := resolvedByName[name]
		if !ok || indexOptions.Collation == nil || indexOptions.Collation.Locale != "en" || indexOptions.Collation.Strength != 2 {
			t.Errorf("index %s must use case-insensitive English collation", name)
		}
	}
	for _, name := range []string{"ttl_admin_session_expiry", "ttl_rate_limit_expiry"} {
		indexOptions, ok := resolvedByName[name]
		if !ok || indexOptions.ExpireAfterSeconds == nil || *indexOptions.ExpireAfterSeconds != 0 {
			t.Errorf("index %s must be a zero-second TTL index", name)
		}
	}
	for _, name := range []string{"ix_admin_account_list", "uq_account_role_admin_active", "ix_account_role_role_active", "uq_admin_role_name_active", "uq_admin_role_code_active", "ix_admin_role_list"} {
		indexOptions, ok := resolvedByName[name]
		if !ok || indexOptions.PartialFilterExpression == nil {
			t.Errorf("index %s must use its ERD partial filter", name)
		}
	}
}

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
	for _, group := range authIndexModels() {
		indexCursor, err := database.Collection(group.collection).Indexes().List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var indexes []bson.M
		if err := indexCursor.All(ctx, &indexes); err != nil {
			t.Fatal(err)
		}
		found := make(map[string]bool, len(indexes))
		for _, index := range indexes {
			if name, ok := index["name"].(string); ok {
				found[name] = true
			}
		}
		for _, model := range group.models {
			resolved := options.IndexOptions{}
			for _, apply := range model.Options.List() {
				if err := apply(&resolved); err != nil {
					t.Fatal(err)
				}
			}
			if !found[*resolved.Name] {
				t.Errorf("%s index %s was not created", group.collection, *resolved.Name)
			}
		}
	}

	now := time.Now().UTC()
	role := bson.D{{Key: "role_id", Value: "role_super_admin"}, {Key: "name", Value: "Super Admin"}, {Key: "code", Value: "SUPER_ADMIN"}, {Key: "status", Value: "ACTIVE"}, {Key: "is_system", Value: true}, {Key: "deleted_at", Value: nil}}
	account := bson.D{{Key: "admin_account_id", Value: "admin_integration"}, {Key: "username", Value: "integration"}, {Key: "name", Value: "Integration Admin"}, {Key: "email", Value: "integration@example.test"}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}}
	if _, err := database.Collection(adminRoles).InsertOne(ctx, role); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(adminRoles).InsertOne(ctx, bson.D{{Key: "role_id", Value: "role_support"}, {Key: "name", Value: "Support"}, {Key: "code", Value: "SUPPORT"}, {Key: "status", Value: "ACTIVE"}, {Key: "is_system", Value: false}, {Key: "deleted_at", Value: nil}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(adminRoles).InsertOne(ctx, bson.D{{Key: "role_id", Value: "role_support_duplicate"}, {Key: "name", Value: "SUPPORT"}, {Key: "code", Value: "SUPPORT_DUPLICATE"}, {Key: "status", Value: "ACTIVE"}, {Key: "is_system", Value: false}, {Key: "deleted_at", Value: nil}}); err == nil {
		t.Fatal("case-insensitive role-name index accepted duplicate active role name")
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
	failedNext := next
	failedNext.ID = first.ID
	if _, err := dataModel.RotateSession(ctx, first.ID, first.CSRFTokenDigest, failedNext, now.Add(time.Minute)); err == nil {
		t.Fatal("duplicate next-generation ID did not fail the transaction")
	}
	var firstAfterFailedRotation sessionDocument
	if err := database.Collection(adminSessions).FindOne(ctx, bson.D{{Key: "session_id", Value: first.ID}}).Decode(&firstAfterFailedRotation); err != nil {
		t.Fatal(err)
	}
	if firstAfterFailedRotation.RotatedAt != nil {
		t.Fatal("failed refresh store partially rotated the previous generation")
	}
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

	concurrentBase := Session{ID: "session_concurrent", FamilyID: "family_concurrent", AdminAccountID: "admin_integration", RefreshTokenDigest: "refresh_digest_concurrent", CSRFTokenDigest: "csrf_digest_concurrent", CreatedAt: now.Add(3 * time.Minute), ExpiresAt: now.Add(3*time.Minute + RefreshLifetime)}
	if _, err := dataModel.CreateLoginSession(ctx, "admin_integration", concurrentBase); err != nil {
		t.Fatal(err)
	}
	concurrentResults := make(chan RefreshResult, 2)
	concurrentErrors := make(chan error, 2)
	for _, nextGeneration := range []Session{
		{ID: "session_concurrent_a", FamilyID: concurrentBase.FamilyID, AdminAccountID: concurrentBase.AdminAccountID, RefreshTokenDigest: "refresh_digest_concurrent_a", CSRFTokenDigest: "csrf_digest_concurrent_a", CreatedAt: now.Add(4 * time.Minute), ExpiresAt: now.Add(4*time.Minute + RefreshLifetime)},
		{ID: "session_concurrent_b", FamilyID: concurrentBase.FamilyID, AdminAccountID: concurrentBase.AdminAccountID, RefreshTokenDigest: "refresh_digest_concurrent_b", CSRFTokenDigest: "csrf_digest_concurrent_b", CreatedAt: now.Add(4 * time.Minute), ExpiresAt: now.Add(4*time.Minute + RefreshLifetime)},
	} {
		nextGeneration := nextGeneration
		go func() {
			result, err := dataModel.RotateSession(ctx, concurrentBase.ID, concurrentBase.CSRFTokenDigest, nextGeneration, now.Add(4*time.Minute))
			concurrentResults <- result
			concurrentErrors <- err
		}()
	}
	validRotations, replayedRotations := 0, 0
	for range 2 {
		result := <-concurrentResults
		if err := <-concurrentErrors; err != nil {
			t.Fatalf("concurrent refresh failed: %v", err)
		}
		if result.Valid {
			validRotations++
		}
		if result.Replay {
			replayedRotations++
		}
	}
	if validRotations != 1 || replayedRotations != 1 {
		t.Fatalf("concurrent refresh was not single-use: valid=%d replay=%d", validRotations, replayedRotations)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, concurrentBase.FamilyID, concurrentBase.AdminAccountID, now.Add(5*time.Minute)); err != nil || active {
		t.Fatalf("concurrent replay did not revoke family: active=%v err=%v", active, err)
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

	inactiveFamily := Session{ID: "session_inactive", FamilyID: "family_inactive", AdminAccountID: "admin_integration", RefreshTokenDigest: "refresh_digest_inactive", CSRFTokenDigest: "csrf_digest_inactive", CreatedAt: now.Add(5 * time.Minute), ExpiresAt: now.Add(5*time.Minute + RefreshLifetime)}
	if _, err := dataModel.CreateLoginSession(ctx, "admin_integration", inactiveFamily); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(adminAccounts).UpdateOne(ctx, bson.D{{Key: "admin_account_id", Value: "admin_integration"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "INACTIVE"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, inactiveFamily.FamilyID, inactiveFamily.AdminAccountID, now.Add(6*time.Minute)); err != nil || active {
		t.Fatalf("inactive account retained active access: active=%v err=%v", active, err)
	}
	var inactiveSession sessionDocument
	if err := database.Collection(adminSessions).FindOne(ctx, bson.D{{Key: "session_id", Value: inactiveFamily.ID}}).Decode(&inactiveSession); err != nil || inactiveSession.RevokedAt == nil {
		t.Fatalf("inactive account family was not revoked: session=%#v err=%v", inactiveSession, err)
	}
	deletedFamily := Session{ID: "session_deleted", FamilyID: "family_deleted", AdminAccountID: "admin_integration", RefreshTokenDigest: "refresh_digest_deleted", CSRFTokenDigest: "csrf_digest_deleted", CreatedAt: now.Add(6 * time.Minute), ExpiresAt: now.Add(6*time.Minute + RefreshLifetime)}
	if _, err := database.Collection(adminSessions).InsertOne(ctx, toSessionDocument(deletedFamily)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(adminAccounts).UpdateOne(ctx, bson.D{{Key: "admin_account_id", Value: "admin_integration"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: now}}}}); err != nil {
		t.Fatal(err)
	}
	if _, active, err := dataModel.ResolveAccess(ctx, deletedFamily.FamilyID, deletedFamily.AdminAccountID, now.Add(7*time.Minute)); err != nil || active {
		t.Fatalf("soft-deleted account retained active access: active=%v err=%v", active, err)
	}
}
