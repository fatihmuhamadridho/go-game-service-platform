package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type catalogRepositoryStub struct {
	games      []model.GameCatalog
	categories []model.ServiceCategory
	services   []model.GameService
}

type responseStatus struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s catalogRepositoryStub) ListActiveGames(context.Context) ([]model.GameCatalog, error) {
	return s.games, nil
}

func (s catalogRepositoryStub) FindActiveGameBySlug(_ context.Context, slug string) (*model.GameCatalog, error) {
	for i := range s.games {
		if s.games[i].Slug == slug {
			return &s.games[i], nil
		}
	}
	return nil, nil
}

func (s catalogRepositoryStub) ListActiveCategories(context.Context) ([]model.ServiceCategory, error) {
	return s.categories, nil
}

func (s catalogRepositoryStub) ListActiveServices(_ context.Context, gameID *bson.ObjectID) ([]model.GameService, error) {
	if gameID == nil {
		return s.services, nil
	}
	result := make([]model.GameService, 0)
	for _, service := range s.services {
		if service.GameID == *gameID {
			result = append(result, service)
		}
	}
	return result, nil
}

func (catalogRepositoryStub) GetPublicSettings(context.Context) (*model.PlatformSettings, error) {
	return nil, nil
}

func TestPublicGameListAndValidation(t *testing.T) {
	gameID := bson.NewObjectID()
	categoryID := bson.NewObjectID()
	repo := catalogRepositoryStub{
		games:      []model.GameCatalog{{ID: gameID, Name: "Mobile Legends", Slug: "mobile-legends", IsActive: true, IsPopular: true}},
		categories: []model.ServiceCategory{{ID: categoryID, Name: "Rank Boost", Slug: "rank-boost", IsActive: true}},
		services:   []model.GameService{{ID: bson.NewObjectID(), GameID: gameID, CategoryID: categoryID, Name: "Mythic", IsActive: true}},
	}
	mux := http.NewServeMux()
	RegisterCatalogRoutes(mux, controller.NewCatalogPresenter(repo))

	request := httptest.NewRequest(http.MethodGet, "/api/games/v1/list?category=rank-boost", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Status responseStatus `json:"status"`
		Data   struct {
			Games      []controller.PublicGame `json:"games"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status.Code != "GAMS00000" || payload.Data.Pagination.Total != 1 || len(payload.Data.Games) != 1 || payload.Data.Games[0].ServiceCount != 1 {
		t.Fatalf("unexpected catalog response: %+v", payload)
	}

	badRequest := httptest.NewRequest(http.MethodGet, "/api/games/v1/list?sort=INVALID", nil)
	badResponse := httptest.NewRecorder()
	mux.ServeHTTP(badResponse, badRequest)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid sort to return 400, got %d", badResponse.Code)
	}
}

func TestServicesByGameEndpoint(t *testing.T) {
	gameID := bson.NewObjectID()
	categoryID := bson.NewObjectID()
	repo := catalogRepositoryStub{
		games:      []model.GameCatalog{{ID: gameID, Name: "Mobile Legends", Slug: "mobile-legends", IsActive: true}},
		categories: []model.ServiceCategory{{ID: categoryID, Name: "Rank Boost", Slug: "rank-boost", IsActive: true}},
		services:   []model.GameService{{ID: bson.NewObjectID(), GameID: gameID, CategoryID: categoryID, Name: "Mythic", IsActive: true}},
	}
	mux := http.NewServeMux()
	RegisterCatalogRoutes(mux, controller.NewCatalogPresenter(repo))
	request := httptest.NewRequest(http.MethodGet, "/api/games/v1/mobile-legends/services", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Status responseStatus `json:"status"`
		Data   struct {
			Game     GamePayload       `json:"game"`
			Services []json.RawMessage `json:"services"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status.Code != "SVS00000" || payload.Data.Game.Slug != "mobile-legends" || len(payload.Data.Services) != 1 {
		t.Fatalf("unexpected services response: %+v", payload)
	}
}

type GamePayload struct {
	Slug string `json:"slug"`
}
