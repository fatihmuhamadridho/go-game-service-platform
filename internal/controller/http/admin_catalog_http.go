package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminCatalogHTTP struct {
	auth    *controller.AuthPresenter
	catalog *controller.AdminCatalogController
}

func RegisterAdminCatalogRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, catalog *controller.AdminCatalogController) {
	h := &AdminCatalogHTTP{auth: auth, catalog: catalog}
	mux.HandleFunc("GET /api/admin/service-categories/v1/list", h.listCategories)
	mux.HandleFunc("POST /api/admin/service-categories/v1/create", h.createCategory)
	mux.HandleFunc("GET /api/admin/service-categories/v1/detail/{categoryId}", h.getCategory)
	mux.HandleFunc("PUT /api/admin/service-categories/v1/update/{categoryId}", h.updateCategory)
	mux.HandleFunc("DELETE /api/admin/service-categories/v1/delete/{categoryId}", h.deleteCategory)
	mux.HandleFunc("GET /api/admin/services/v1/list", h.listServices)
	mux.HandleFunc("POST /api/admin/services/v1/create", h.createService)
	mux.HandleFunc("GET /api/admin/services/v1/detail/{serviceId}", h.getService)
	mux.HandleFunc("PUT /api/admin/services/v1/update/{serviceId}", h.updateService)
	mux.HandleFunc("DELETE /api/admin/services/v1/delete/{serviceId}", h.deleteService)
}
func (h *AdminCatalogHTTP) authorize(w http.ResponseWriter, r *http.Request, category bool) *controller.VerifiedToken {
	code := "SRVB01001"
	if category {
		code = "SCAB01001"
	}
	token := authToken(r, adminAccessCookie)
	claims, err := h.auth.VerifyAccessToken(r.Context(), token, model.AdminAccountType)
	if err != nil || claims == nil {
		writeJSON(w, 401, code, "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if !controller.AdminPermissionAllowed(claims, "catalog-manage") {
		writeJSON(w, 403, strings.Replace(code, "01001", "01002", 1), "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}
func (h *AdminCatalogHTTP) fail(w http.ResponseWriter, err error, prefix string) {
	e := controller.ToAdminCatalogError(err, prefix)
	writeJSON(w, e.Status, e.Code, e.Message, e.Data)
}
func (h *AdminCatalogHTTP) listCategories(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, true) == nil {
		return
	}
	rows, err := h.catalog.ListCategories(r.Context())
	if err != nil {
		h.fail(w, err, "SC")
		return
	}
	writeJSON(w, 200, "SCAS00000", "Daftar kategori berhasil diambil.", map[string]any{"categories": rows})
}
func (h *AdminCatalogHTTP) getCategory(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, true) == nil {
		return
	}
	row, err := h.catalog.GetCategory(r.Context(), r.PathValue("categoryId"))
	if err != nil {
		h.fail(w, err, "SC")
		return
	}
	writeJSON(w, 200, "SCAS00000", "Kategori berhasil diambil.", map[string]any{"category": row})
}
func (h *AdminCatalogHTTP) createCategory(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, true)
	if claims == nil {
		return
	}
	input, ok := decodeCategoryInput(r, false)
	if !ok {
		writeJSON(w, 400, "SCAB02001", "Data kategori tidak valid.", nil)
		return
	}
	row, err := h.catalog.CreateCategory(r.Context(), input, requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SC")
		return
	}
	writeJSON(w, 201, "SCAS00000", "Kategori berhasil dibuat.", map[string]any{"category": row})
}
func (h *AdminCatalogHTTP) updateCategory(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, true)
	if claims == nil {
		return
	}
	input, ok := decodeCategoryInput(r, true)
	if !ok {
		writeJSON(w, 400, "SCAB02001", "Data kategori tidak valid.", nil)
		return
	}
	row, err := h.catalog.UpdateCategory(r.Context(), r.PathValue("categoryId"), input, requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SC")
		return
	}
	writeJSON(w, 200, "SCAS00000", "Kategori berhasil diperbarui.", map[string]any{"category": row})
}
func (h *AdminCatalogHTTP) deleteCategory(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, true)
	if claims == nil {
		return
	}
	data, err := h.catalog.DeleteCategory(r.Context(), r.PathValue("categoryId"), requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SC")
		return
	}
	writeJSON(w, 200, "SCAS00000", "Kategori berhasil dihapus.", data)
}
func (h *AdminCatalogHTTP) listServices(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, false) == nil {
		return
	}
	rows, err := h.catalog.ListServices(r.Context())
	if err != nil {
		h.fail(w, err, "SRV")
		return
	}
	writeJSON(w, 200, "SRVS00000", "Daftar layanan berhasil diambil.", rows)
}
func (h *AdminCatalogHTTP) getService(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, false) == nil {
		return
	}
	row, err := h.catalog.GetService(r.Context(), r.PathValue("serviceId"))
	if err != nil {
		h.fail(w, err, "SRV")
		return
	}
	writeJSON(w, 200, "SRVS00000", "Layanan berhasil diambil.", map[string]any{"service": row})
}
func (h *AdminCatalogHTTP) createService(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, false)
	if claims == nil {
		return
	}
	input, ok := decodeServiceInput(r, false)
	if !ok {
		writeJSON(w, 400, "SRVB02001", "Data layanan tidak valid.", nil)
		return
	}
	row, err := h.catalog.CreateService(r.Context(), input, requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SRV")
		return
	}
	writeJSON(w, 201, "SRVS00000", "Layanan berhasil dibuat.", map[string]any{"service": row})
}
func (h *AdminCatalogHTTP) updateService(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, false)
	if claims == nil {
		return
	}
	input, ok := decodeServiceInput(r, true)
	if !ok {
		writeJSON(w, 400, "SRVB02001", "Data layanan tidak valid.", nil)
		return
	}
	row, err := h.catalog.UpdateService(r.Context(), r.PathValue("serviceId"), input, requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SRV")
		return
	}
	writeJSON(w, 200, "SRVS00000", "Layanan berhasil diperbarui.", map[string]any{"service": row})
}
func (h *AdminCatalogHTTP) deleteService(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, false)
	if claims == nil {
		return
	}
	data, err := h.catalog.DeleteService(r.Context(), r.PathValue("serviceId"), requestActivityContext(r, claims))
	if err != nil {
		h.fail(w, err, "SRV")
		return
	}
	writeJSON(w, 200, "SRVS00000", "Layanan berhasil dihapus.", data)
}

