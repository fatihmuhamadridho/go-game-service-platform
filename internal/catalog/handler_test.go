package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/publicgame"
)

func TestAdminCategoryCreateUsesStore(t *testing.T) {
	store := newCatalogStoreFake()
	handler := NewHandler(store, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/service-categories/v1/create", strings.NewReader(`{"name":"Rank","slug":"joki-rank","description":"Rank services","isActive":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.admin(response, request)
	if response.Code != http.StatusCreated || len(store.categories) != 1 {
		t.Fatalf("category not persisted: status=%d categories=%d body=%s", response.Code, len(store.categories), response.Body.String())
	}
	var payload struct {
		Data struct {
			Category CategoryView `json:"category"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Category.ID == "" || payload.Data.Category.Slug != "joki-rank" || !payload.Data.Category.IsActive {
		t.Fatalf("unexpected created category: %#v", payload.Data.Category)
	}
}

func TestServiceCreatePersistsAndPublicServicesReadsStore(t *testing.T) {
	store := newCatalogStoreFake()
	category := Category{ID: "category-1", Name: "Rank", Slug: "rank", IsActive: true}
	store.categories[category.ID] = category
	handler := NewHandler(store, catalogGamesFake{publicgame.Game{ID: "game-1", Name: "Mobile Legends", Slug: "mobile-legends"}})
	request := httptest.NewRequest(http.MethodPost, "/api/admin/services/v1/create", strings.NewReader(`{"name":"Rank Boost","gameId":"game-1","categoryId":"category-1","priceAmount":25000,"isCustomPrice":false,"estimateText":"1 hari","isActive":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.admin(response, request)
	if response.Code != http.StatusCreated || len(store.services) != 1 {
		t.Fatalf("service not persisted: status=%d services=%d body=%s", response.Code, len(store.services), response.Body.String())
	}
	publicRequest := httptest.NewRequest(http.MethodGet, "/api/games/v1/mobile-legends/services", nil)
	publicRequest.SetPathValue("slug", "mobile-legends")
	publicResponse := httptest.NewRecorder()
	handler.publicServices(publicResponse, publicRequest)
	if publicResponse.Code != http.StatusOK {
		t.Fatalf("public read status=%d: %s", publicResponse.Code, publicResponse.Body.String())
	}
	var payload struct {
		Data struct {
			Services []PublicService `json:"services"`
		} `json:"data"`
	}
	if err := json.Unmarshal(publicResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Services) != 1 || payload.Data.Services[0].Price == nil || *payload.Data.Services[0].Price != 25000 || payload.Data.Services[0].CustomPrice {
		t.Fatalf("public service did not reflect persisted record: %#v", payload.Data.Services)
	}
}

func TestInactiveCategoryHidesItsServicesFromPublicCatalog(t *testing.T) {
	store := newCatalogStoreFake()
	store.categories["category-1"] = Category{ID: "category-1", Name: "Hidden", Slug: "hidden", IsActive: false}
	price := int64(25000)
	store.services["service-1"] = Service{ID: "service-1", Name: "Rank", GameID: "game-1", CategoryID: "category-1", PriceAmount: &price, IsActive: true}
	handler := NewHandler(store, catalogGamesFake{publicgame.Game{ID: "game-1", Name: "Mobile Legends", Slug: "mobile-legends"}})
	request := httptest.NewRequest(http.MethodGet, "/api/games/v1/mobile-legends/services", nil)
	request.SetPathValue("slug", "mobile-legends")
	response := httptest.NewRecorder()
	handler.publicServices(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("public read status=%d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data struct {
			Services []PublicService `json:"services"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.Services) != 0 {
		t.Fatalf("service in inactive category leaked publicly: %#v", payload.Data.Services)
	}
	count, categories, summary := handler.GameMetadata(context.Background(), "game-1")
	if count != 0 || len(categories) != 0 || summary != "" {
		t.Fatalf("inactive category leaked game metadata: count=%d categories=%#v summary=%q", count, categories, summary)
	}
}

type catalogStoreFake struct {
	categories map[string]Category
	services   map[string]Service
}

func newCatalogStoreFake() *catalogStoreFake {
	return &catalogStoreFake{categories: map[string]Category{}, services: map[string]Service{}}
}
func (s *catalogStoreFake) ListCategories(_ context.Context, active bool) ([]Category, error) {
	result := []Category{}
	for _, item := range s.categories {
		if !active || item.IsActive {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *catalogStoreFake) CategoryByID(_ context.Context, id string) (Category, error) {
	item, ok := s.categories[id]
	if !ok {
		return Category{}, ErrCategoryNotFound
	}
	return item, nil
}
func (s *catalogStoreFake) CategoryBySlug(_ context.Context, slug string) (Category, error) {
	for _, item := range s.categories {
		if item.Slug == slug {
			return item, nil
		}
	}
	return Category{}, ErrCategoryNotFound
}
func (s *catalogStoreFake) CreateCategory(_ context.Context, item Category) error {
	for _, current := range s.categories {
		if current.Slug == item.Slug {
			return ErrCategoryConflict
		}
	}
	s.categories[item.ID] = item
	return nil
}
func (s *catalogStoreFake) UpdateCategory(_ context.Context, id string, item Category) (bool, error) {
	if _, ok := s.categories[id]; !ok {
		return false, nil
	}
	item.ID = id
	s.categories[id] = item
	return true, nil
}
func (s *catalogStoreFake) DeleteCategory(_ context.Context, id string) (bool, error) {
	if _, ok := s.categories[id]; !ok {
		return false, nil
	}
	if count, _ := s.CountServicesForCategory(context.Background(), id); count > 0 {
		return false, ErrCategoryInUse
	}
	delete(s.categories, id)
	return true, nil
}
func (s *catalogStoreFake) CountServicesForCategory(_ context.Context, id string) (int64, error) {
	var count int64
	for _, item := range s.services {
		if item.CategoryID == id {
			count++
		}
	}
	return count, nil
}
func (s *catalogStoreFake) ListServices(_ context.Context, gameID string, active bool) ([]Service, error) {
	result := []Service{}
	for _, item := range s.services {
		if (gameID == "" || item.GameID == gameID) && (!active || item.IsActive) {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *catalogStoreFake) ListServicesForCategory(_ context.Context, categoryID string, active bool) ([]Service, error) {
	result := []Service{}
	for _, item := range s.services {
		if item.CategoryID == categoryID && (!active || item.IsActive) {
			result = append(result, item)
		}
	}
	return result, nil
}
func (s *catalogStoreFake) FindActiveServiceForCheckout(ctx context.Context, gameID, id string) (Service, error) {
	for _, item := range s.services {
		if item.ID == id && item.IsActive && (gameID == "" || item.GameID == gameID) {
			return item, nil
		}
	}
	return Service{}, ErrServiceNotFound
}
func (s *catalogStoreFake) CreateService(_ context.Context, item Service) error {
	s.services[item.ID] = item
	return nil
}
func (s *catalogStoreFake) UpdateService(_ context.Context, id string, item Service) (bool, error) {
	if _, ok := s.services[id]; !ok {
		return false, nil
	}
	item.ID = id
	s.services[id] = item
	return true, nil
}
func (s *catalogStoreFake) DeleteService(_ context.Context, id string) (bool, error) {
	if _, ok := s.services[id]; !ok {
		return false, nil
	}
	delete(s.services, id)
	return true, nil
}

type catalogGamesFake []publicgame.Game

func (games catalogGamesFake) List(_ context.Context, filter publicgame.Filter) (publicgame.Page, error) {
	items := make([]publicgame.Game, 0)
	for _, game := range games {
		items = append(items, game)
	}
	pages := int64(0)
	if len(items) > 0 {
		pages = 1
	}
	return publicgame.Page{Items: items, Page: filter.Page, Limit: filter.Limit, Total: int64(len(items)), TotalPages: pages}, nil
}
func (catalogGamesFake) Allow(context.Context, string, time.Time) (time.Duration, bool, error) {
	return 0, true, nil
}

var _ Store = (*catalogStoreFake)(nil)
var _ GameData = catalogGamesFake{}
