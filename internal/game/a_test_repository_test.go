package game

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestGameIndexPlanCoversUniquenessQueriesAndTombstoneRetention(t *testing.T) {
	indexes := gameIndexModels()
	seen := map[string]options.IndexOptions{}
	for _, index := range indexes {
		if index.Options == nil {
			t.Fatal("game index is missing a name")
		}
		resolved := options.IndexOptions{}
		for _, apply := range index.Options.List() {
			if err := apply(&resolved); err != nil {
				t.Fatal(err)
			}
		}
		if resolved.Name == nil {
			t.Fatal("game index is missing a name")
		}
		seen[*resolved.Name] = resolved
	}
	for _, name := range []string{"uq_game_catalog_id", "uq_game_catalog_slug", "uq_game_catalog_sort_order", "uq_game_catalog_name_ci", "ix_game_catalog_admin_list", "ix_game_catalog_platform", "ix_game_catalog_popular", "ttl_game_catalog_deleted_at"} {
		if _, ok := seen[name]; !ok {
			t.Errorf("missing index %q", name)
		}
	}
	if seen["uq_game_catalog_slug"].Unique == nil || !*seen["uq_game_catalog_slug"].Unique {
		t.Fatal("slug index must be unique across retained documents")
	}
	if seen["uq_game_catalog_sort_order"].Unique == nil || !*seen["uq_game_catalog_sort_order"].Unique {
		t.Fatal("sort-order index must be unique across retained documents")
	}
	if seen["uq_game_catalog_name_ci"].Collation == nil || seen["uq_game_catalog_name_ci"].Collation.Strength != 2 {
		t.Fatal("name index must enforce case-insensitive uniqueness")
	}
	if seen["ttl_game_catalog_deleted_at"].ExpireAfterSeconds == nil || *seen["ttl_game_catalog_deleted_at"].ExpireAfterSeconds != int32((60*24*time.Hour).Seconds()) {
		t.Fatal("tombstone TTL must be 60 days")
	}
}

