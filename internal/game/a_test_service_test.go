package game

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type testGameData struct {
	game       Game
	listPage   GamePage
	createCall *Game
	createAuto bool
	updateCall int
	updateMap  map[string]any
	deleteCall int
	findErr    error
	createErr  error
	updateErr  error
	deleteErr  error
	limit      int
	limitKey   string
	limitRetry time.Duration
	limitBlock bool
	blockAdmin bool
	limitCalls []gameLimitCall
}

type gameLimitCall struct {
	key    string
	limit  int
	window time.Duration
}

func (data *testGameData) List(context.Context, GameFilter) (GamePage, error) {
	return data.listPage, nil
}
func (data *testGameData) FindByID(context.Context, string) (Game, error) {
	return data.game, data.findErr
}
func (data *testGameData) Create(_ context.Context, game Game, allocate bool) error {
	data.createCall = &game
	data.createAuto = allocate
	return data.createErr
}
func (data *testGameData) Update(_ context.Context, _ string, _ Game, changes map[string]any, _ time.Time) (bool, error) {
	data.updateCall++
	data.updateMap = changes
	return true, data.updateErr
}
func (data *testGameData) Delete(context.Context, string, time.Time) (bool, error) {
	data.deleteCall++
	return true, data.deleteErr
}
func (data *testGameData) Allow(_ context.Context, key string, limit int, window time.Duration, _ time.Time) (time.Duration, bool, error) {
	data.limit = limit
	data.limitKey = key
	data.limitCalls = append(data.limitCalls, gameLimitCall{key: key, limit: limit, window: window})
	blocked := data.limitBlock || data.blockAdmin && strings.HasPrefix(key, "game:admin:")
	return data.limitRetry, !blocked, nil
}

func validTestGame() Game {
	return Game{ID: "game_01ARZ3NDEKTSV4RRFFQ69G5FAV", Name: "Mobile Legends", Slug: "mobile-legends", ImageURL: "https://cdn.example.test/game.webp", SortOrder: 0}
}

func TestCreateAllocatesSortOrderWhenOmittedAndUsesInactiveDefaults(t *testing.T) {
	data := &testGameData{}
	service := &GameService{Data: data, Now: func() time.Time { return time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC) }}
	request := CreateGameRequest{Name: " Example Game ", Slug: "example-game", ImageURL: "https://cdn.example.test/game.webp"}
	if err := service.Create(context.Background(), request, false); err != nil {
		t.Fatal(err)
	}
	if data.createCall == nil || !data.createAuto || data.createCall.IsActive || data.createCall.IsPopular || data.createCall.SortOrder != 0 {
		t.Fatalf("create defaults or sort allocation flag incorrect: %#v auto=%v", data.createCall, data.createAuto)
	}
	if err := ValidateGameID(data.createCall.ID); err != nil {
		t.Fatalf("created game ID is invalid: %v", err)
	}
	if data.createCall.Name != "Example Game" {
		t.Fatalf("name was not normalized: %q", data.createCall.Name)
	}
}

func TestCreateRejectsPopularInactiveState(t *testing.T) {
	data := &testGameData{}
	service := &GameService{Data: data}
	request := CreateGameRequest{Name: "Game", Slug: "game", ImageURL: "https://cdn.example.test/game.webp", IsPopular: OptionalBool{Set: true, Value: true}}
	err := service.Create(context.Background(), request, true)
	var gameErr *GameError
	if !errors.As(err, &gameErr) || gameErr.Code != gamePopularStateCode || data.createCall != nil {
		t.Fatalf("invalid popular state was not rejected atomically: err=%v create=%#v", err, data.createCall)
	}
}

func TestCreateRequiresSuperAdminForExplicitPopularFlag(t *testing.T) {
	data := &testGameData{}
	service := &GameService{Data: data}
	request := CreateGameRequest{Name: "Game", Slug: "game", ImageURL: "https://cdn.example.test/game.webp", IsPopular: OptionalBool{Set: true, Value: false}}
	err := service.Create(context.Background(), request, false)
	var gameErr *GameError
	if !errors.As(err, &gameErr) || gameErr.Code != gameForbiddenCode || data.createCall != nil {
		t.Fatalf("non-super-admin explicitly set popular flag: err=%v create=%#v", err, data.createCall)
	}
}

func TestCreateRequiresSuperAdminForExplicitInactiveFlag(t *testing.T) {
	data := &testGameData{}
	service := &GameService{Data: data}
	request := CreateGameRequest{Name: "Game", Slug: "game", ImageURL: "https://cdn.example.test/game.webp", IsActive: OptionalBool{Set: true, Value: false}}
	err := service.Create(context.Background(), request, false)
	var gameErr *GameError
	if !errors.As(err, &gameErr) || gameErr.Code != gameForbiddenCode || data.createCall != nil {
		t.Fatalf("non-super-admin explicitly set active flag: err=%v create=%#v", err, data.createCall)
	}
}

func TestValidateGameRejectsSortOrderOutsideIntegerRange(t *testing.T) {
	game := validTestGame()
	game.SortOrder = maximumSortOrder + 1
	if err := ValidateGame(&game); err == nil {
		t.Fatal("sort order outside signed 32-bit integer range was accepted")
	}
}

func TestUpdateNoOpDoesNotWriteUpdatedAt(t *testing.T) {
	data := &testGameData{game: validTestGame()}
	service := &GameService{Data: data}
	request := UpdateGameRequest{Name: OptionalString{Set: true, Value: stringPointer("Mobile Legends")}}
	if err := service.Update(context.Background(), data.game.ID, request, false); err != nil {
		t.Fatal(err)
	}
	if data.updateCall != 0 {
		t.Fatalf("effective no-op performed %d writes", data.updateCall)
	}
}

func TestUpdateAllowsClearingDescriptionButRejectsNullImageURL(t *testing.T) {
	data := &testGameData{game: validTestGame()}
	data.game.Description = stringPointer("desc")
	service := &GameService{Data: data}
	if err := service.Update(context.Background(), data.game.ID, UpdateGameRequest{Description: OptionalString{Set: true}}, true); err != nil {
		t.Fatal(err)
	}
	if value, ok := data.updateMap["description"]; !ok || value != (*string)(nil) {
		t.Fatalf("description null did not clear field: %#v", data.updateMap)
	}
	data.updateCall = 0
	err := service.Update(context.Background(), data.game.ID, UpdateGameRequest{ImageURL: OptionalString{Set: true}}, true)
	var gameErr *GameError
	if !errors.As(err, &gameErr) || gameErr.Code != gameInvalidCode || data.updateCall != 0 {
		t.Fatalf("null imageUrl should be rejected without writing: err=%v writes=%d", err, data.updateCall)
	}
}

func TestUpdateProtectsPublicationFieldsFromNonSuperAdmin(t *testing.T) {
	data := &testGameData{game: validTestGame()}
	service := &GameService{Data: data}
	err := service.Update(context.Background(), data.game.ID, UpdateGameRequest{IsActive: OptionalBool{Set: true, Value: false}}, false)
	var gameErr *GameError
	if !errors.As(err, &gameErr) || gameErr.Code != gameForbiddenCode || data.updateCall != 0 {
		t.Fatalf("non-super-admin changed publication state: err=%v writes=%d", err, data.updateCall)
	}
}

func stringPointer(value string) *string { return &value }
