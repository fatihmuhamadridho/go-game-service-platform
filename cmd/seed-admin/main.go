package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/platform/database"
	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/term"
)

const superAdminRoleID = "role_super_admin"

type permissionSeed struct {
	id, key, name, description string
}

var permissionCatalog = []permissionSeed{
	{"permission_role_read", "role.read", "Read roles", "View administrator roles and their permissions."},
	{"permission_role_create", "role.create", "Create roles", "Create custom administrator roles."},
	{"permission_role_update", "role.update", "Update roles", "Update custom administrator roles and permissions."},
	{"permission_role_delete", "role.delete", "Delete roles", "Soft-delete custom administrator roles."},
	{"permission_admin_account_read", "admin-account.read", "Read administrator accounts", "View administrator accounts and their assigned roles."},
	{"permission_game_read", "game.read", "Read games", "View the admin game catalog."},
	{"permission_game_create", "game.create", "Create games", "Create games in the admin catalog."},
	{"permission_game_update", "game.update", "Update games", "Update games in the admin catalog."},
	{"permission_game_delete", "game.delete", "Delete games", "Soft-delete games from the admin catalog."},
}

var accountIDPattern = regexp.MustCompile(`^admin_[A-Za-z0-9_-]{1,58}$`)

type adminInput struct {
	id, username, name, email, passwordHash string
}

func main() {
	permissionsOnly := flag.Bool("permissions-only", false, "seed only system permissions, Super Admin role, and grants")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./cmd/seed-admin [--permissions-only]")
		os.Exit(2)
	}

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		fail("could not load local environment")
	}
	uri := strings.TrimSpace(os.Getenv("MONGODB_URL"))
	if uri == "" {
		uri = strings.TrimSpace(os.Getenv("MONGODB_URI"))
	}
	if uri == "" {
		fail("MONGODB_URL or MONGODB_URI is required")
	}
	databaseName := strings.TrimSpace(os.Getenv("MONGODB_DATABASE"))
	if databaseName == "" {
		parsed, err := url.Parse(uri)
		if err != nil {
			fail("MONGODB_URL is invalid")
		}
		databaseName, err = url.PathUnescape(strings.Trim(parsed.Path, "/"))
		if err != nil {
			fail("MongoDB database name in URI is invalid")
		}
	}
	if databaseName == "" {
		fail("MONGODB_DATABASE is required when MongoDB URI has no database path")
	}

	var input adminInput
	if !*permissionsOnly {
		var err error
		input, err = collectAdminInput(os.Stdin, os.Stderr)
		if err != nil {
			fail(err.Error())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := database.Connect(ctx, uri)
	if err != nil {
		fail("MongoDB connection failed; check the URI, network, and credentials")
	}
	defer func() {
		disconnectCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.Disconnect(disconnectCtx)
	}()
	db := client.Database(databaseName)
	if err := auth.NewMongoAdminAuthRepository(db).EnsureIndexes(ctx); err != nil {
		fail("MongoDB index setup failed; check for duplicate or incomplete legacy records")
	}

	if err := seed(ctx, db, input, *permissionsOnly); err != nil {
		fail(err.Error())
	}
	if *permissionsOnly {
		fmt.Println("Seeded system permission catalog and Super Admin grants.")
		return
	}
	fmt.Printf("Seeded Super Admin account %s (%s).\n", input.id, input.username)
}

func collectAdminInput(in *os.File, out io.Writer) (adminInput, error) {
	reader := bufio.NewReader(in)
	prompt := func(label string) (string, error) {
		fmt.Fprintf(out, "%s: ", label)
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.New("could not read administrator details")
		}
		return strings.TrimSpace(value), nil
	}
	var input adminInput
	var err error
	if input.username, err = prompt("Super Admin username"); err != nil {
		return input, err
	}
	if input.name, err = prompt("Super Admin display name"); err != nil {
		return input, err
	}
	if input.email, err = prompt("Super Admin email"); err != nil {
		return input, err
	}
	if input.id, err = prompt("Admin account ID (leave blank to generate one)"); err != nil {
		return input, err
	}
	if input.id == "" {
		input.id, err = newAdminID()
		if err != nil {
			return input, errors.New("could not generate administrator account ID")
		}
	}
	if err := validateAdminInput(input); err != nil {
		return input, err
	}
	if !term.IsTerminal(int(in.Fd())) {
		return input, errors.New("password entry requires an interactive terminal")
	}
	fmt.Fprint(out, "Super Admin password: ")
	password, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return input, errors.New("could not read password")
	}
	defer wipe(password)
	fmt.Fprint(out, "Confirm password: ")
	confirmation, err := term.ReadPassword(int(in.Fd()))
	fmt.Fprintln(out)
	if err != nil {
		return input, errors.New("could not read password confirmation")
	}
	defer wipe(confirmation)
	if !equalBytes(password, confirmation) {
		return input, errors.New("passwords do not match")
	}
	input.passwordHash, err = (auth.Argon2id{}).Hash(string(password))
	if err != nil {
		return input, errors.New("password does not meet the password hashing policy")
	}
	return input, nil
}