func TestPublicGameQueryAlwaysRequiresActiveAndNotDeleted(t *testing.T) {
	query := publicGameQuery(bson.D{{Key: "platform", Value: "PC"}, {Key: "is_active", Value: false}})
	if len(query) != 1 || query[0].Key != "$and" {
		t.Fatalf("caller filter can override visibility constraints: %#v", query)
	}
	clauses, ok := query[0].Value.(bson.A)
	if !ok || len(clauses) != 2 {
		t.Fatalf("visibility filter not combined safely: %#v", query)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestMongoGameIndexesAndConcurrentCreateLifecycle(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URL")
	if uri == "" {
		t.Skip("set MONGODB_TEST_URL to a disposable MongoDB URI to run game catalog integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatal(err)
	}
	database := client.Database(fmt.Sprintf("game_catalog_test_%d", time.Now().UnixNano()))
	defer func() { _ = database.Drop(context.Background()) }()
	repository := NewMongoGameRepository(database)
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}

	base := validTestGame()
	if err := repository.Create(ctx, base, false); err != nil {
		t.Fatal(err)
	}
	caseDuplicate := base
	caseDuplicate.ID = "game_01ARZ3NDEKTSV4RRFFQ69G5FAW"
	caseDuplicate.Name = "mobile legends"
	caseDuplicate.Slug = "mobile-legends-case"
	caseDuplicate.SortOrder = 1
	if err := repository.Create(ctx, caseDuplicate, false); err != ErrGameConflict {
		t.Fatalf("case-insensitive duplicate name was not rejected: %v", err)
	}

	const concurrentCount = 8
	var workers sync.WaitGroup
	errorsByWorker := make(chan error, concurrentCount)
	for worker := 0; worker < concurrentCount; worker++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			game := validTestGame()
			game.ID, _ = NewGameID()
			game.Name = fmt.Sprintf("Concurrent Game %d", index)
			game.Slug = fmt.Sprintf("concurrent-game-%d", index)
			if err := repository.Create(ctx, game, true); err != nil {
				errorsByWorker <- err
			}
		}(worker)
	}
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Errorf("concurrent create failed: %v", err)
	}
	page, err := repository.List(ctx, GameFilter{Page: 1, Limit: 100})
	if err != nil || page.Total != concurrentCount+1 {
		t.Fatalf("concurrent sort allocation failed: total=%d err=%v", page.Total, err)
	}
	for index := 1; index < len(page.Items); index++ {
		if page.Items[index-1].SortOrder >= page.Items[index].SortOrder {
			t.Fatalf("sort order is not unique and ascending: %#v", page.Items)
		}
	}

	active := validTestGame()
	active.ID = "game_01ARZ3NDEKTSV4RRFFQ69G5FAX"
	active.Name = "Popular Game"
	active.Slug = "popular-game"
	active.SortOrder = 100
	active.IsActive = true
	active.IsPopular = true
	if err := repository.Create(ctx, active, false); err != nil {
		t.Fatal(err)
	}
	popularPage, err := repository.List(ctx, GameFilter{Query: "POPULAR", IsActive: boolPointer(true), IsPopular: boolPointer(true), Page: 1, Limit: 20})
	if err != nil || popularPage.Total != 1 || popularPage.TotalPages != 1 {
		t.Fatalf("case-insensitive list search/filter failed: page=%#v err=%v", popularPage, err)
	}
	emptyPage, err := repository.List(ctx, GameFilter{Query: "not-found", Page: 1, Limit: 20})
	if err != nil || emptyPage.Total != 0 || emptyPage.TotalPages != 0 || emptyPage.Items == nil {
		t.Fatalf("empty list result contract failed: page=%#v err=%v", emptyPage, err)
	}
	if _, err := repository.Update(ctx, active.ID, active, map[string]any{"is_active": false}, time.Now().UTC()); err != ErrGameState {
		t.Fatalf("concurrent invariant guard allowed popular inactive state: %v", err)
	}
	publicCursor, err := repository.FindPublic(ctx, bson.D{{Key: "game_id", Value: active.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !publicCursor.Next(ctx) {
		_ = publicCursor.Close(ctx)
		t.Fatal("active game was hidden from the public query helper")
	}
	_ = publicCursor.Close(ctx)

	now := time.Now().UTC()
	for attempt := 0; attempt < 2; attempt++ {
		if retry, allowed, err := repository.Allow(ctx, "game:test-window", 2, time.Minute, now); err != nil || !allowed || retry != 0 {
			t.Fatalf("rate limit denied request %d: allowed=%v retry=%s err=%v", attempt+1, allowed, retry, err)
		}
	}
	if retry, allowed, err := repository.Allow(ctx, "game:test-window", 2, time.Minute, now.Add(time.Second)); err != nil || allowed || retry <= 0 {
		t.Fatalf("rate limit did not enforce rolling window: allowed=%v retry=%s err=%v", allowed, retry, err)
	}
	maxOrder := validTestGame()
	maxOrder.ID = "game_01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	maxOrder.Name = "Last Sort Position"
	maxOrder.Slug = "last-sort-position"
	maxOrder.SortOrder = maximumSortOrder
	if err := repository.Create(ctx, maxOrder, false); err != nil {
		t.Fatal(err)
	}
	noSortSpace := validTestGame()
	noSortSpace.ID = "game_01ARZ3NDEKTSV4RRFFQ69G5FB0"
	noSortSpace.Name = "No Sort Space"
	noSortSpace.Slug = "no-sort-space"
	if err := repository.Create(ctx, noSortSpace, true); err != ErrGameConflict {
		t.Fatalf("automatic sort allocation exceeded integer range: %v", err)
	}

	if _, err := repository.Delete(ctx, base.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Delete(ctx, base.ID, time.Now().UTC()); err != nil {
		t.Fatalf("repeated tombstone delete failed: %v", err)
	}
	if _, err := repository.FindByID(ctx, base.ID); err != ErrGameNotFound {
		t.Fatalf("deleted game remained visible to admin detail: %v", err)
	}
	cursor, err := repository.FindPublic(ctx, bson.D{{Key: "game_id", Value: base.ID}})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		t.Fatal("public reader returned a deleted game")
	}
	retainedDuplicate := base
	retainedDuplicate.ID = "game_01ARZ3NDEKTSV4RRFFQ69G5FAY"
	retainedDuplicate.Name = "Reused slug"
	retainedDuplicate.SortOrder = 200
	if err := repository.Create(ctx, retainedDuplicate, false); err != ErrGameConflict {
		t.Fatalf("soft-deleted slug was not reserved during retention: %v", err)
	}

	indexesCursor, err := database.Collection(gameCollection).Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer indexesCursor.Close(ctx)
	var foundTTL bool
	for indexesCursor.Next(ctx) {
		var index bson.M
		if err := indexesCursor.Decode(&index); err != nil {
			t.Fatal(err)
		}
		if index["name"] == "ttl_game_catalog_deleted_at" {
			foundTTL = index["expireAfterSeconds"] == int32(gameRetention.Seconds()) || index["expireAfterSeconds"] == int64(gameRetention.Seconds())
		}
	}
	if !foundTTL {
		t.Fatal("MongoDB TTL index for 60-day tombstones missing")
	}
}
