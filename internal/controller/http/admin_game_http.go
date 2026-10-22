package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminGameHTTP struct {
	auth      *controller.AuthPresenter
	presenter *controller.AdminGamePresenter
}

func RegisterAdminGameRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, games *controller.AdminGamePresenter) {
	handler := &AdminGameHTTP{auth: auth, presenter: games}
	mux.HandleFunc("GET /api/admin/games/v1/list", handler.list)
	mux.HandleFunc("POST /api/admin/games/v1/create", handler.create)
	mux.HandleFunc("GET /api/admin/games/v1/detail/{gameId}", handler.detail)
	mux.HandleFunc("PATCH /api/admin/games/v1/update/{gameId}", handler.update)
	mux.HandleFunc("DELETE /api/admin/games/v1/delete/{gameId}", handler.delete)
}

func (h *AdminGameHTTP) authorize(w http.ResponseWriter, r *http.Request) *controller.VerifiedToken {
	token := authToken(r, adminAccessCookie)
	claims, err := h.auth.VerifyAccessToken(r.Context(), token, model.AdminAccountType)
	if err != nil || claims == nil {
		writeJSON(w, http.StatusUnauthorized, "GAMB01001", "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if !controller.AdminPermissionAllowed(claims, "catalog-manage") {
		writeJSON(w, http.StatusForbidden, "GAMB01002", "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}

func (h *AdminGameHTTP) list(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	games, err := h.presenter.List(r.Context())
	if err != nil {
		failure := controller.ToAdminGameError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Daftar game berhasil diambil.", map[string]any{"games": games})
}

func (h *AdminGameHTTP) detail(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	game, err := h.presenter.Get(r.Context(), r.PathValue("gameId"))
	if err != nil {
		failure := controller.ToAdminGameError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Detail game berhasil diambil.", map[string]any{"game": game})
}

func (h *AdminGameHTTP) create(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	body, err := decodeAdminGameBody(r)
	if err != nil || body.Name == nil || body.Slug == nil {
		writeJSON(w, http.StatusBadRequest, "GAMB02001", "Data game tidak valid.", nil)
		return
	}
	game, err := h.presenter.Create(r.Context(), body, requestActivityContext(r, claims))
	if err != nil {
		failure := controller.ToAdminGameError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, http.StatusCreated, "GAMS00000", "Game berhasil dibuat.", map[string]any{"game": game})
}

func (h *AdminGameHTTP) update(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	body, err := decodeAdminGameBody(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, "GAMB02001", "Data game tidak valid.", nil)
		return
	}
	game, err := h.presenter.Update(r.Context(), r.PathValue("gameId"), body, requestActivityContext(r, claims))
	if err != nil {
		failure := controller.ToAdminGameError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Game berhasil diperbarui.", map[string]any{"game": game})
}

func (h *AdminGameHTTP) delete(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	game, err := h.presenter.Delete(r.Context(), r.PathValue("gameId"), requestActivityContext(r, claims))
	if err != nil {
		failure := controller.ToAdminGameError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, http.StatusOK, "GAMS00000", "Game berhasil dihapus.", map[string]any{"game": game})
}

func decodeAdminGameBody(r *http.Request) (controller.AdminGameInput, error) {
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil || raw == nil {
		if err == nil {
			err = errors.New("invalid game payload")
		}
		return controller.AdminGameInput{}, err
	}
	if !onlyJSONKeys(raw, "name", "slug", "serviceSummary", "artworkKey", "isActive", "isPopular", "sortOrder") {
		return controller.AdminGameInput{}, errors.New("invalid game payload")
	}
	name, ok := optionalJSONString(raw, "name")
	if !ok {
		return controller.AdminGameInput{}, errors.New("invalid game payload")
	}
	slug, ok := optionalJSONString(raw, "slug")
	if !ok {
		return controller.AdminGameInput{}, errors.New("invalid game payload")
	}
	serviceSummary, err := parseOptionalString(raw["serviceSummary"])
	if err != nil {
		return controller.AdminGameInput{}, err
	}
	artworkKey, err := parseOptionalString(raw["artworkKey"])
	if err != nil {
		return controller.AdminGameInput{}, err
	}
	isActive, ok := optionalJSONBool(raw, "isActive")
	if !ok {
		return controller.AdminGameInput{}, errors.New("invalid game payload")
	}
	isPopular, ok := optionalJSONBool(raw, "isPopular")
	if !ok {
		return controller.AdminGameInput{}, errors.New("invalid game payload")
	}
	var sortOrder *int64
	if value, exists := raw["sortOrder"]; exists {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
			return controller.AdminGameInput{}, errors.New("invalid game payload")
		}
		number, parseErr := strconv.ParseFloat(string(trimmed), 64)
		if parseErr != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || math.Abs(number) > 9007199254740991 {
			return controller.AdminGameInput{}, errors.New("invalid game payload")
		}
		parsed := int64(number)
		sortOrder = &parsed
	}
	return controller.AdminGameInput{
		Name: name, Slug: slug, ServiceSummary: serviceSummary, ArtworkKey: artworkKey,
		IsActive: isActive, IsPopular: isPopular, SortOrder: sortOrder,
	}, nil
}

func parseOptionalString(raw json.RawMessage) (model.OptionalString, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return model.OptionalString{}, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return model.OptionalString{Set: true}, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return model.OptionalString{}, err
	}
	return model.OptionalString{Set: true, Value: &value}, nil
}

func requestActivityContext(r *http.Request, claims *controller.VerifiedToken) model.ActivityContext {
	sourceIP := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if sourceIP == "" {
		sourceIP = r.RemoteAddr
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = r.Header.Get("X-Vercel-Id")
	}
	return controller.CurrentRequestAudit(claims, requestID, sourceIP, r.UserAgent())
}