func validateAdminInput(input adminInput) error {
	if !accountIDPattern.MatchString(input.id) {
		return errors.New("admin account ID must start with admin_ and contain only letters, numbers, _ or -")
	}
	if input.username == "" || utf8.RuneCountInString(input.username) > 30 || strings.Contains(input.username, "@") {
		return errors.New("username must contain 1-30 characters and cannot contain @")
	}
	if input.name == "" || utf8.RuneCountInString(input.name) > 150 {
		return errors.New("display name must contain 1-150 characters")
	}
	if len(input.email) > 254 {
		return errors.New("email must be at most 254 characters")
	}
	address, err := mail.ParseAddress(input.email)
	if err != nil || address.Address != input.email || !strings.Contains(input.email, "@") {
		return errors.New("a valid email address is required")
	}
	return nil
}

func seed(ctx context.Context, db *mongo.Database, input adminInput, permissionsOnly bool) error {
	session, err := db.Client().StartSession()
	if err != nil {
		return errors.New("could not start MongoDB seed transaction")
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		now := time.Now().UTC()
		permissionCollection := db.Collection("permission")
		for _, permission := range permissionCatalog {
			filter := bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "permission_id", Value: permission.id}},
				bson.D{{Key: "permission_key", Value: permission.key}},
			}}}
			var existing bson.M
			findErr := permissionCollection.FindOne(tx, filter).Decode(&existing)
			if findErr == nil {
				if existing["permission_id"] != permission.id || existing["permission_key"] != permission.key {
					return nil, errors.New("an existing permission conflicts with the immutable system catalog")
				}
				_, updateErr := permissionCollection.UpdateOne(tx, bson.D{{Key: "permission_id", Value: permission.id}}, bson.D{{Key: "$set", Value: bson.D{
					{Key: "name", Value: permission.name}, {Key: "description", Value: permission.description},
					{Key: "is_sensitive", Value: false}, {Key: "is_active", Value: true}, {Key: "updated_at", Value: now},
				}}})
				if updateErr != nil {
					return nil, errors.New("could not update system permission catalog")
				}
			} else if errors.Is(findErr, mongo.ErrNoDocuments) {
				_, insertErr := permissionCollection.InsertOne(tx, bson.D{
					{Key: "permission_id", Value: permission.id}, {Key: "permission_key", Value: permission.key},
					{Key: "name", Value: permission.name}, {Key: "description", Value: permission.description},
					{Key: "is_sensitive", Value: false}, {Key: "is_active", Value: true},
					{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
				})
				if insertErr != nil {
					return nil, errors.New("could not create system permission catalog")
				}
			} else {
				return nil, errors.New("could not read system permission catalog")
			}
		}

		roles := db.Collection("admin_role")
		activeSuperAdmins, err := roles.CountDocuments(tx, bson.D{{Key: "code", Value: "SUPER_ADMIN"}, {Key: "status", Value: "ACTIVE"}, {Key: "deleted_at", Value: nil}})
		if err != nil || activeSuperAdmins > 1 {
			return nil, errors.New("could not verify the immutable SUPER_ADMIN role")
		}
		var role bson.M
		err = roles.FindOne(tx, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "role_id", Value: superAdminRoleID}}, bson.D{{Key: "code", Value: "SUPER_ADMIN"}},
		}}}).Decode(&role)
		if err == nil {
			if role["role_id"] != superAdminRoleID || role["code"] != "SUPER_ADMIN" || role["name"] != "Super Admin" || role["status"] != "ACTIVE" || role["is_system"] != true || role["deleted_at"] != nil {
				return nil, errors.New("existing SUPER_ADMIN role differs from its immutable bootstrap definition")
			}
		} else if errors.Is(err, mongo.ErrNoDocuments) {
			if permissionsOnly {
				return nil, errors.New("the immutable SUPER_ADMIN role does not exist; run the full seeder first")
			}
			_, err = roles.InsertOne(tx, bson.D{
				{Key: "role_id", Value: superAdminRoleID}, {Key: "name", Value: "Super Admin"},
				{Key: "code", Value: "SUPER_ADMIN"}, {Key: "status", Value: "ACTIVE"},
				{Key: "is_system", Value: true}, {Key: "description", Value: "System administrator role"},
				{Key: "created_at", Value: now}, {Key: "updated_at", Value: now}, {Key: "deleted_at", Value: nil},
			})
			if err != nil {
				return nil, errors.New("could not create immutable SUPER_ADMIN role")
			}
		} else {
			return nil, errors.New("could not read immutable SUPER_ADMIN role")
		}

		grants := db.Collection("role_permission")
		for _, permission := range permissionCatalog {
			_, err := grants.UpdateOne(tx,
				bson.D{{Key: "role_id", Value: superAdminRoleID}, {Key: "permission_id", Value: permission.id}},
				bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "created_at", Value: now}}}},
				options.UpdateOne().SetUpsert(true))
			if err != nil {
				return nil, errors.New("could not assign system permissions to SUPER_ADMIN")
			}
		}

		if permissionsOnly {
			return nil, nil
		}
		if err := seedAccount(tx, db, input, now); err != nil {
			return nil, err
		}
		return nil, nil
	})
	if err != nil {
		return errors.New("seeding failed; check MongoDB transaction support and existing records (details suppressed)")
	}
	return nil
}

