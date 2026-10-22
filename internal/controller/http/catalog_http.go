package controllers

import (
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/view"
)

type CatalogHTTP struct {
	presenter *controller.CatalogPresenter
}

func RegisterCatalogRoutes(mux *http.ServeMux, catalog *controller.CatalogPresenter) {
	handler := &CatalogHTTP{presenter: catalog}
	mux.HandleFunc("GET /api/games/v1/list", handler.listGames)
	mux.HandleFunc("GET /api/games/v1/popular", handler.listPopularGames)
	mux.HandleFunc("GET /api/games/v1/{gameSlug}/services", handler.listGameServices(true, "SVB02001", "SVS00000", "Daftar layanan game berhasil diambil.", "SVB02002", "Game tidak tersedia.", "SVT03001"))
	mux.HandleFunc("GET /api/services/v1/games/{gameSlug}", handler.listGameServices(false, "SRVB02001", "SRVS00000", "Layanan game berhasil diambil.", "SRVB02001", "Game tidak ditemukan.", "SRVT03001"))
	mux.HandleFunc("GET /api/service-categories/v1/list", handler.listCategories)
	mux.HandleFunc("GET /api/settings/v1/public", handler.publicSettings)
}

func writeJSON(w http.ResponseWriter, statusCode int, code, message string, data any) {
	view.WriteJSON(w, statusCode, code, message, data)
}

func invalidQuery(w http.ResponseWriter, code, message string) {
	writeJSON(w, http.StatusBadRequest, code, message, nil)
}

func parseIntegerQuery(r *http.Request, key string, fallback, minimum, maximum int) (int, bool) {
	values, exists := r.URL.Query()[key]
	if !exists {
		return fallback, true
	}
	if len(values) == 0 {
		return 0, false
	}
	raw := strings.TrimFunc(values[0], func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
	number := 0.0
	if raw != "" {
		parsed, ok := parseJSNumber(raw)
		if !ok {
			return 0, false
		}
		number = parsed
	}
	maxInt := int(^uint(0) >> 1)
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < float64(minimum) || number >= float64(maxInt) || (maximum > 0 && number > float64(maximum)) {
		return 0, false
	}
	return int(number), true
}

func (h *CatalogHTTP) listGames(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	allowed := map[string]struct{}{"search": {}, "sort": {}, "limit": {}, "offset": {}, "category": {}}
	for key := range query {
		if _, ok := allowed[key]; !ok {
			invalidQuery(w, "GAMB02001", "Parameter pencarian game tidak valid.")
			return
		}
	}
	limit, okLimit := parseIntegerQuery(r, "limit", 20, 1, 100)
	offset, okOffset := parseIntegerQuery(r, "offset", 0, 0, 0)
	search := query.Get("search")
	sortBy := query.Get("sort")
	if sortBy == "" {
		sortBy = "POPULAR"
	}
	category := query.Get("category")
	if !okLimit || !okOffset || limit < 1 || limit > 100 || offset < 0 || controller.JSStringLength(search) > 100 || (sortBy != "POPULAR" && sortBy != "NAME_ASC") || (category != "" && !slugPattern.MatchString(category)) {
		invalidQuery(w, "GAMB02001", "Parameter pencarian game tidak valid.")
		return
	}
	result, err := h.presenter.ListGames(r.Context(), search, limit, offset, category, sortBy)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "GAMT03001", "Layanan katalog sedang tidak tersedia.", nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Daftar game berhasil diambil.", map[string]any{
		"games":      result.Games,
		"pagination": map[string]int{"limit": limit, "offset": offset, "total": result.Total},
		"filters":    map[string]any{"serviceCategories": result.Categories},
	})
}

func (h *CatalogHTTP) listPopularGames(w http.ResponseWriter, r *http.Request) {
	for key := range r.URL.Query() {
		if key != "limit" && key != "offset" {
			invalidQuery(w, "GAMB02001", "Parameter pagination tidak valid.")
			return
		}
	}
	limit, okLimit := parseIntegerQuery(r, "limit", 6, 1, 50)
	offset, okOffset := parseIntegerQuery(r, "offset", 0, 0, 0)
	if !okLimit || !okOffset || limit < 1 || limit > 50 || offset < 0 {
		invalidQuery(w, "GAMB02001", "Parameter pagination tidak valid.")
		return
	}
	games, total, err := h.presenter.ListPopularGames(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "GAMT03001", "Layanan katalog sedang tidak tersedia.", nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Game populer berhasil diambil.", map[string]any{
		"games": games, "pagination": map[string]int{"limit": limit, "offset": offset, "total": total},
	})
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (h *CatalogHTTP) listGameServices(validateSlug bool, invalidCode, successCode, successMessage, notFoundCode, notFoundMessage, errorCode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if validateSlug && len(r.URL.Query()) != 0 {
			writeJSON(w, http.StatusBadRequest, "SVB02001", "Parameter layanan tidak didukung.", nil)
			return
		}
		slug := r.PathValue("gameSlug")
		if validateSlug {
			slug = strings.ToLower(strings.TrimFunc(slug, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }))
		}
		if validateSlug && !slugPattern.MatchString(slug) {
			writeJSON(w, http.StatusBadRequest, invalidCode, "Slug game tidak valid.", nil)
			return
		}
		result, err := h.presenter.ListServicesByGame(r.Context(), slug)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorCode, "Katalog layanan sedang tidak tersedia.", nil)
			return
		}
		if result == nil {
			writeJSON(w, http.StatusNotFound, notFoundCode, notFoundMessage, nil)
			return
		}
		writeJSON(w, http.StatusOK, successCode, successMessage, result)
	}
}

func (h *CatalogHTTP) listCategories(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeJSON(w, http.StatusBadRequest, "SCAB02001", "Parameter kategori tidak didukung.", nil)
		return
	}
	categories, err := h.presenter.ListServiceCategories(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "SCAT03001", "Katalog layanan sedang tidak tersedia.", nil)
		return
	}
	writeJSON(w, http.StatusOK, "SCAS00000", "Daftar kategori layanan berhasil diambil.", map[string]any{"categories": categories})
}

func (h *CatalogHTTP) publicSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings, err := h.presenter.PublicSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, "SETT03001", "Pengaturan platform sementara tidak tersedia.", nil)
		return
	}
	writeJSON(w, http.StatusOK, "SETS00000", "Pengaturan situs berhasil diambil.", settings)
}