func decodeCategoryInput(r *http.Request, partial bool) (controller.CategoryInput, bool) {
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil || raw == nil {
		return controller.CategoryInput{}, false
	}
	if !onlyJSONKeys(raw, "name", "slug", "description", "isActive") {
		return controller.CategoryInput{}, false
	}
	name, ok := optionalJSONString(raw, "name")
	if !ok {
		return controller.CategoryInput{}, false
	}
	slug, ok := optionalJSONString(raw, "slug")
	if !ok {
		return controller.CategoryInput{}, false
	}
	isActive, ok := optionalJSONBool(raw, "isActive")
	if !ok {
		return controller.CategoryInput{}, false
	}
	description, ok := parseCatalogOptionalString(raw["description"])
	if !ok {
		return controller.CategoryInput{}, false
	}
	if (!partial && (name == nil || slug == nil)) || (partial && name == nil && slug == nil && !description.Set && isActive == nil) {
		return controller.CategoryInput{}, false
	}
	if name != nil && (controller.JSTrim(*name) == "" || controller.JSStringLength(controller.JSTrim(*name)) > 100) {
		return controller.CategoryInput{}, false
	}
	if slug != nil && (!adminCatalogSlug.MatchString(*slug) || len(*slug) > 100) {
		return controller.CategoryInput{}, false
	}
	if description.Value != nil && controller.JSStringLength(controller.JSTrim(*description.Value)) > 500 {
		return controller.CategoryInput{}, false
	}
	return controller.CategoryInput{Name: name, Slug: slug, Description: description, IsActive: isActive}, true
}

var adminCatalogSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func decodeServiceInput(r *http.Request, partial bool) (controller.ServiceInput, bool) {
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil || raw == nil {
		return controller.ServiceInput{}, false
	}
	if !onlyJSONKeys(raw, "gameId", "categoryId", "name", "priceAmount", "isCustomPrice", "estimateText", "isActive") {
		return controller.ServiceInput{}, false
	}
	gameID, ok := optionalJSONString(raw, "gameId")
	if !ok || (gameID != nil && (controller.JSTrim(*gameID) == "" || controller.JSStringLength(*gameID) > 64)) {
		return controller.ServiceInput{}, false
	}
	categoryID, ok := optionalJSONString(raw, "categoryId")
	if !ok || (categoryID != nil && (controller.JSTrim(*categoryID) == "" || controller.JSStringLength(*categoryID) > 64)) {
		return controller.ServiceInput{}, false
	}
	name, ok := optionalJSONString(raw, "name")
	if !ok {
		return controller.ServiceInput{}, false
	}
	estimateText, ok := optionalJSONString(raw, "estimateText")
	if !ok {
		return controller.ServiceInput{}, false
	}
	isCustomPrice, ok := optionalJSONBool(raw, "isCustomPrice")
	if !ok {
		return controller.ServiceInput{}, false
	}
	isActive, ok := optionalJSONBool(raw, "isActive")
	if !ok {
		return controller.ServiceInput{}, false
	}
	priceRaw, priceSet := raw["priceAmount"]
	var price *float64
	if priceSet && !bytes.Equal(bytes.TrimSpace(priceRaw), []byte("null")) {
		trimmed := bytes.TrimSpace(priceRaw)
		if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
			return controller.ServiceInput{}, false
		}
		var v float64
		if json.Unmarshal(priceRaw, &v) != nil {
			return controller.ServiceInput{}, false
		}
		price = &v
	}
	if !partial && (gameID == nil || categoryID == nil || name == nil || !priceSet || isCustomPrice == nil || estimateText == nil) {
		return controller.ServiceInput{}, false
	}
	if partial && gameID == nil && categoryID == nil && name == nil && !priceSet && isCustomPrice == nil && estimateText == nil && isActive == nil {
		return controller.ServiceInput{}, false
	}
	if name != nil && (controller.JSTrim(*name) == "" || controller.JSStringLength(controller.JSTrim(*name)) > 150) {
		return controller.ServiceInput{}, false
	}
	if estimateText != nil && (controller.JSTrim(*estimateText) == "" || controller.JSStringLength(controller.JSTrim(*estimateText)) > 100) {
		return controller.ServiceInput{}, false
	}
	if price != nil && (*price < 0 || !regexp.MustCompile(`^\d{1,16}(?:\.\d{1,2})?$`).MatchString(strconv.FormatFloat(*price, 'f', -1, 64))) {
		return controller.ServiceInput{}, false
	}
	if isCustomPrice != nil && *isCustomPrice && (!priceSet || price != nil) {
		return controller.ServiceInput{}, false
	}
	if isCustomPrice != nil && !*isCustomPrice && (price == nil) {
		return controller.ServiceInput{}, false
	}
	return controller.ServiceInput{GameID: gameID, CategoryID: categoryID, Name: name, PriceAmount: price, PriceSet: priceSet, IsCustomPrice: isCustomPrice, EstimateText: estimateText, IsActive: isActive}, true
}

func onlyJSONKeys(raw map[string]json.RawMessage, allowed ...string) bool {
	for key := range raw {
		if !containsKey(allowed, key) {
			return false
		}
	}
	return true
}

func optionalJSONString(raw map[string]json.RawMessage, key string) (*string, bool) {
	value, exists := raw[key]
	if !exists {
		return nil, true
	}
	if !isJSONString(value) {
		return nil, false
	}
	var parsed string
	if json.Unmarshal(value, &parsed) != nil {
		return nil, false
	}
	return &parsed, true
}

func optionalJSONBool(raw map[string]json.RawMessage, key string) (*bool, bool) {
	value, exists := raw[key]
	if !exists {
		return nil, true
	}
	trimmed := bytes.TrimSpace(value)
	if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
		return nil, false
	}
	var parsed bool
	if json.Unmarshal(value, &parsed) != nil {
		return nil, false
	}
	return &parsed, true
}
func parseCatalogOptionalString(raw json.RawMessage) (model.OptionalString, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return model.OptionalString{}, true
	}
	if bytes.Equal(raw, []byte("null")) {
		return model.OptionalString{Set: true}, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return model.OptionalString{}, false
	}
	return model.OptionalString{Set: true, Value: &value}, true
}
