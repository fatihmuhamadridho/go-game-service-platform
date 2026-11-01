package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/publicgame"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

type GameData interface {
	List(context.Context, publicgame.Filter) (publicgame.Page, error)
}

type Handler struct {
	store Store
	games GameData
}

func NewHandler(store Store, games GameData) *Handler { return &Handler{store: store, games: games} }

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /api/service-categories/v1/list", h.publicCategories)
	mux.HandleFunc("GET /api/games/v1/{slug}/services", h.publicServices)
	admin := protect(http.HandlerFunc(h.adminGuard))
	for _, pattern := range []string{
		"GET /api/admin/services/v1/list", "POST /api/admin/services/v1/create", "PUT /api/admin/services/v1/update/{id}", "DELETE /api/admin/services/v1/delete/{id}",
		"GET /api/admin/service-categories/v1/list", "POST /api/admin/service-categories/v1/create", "PUT /api/admin/service-categories/v1/update/{id}", "DELETE /api/admin/service-categories/v1/delete/{id}",
	} {
		mux.Handle(pattern, admin)
	}
}

func (h *Handler) adminGuard(w http.ResponseWriter, r *http.Request) {
	profile, ok := auth.AdminProfileFromRequest(r)
	if !ok || !profile.Role.System || profile.Role.Code != "SUPER_ADMIN" {
		writeError(w, http.StatusForbidden, "Akses katalog hanya tersedia untuk Super Admin.")
		return
	}
	h.admin(w, r)
}

func (h *Handler) admin(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/admin/service-categories/v1/list" && r.Method == http.MethodGet {
		categories, err := h.store.ListCategories(r.Context(), false)
		if err != nil {
			writeError(w, 500, "Kategori layanan tidak dapat dimuat.")
			return
		}
		views := make([]CategoryView, 0, len(categories))
		for _, category := range categories {
			count, err := h.store.CountServicesForCategory(r.Context(), category.ID)
			if err != nil {
				writeError(w, 500, "Jumlah layanan kategori tidak dapat dimuat.")
				return
			}
			views = append(views, CategoryView{Category: category, ServiceCount: int(count)})
		}
		write(w, 200, "Kategori layanan berhasil dimuat.", map[string]any{"categories": views})
		return
	}
	if path == "/api/admin/service-categories/v1/create" && r.Method == http.MethodPost {
		var request CategoryRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		category, err := ValidateCategoryRequest(request)
		if err != nil {
			writeError(w, 400, "Nama, slug, atau deskripsi kategori tidak valid.")
			return
		}
		category.ID, err = NewID("cat")
		if err == nil {
			category.CreatedAt, category.UpdatedAt = time.Now().UTC(), time.Now().UTC()
			err = h.store.CreateCategory(r.Context(), category)
		}
		if err != nil {
			h.writeStoreError(w, err, true)
			return
		}
		write(w, 201, "Kategori layanan berhasil dibuat.", map[string]any{"category": CategoryView{Category: category, ServiceCount: 0}})
		return
	}
	if id := r.PathValue("id"); strings.HasPrefix(path, "/api/admin/service-categories/v1/update/") && r.Method == http.MethodPut {
		var request CategoryRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		category, err := ValidateCategoryRequest(request)
		if err != nil {
			writeError(w, 400, "Nama, slug, atau deskripsi kategori tidak valid.")
			return
		}
		category.UpdatedAt = time.Now().UTC()
		updated, err := h.store.UpdateCategory(r.Context(), id, category)
		if err != nil {
			h.writeStoreError(w, err, true)
			return
		}
		if !updated {
			h.writeStoreError(w, ErrCategoryNotFound, true)
			return
		}
		category.ID = id
		count, err := h.store.CountServicesForCategory(r.Context(), id)
		if err != nil {
			writeError(w, 500, "Jumlah layanan kategori tidak dapat dimuat.")
			return
		}
		write(w, 200, "Kategori layanan berhasil diperbarui.", map[string]any{"category": CategoryView{Category: category, ServiceCount: int(count)}})
		return
	}
	if id := r.PathValue("id"); strings.HasPrefix(path, "/api/admin/service-categories/v1/delete/") && r.Method == http.MethodDelete {
		deleted, err := h.store.DeleteCategory(r.Context(), id)
		if err != nil {
			h.writeStoreError(w, err, true)
			return
		}
		if !deleted {
			h.writeStoreError(w, ErrCategoryNotFound, true)
			return
		}
		write(w, 200, "Kategori layanan berhasil dihapus.", map[string]any{"deleted": true})
		return
	}
	if path == "/api/admin/services/v1/list" && r.Method == http.MethodGet {
		services, err := h.store.ListServices(r.Context(), "", false)
		if err != nil {
			writeError(w, 500, "Layanan tidak dapat dimuat.")
			return
		}
		categories, err := h.store.ListCategories(r.Context(), false)
		if err != nil {
			writeError(w, 500, "Kategori layanan tidak dapat dimuat.")
			return
		}
		games, err := h.allGames(r.Context())
		if err != nil {
			writeError(w, 500, "Daftar game tidak dapat dimuat.")
			return
		}
		categoryNames := make(map[string]string, len(categories))
		for _, category := range categories {
			categoryNames[category.ID] = category.Name
		}
		gameNames := make(map[string]string, len(games))
		for _, game := range games {
			gameNames[game.ID] = game.Name
		}
		views := make([]ServiceView, 0, len(services))
		for _, service := range services {
			views = append(views, toServiceView(service, gameNames[service.GameID], categoryNames[service.CategoryID]))
		}
		options := make([]GameOption, 0, len(games))
		for _, game := range games {
			options = append(options, game)
		}
		categoryOptions := make([]map[string]string, 0, len(categories))
		for _, category := range categories {
			categoryOptions = append(categoryOptions, map[string]string{"id": category.ID, "name": category.Name})
		}
		write(w, 200, "Layanan berhasil dimuat.", map[string]any{"services": views, "games": options, "categories": categoryOptions})
		return
	}
	if path == "/api/admin/services/v1/create" && r.Method == http.MethodPost {
		var request ServiceRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		service, err := ValidateServiceRequest(request)
		if err != nil {
			writeError(w, 400, "Data layanan tidak valid.")
			return
		}
		if err := h.validateRelations(r.Context(), service); err != nil {
			h.writeStoreError(w, err, false)
			return
		}
		service.ID, err = NewID("svc")
		if err == nil {
			service.CreatedAt, service.UpdatedAt = time.Now().UTC(), time.Now().UTC()
			err = h.store.CreateService(r.Context(), service)
		}
		if err != nil {
			h.writeStoreError(w, err, false)
			return
		}
		write(w, 201, "Layanan berhasil dibuat.", map[string]any{"service": toServiceView(service, "", "")})
		return
	}
	if id := r.PathValue("id"); strings.HasPrefix(path, "/api/admin/services/v1/update/") && r.Method == http.MethodPut {
		var request ServiceRequest
		if !decodeStrict(w, r, &request) {
			return
		}
		service, err := ValidateServiceRequest(request)
		if err != nil {
			writeError(w, 400, "Data layanan tidak valid.")
			return
		}
		if err := h.validateRelations(r.Context(), service); err != nil {
			h.writeStoreError(w, err, false)
			return
		}
		service.UpdatedAt = time.Now().UTC()
		updated, err := h.store.UpdateService(r.Context(), id, service)
		if err != nil {
			h.writeStoreError(w, err, false)
			return
		}
		if !updated {
			h.writeStoreError(w, ErrServiceNotFound, false)
			return
		}
		service.ID = id
		write(w, 200, "Layanan berhasil diperbarui.", map[string]any{"service": toServiceView(service, "", "")})
		return
	}
	if id := r.PathValue("id"); strings.HasPrefix(path, "/api/admin/services/v1/delete/") && r.Method == http.MethodDelete {
		deleted, err := h.store.DeleteService(r.Context(), id)
		if err != nil {
			h.writeStoreError(w, err, false)
			return
		}
		if !deleted {
			h.writeStoreError(w, ErrServiceNotFound, false)
			return
		}
		write(w, 200, "Layanan berhasil dihapus.", map[string]any{"deleted": true})
		return
	}
	writeError(w, http.StatusNotFound, "Endpoint katalog tidak ditemukan.")
}