func seedAccount(ctx context.Context, db *mongo.Database, input adminInput, now time.Time) error {
	accounts := db.Collection("admin_account")
	var account bson.M
	err := accounts.FindOne(ctx, bson.D{{Key: "admin_account_id", Value: input.id}}).Decode(&account)
	if err == nil {
		if account["username"] != input.username || account["email"] != input.email || account["name"] != input.name || account["status"] != "ACTIVE" || account["deleted_at"] != nil {
			return errors.New("existing admin account differs from supplied details")
		}
	} else if errors.Is(err, mongo.ErrNoDocuments) {
		_, err = accounts.InsertOne(ctx, bson.D{
			{Key: "admin_account_id", Value: input.id}, {Key: "username", Value: input.username},
			{Key: "name", Value: input.name}, {Key: "email", Value: input.email}, {Key: "status", Value: "ACTIVE"},
			{Key: "last_active", Value: nil}, {Key: "created_at", Value: now}, {Key: "updated_at", Value: now}, {Key: "deleted_at", Value: nil},
		})
		if err != nil {
			return errors.New("could not create admin account; username or email may already exist")
		}
	} else {
		return errors.New("could not read admin account")
	}

	credentials := db.Collection("admin_credential")
	count, err := credentials.CountDocuments(ctx, bson.D{{Key: "admin_account_id", Value: input.id}})
	if err != nil {
		return errors.New("could not check admin credentials")
	}
	if count == 0 {
		credentialID := "credential_" + input.id
		if len(credentialID) > 100 {
			credentialID = "credential_" + strings.TrimPrefix(input.id, "admin_")
		}
		_, err = credentials.InsertOne(ctx, bson.D{
			{Key: "credential_id", Value: credentialID}, {Key: "admin_account_id", Value: input.id},
			{Key: "password_hash", Value: input.passwordHash}, {Key: "password_updated_at", Value: now},
			{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
		})
		if err != nil {
			return errors.New("could not create admin credential")
		}
	}

	assignments := db.Collection("account_role")
	cursor, err := assignments.Find(ctx, bson.D{{Key: "admin_account_id", Value: input.id}, {Key: "revoked_at", Value: nil}})
	if err != nil {
		return errors.New("could not read admin role assignment")
	}
	var active []bson.M
	if err := cursor.All(ctx, &active); err != nil {
		return errors.New("could not read admin role assignment")
	}
	if len(active) > 1 || (len(active) == 1 && active[0]["role_id"] != superAdminRoleID) {
		return errors.New("admin account has a conflicting active role assignment")
	}
	if len(active) == 0 {
		_, err := assignments.InsertOne(ctx, bson.D{
			{Key: "account_role_id", Value: "account_role_" + strings.TrimPrefix(input.id, "admin_")},
			{Key: "admin_account_id", Value: input.id}, {Key: "role_id", Value: superAdminRoleID},
			{Key: "assigned_at", Value: now}, {Key: "revoked_at", Value: nil},
		})
		if err != nil {
			return errors.New("could not assign SUPER_ADMIN role to account")
		}
	}
	return nil
}

func newAdminID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "admin_" + hex.EncodeToString(random), nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for i := range left {
		difference |= left[i] ^ right[i]
	}
	return difference == 0
}

func wipe(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