func (h *Handler) validateRelations(ctx context.Context, service Service) error {
	if _, err := h.store.CategoryByID(ctx, service.CategoryID); err != nil {
		return err
	}
	games, err := h.allGames(ctx)
	if err != nil {
		return err
	}
	for _, game := range games {
		if game.ID == service.GameID {
			return nil
		}
	}
	return ErrInvalidCatalog
}

func (h *Handler) publicCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.store.ListCategories(r.Context(), true)
	if err != nil {
		writeError(w, 500, "Kategori layanan tidak dapat dimuat.")
		return
	}
	services, err := h.store.ListServices(r.Context(), "", true)
	if err != nil {
		writeError(w, 500, "Layanan tidak dapat dimuat.")
		return
	}
	views := make([]map[string]any, 0, len(categories))
	for _, category := range categories {
		gameIDs := map[string]bool{}
		count := 0
		for _, service := range services {
			if service.CategoryID == category.ID {
				count++
				gameIDs[service.GameID] = true
			}
		}
		ids := make([]string, 0, len(gameIDs))
		for id := range gameIDs {
			ids = append(ids, id)
		}
		views = append(views, map[string]any{"id": category.ID, "name": category.Name, "slug": category.Slug, "description": category.Description, "active": category.IsActive, "serviceCount": count, "gameCount": len(ids), "count": len(ids), "gameIds": ids, "updatedAt": category.UpdatedAt})
	}
	write(w, 200, "Kategori layanan berhasil dimuat.", map[string]any{"categories": views})
}

func (h *Handler) publicServices(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	gameID := ""
	games, err := h.allGames(r.Context())
	if err != nil {
		writeError(w, 500, "Daftar game tidak dapat dimuat.")
		return
	}
	for _, game := range games {
		if game.Slug == slug {
			gameID = game.ID
			break
		}
	}
	items := make([]PublicService, 0)
	if gameID != "" {
		categories, err := h.store.ListCategories(r.Context(), true)
		if err != nil {
			writeError(w, 500, "Kategori layanan tidak dapat dimuat.")
			return
		}
		activeCategories := make(map[string]bool, len(categories))
		for _, category := range categories {
			activeCategories[category.ID] = true
		}
		services, err := h.store.ListServices(r.Context(), gameID, true)
		if err != nil {
			writeError(w, 500, "Layanan game tidak dapat dimuat.")
			return
		}
		for _, service := range services {
			if !activeCategories[service.CategoryID] {
				continue
			}
			items = append(items, PublicService{ID: service.ID, Name: service.Name, Price: service.PriceAmount, CustomPrice: service.PriceAmount == nil, Estimate: service.Estimate})
		}
	}
	write(w, 200, "Layanan game berhasil dimuat.", map[string]any{"services": items})
}

func (h *Handler) GameIDsInCategory(ctx context.Context, slug string) []string {
	category, err := h.store.CategoryBySlug(ctx, slug)
	if err != nil || !category.IsActive {
		return []string{}
	}
	services, err := h.store.ListServicesForCategory(ctx, category.ID, true)
	if err != nil {
		return []string{}
	}
	ids := make(map[string]bool)
	for _, service := range services {
		ids[service.GameID] = true
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}

func (h *Handler) GameMetadata(ctx context.Context, gameID string) (int64, []publicgame.CategorySummary, string) {
	services, err := h.store.ListServices(ctx, gameID, true)
	if err != nil {
		return 0, []publicgame.CategorySummary{}, ""
	}
	categories, err := h.store.ListCategories(ctx, true)
	if err != nil {
		return int64(len(services)), []publicgame.CategorySummary{}, ""
	}
	categoryByID := make(map[string]Category, len(categories))
	for _, category := range categories {
		categoryByID[category.ID] = category
	}
	summaries := make([]publicgame.CategorySummary, 0)
	seen := make(map[string]bool)
	names := make([]string, 0, len(services))
	for _, service := range services {
		if _, ok := categoryByID[service.CategoryID]; !ok {
			continue
		}
		names = append(names, service.Name)
		if category, ok := categoryByID[service.CategoryID]; ok && !seen[category.ID] {
			summaries = append(summaries, publicgame.CategorySummary{ID: category.ID, Name: category.Name, Slug: category.Slug})
			seen[category.ID] = true
		}
	}
	return int64(len(names)), summaries, strings.Join(names, " · ")
}

func (h *Handler) allGames(ctx context.Context) ([]GameOption, error) {
	if h.games == nil {
		return []GameOption{}, nil
	}
	result := make([]GameOption, 0)
	for pageNumber := int64(1); pageNumber <= 1000; pageNumber++ {
		page, err := h.games.List(ctx, publicgame.Filter{Page: pageNumber, Limit: 100})
		if err != nil {
			return nil, err
		}
		for _, game := range page.Items {
			result = append(result, GameOption{ID: game.ID, Name: game.Name, Slug: game.Slug})
		}
		if pageNumber >= page.TotalPages || len(page.Items) == 0 {
			break
		}
	}
	return result, nil
}

func toServiceView(service Service, gameName, categoryName string) ServiceView {
	return ServiceView{ID: service.ID, Name: service.Name, GameID: service.GameID, Game: gameName, CategoryID: service.CategoryID, Category: categoryName, Price: service.PriceAmount, Estimate: service.Estimate, Active: service.IsActive, UpdatedAt: service.UpdatedAt}
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	if strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0])) != "application/json" {
		writeError(w, 400, "Content-Type harus application/json.")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "Payload katalog tidak valid.")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeError(w, 400, "Payload katalog tidak valid.")
		return false
	}
	return true
}

func (h *Handler) writeStoreError(w http.ResponseWriter, err error, category bool) {
	switch {
	case errors.Is(err, ErrInvalidCatalog):
		writeError(w, 400, "Data katalog tidak valid.")
	case !category && errors.Is(err, ErrCategoryNotFound):
		writeError(w, 400, "Kategori layanan tidak tersedia.")
	case category && errors.Is(err, ErrCategoryNotFound), !category && errors.Is(err, ErrServiceNotFound):
		writeError(w, 404, "Data katalog tidak ditemukan.")
	case errors.Is(err, ErrCategoryConflict), errors.Is(err, ErrServiceConflict), errors.Is(err, ErrCategoryInUse):
		writeError(w, 409, "Data katalog duplikat atau masih digunakan.")
	default:
		writeError(w, 500, "Penyimpanan katalog tidak tersedia.")
	}
}

func write(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, status, utils.ResponseEnvelope{Code: "CAT00000", Message: message, Data: data})
}

func writeError(w http.ResponseWriter, status int, message string) { write(w, status, message, nil) }
