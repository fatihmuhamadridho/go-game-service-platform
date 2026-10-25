package game

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

type AdminGameHandler struct {
	service        *GameService
	auth           *auth.AdminAuthHandler
	trustedProxies []*net.IPNet
}

func NewAdminGameHandler(service *GameService, authHandler *auth.AdminAuthHandler, trustedProxies []*net.IPNet) *AdminGameHandler {
	return &AdminGameHandler{service: service, auth: authHandler, trustedProxies: trustedProxies}
}

func (h *AdminGameHandler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/games/v1/list", h.protect("game.read", false, http.HandlerFunc(h.list)))
	mux.Handle("GET /api/admin/games/v1/detail/{gameId}", h.protect("game.read", false, http.HandlerFunc(h.detail)))
	mux.Handle("POST /api/admin/games/v1/create", h.protect("game.create", true, http.HandlerFunc(h.create)))
	mux.Handle("PATCH /api/admin/games/v1/update/{gameId}", h.protect("game.update", true, http.HandlerFunc(h.update)))
	mux.Handle("DELETE /api/admin/games/v1/delete/{gameId}", h.protect("game.delete", true, http.HandlerFunc(h.delete)))
}

func (h *AdminGameHandler) protect(permission string, mutation bool, next http.Handler) http.Handler {
	return h.auth.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		profile, ok := auth.AdminProfileFromRequest(request)
		if !ok {
			writeGameError(writer, gameError(gameUnauthorizedCode, http.StatusUnauthorized, nil))
			return
		}
		if !hasPermission(profile, permission) {
			writeGameError(writer, gameError(gameForbiddenCode, http.StatusForbidden, nil))
			return
		}
		limit := gameReadAdminLimit
		adminBucket := "read"
		if mutation {
			limit = gameWriteAdminLimit
			adminBucket = "write"
		}
		ipLimit := gameReadIPLimit
		if mutation {
			ipLimit = gameWriteIPLimit
		}
		clientIP := auth.ClientIP(request, h.trustedProxies)
		if err := h.consumeLimit(request.Context(), "game:ip:"+utils.DigestToken(clientIP), ipLimit); err != nil {
			writeGameError(writer, err)
			return
		}
		if err := h.consumeLimit(request.Context(), "game:admin:"+adminBucket+":"+utils.DigestToken(profile.ID), limit); err != nil {
			writeGameError(writer, err)
			return
		}
		next.ServeHTTP(writer, request)
	}))
}

func hasPermission(profile auth.AdminProfile, permission string) bool {
	for _, current := range profile.Perms {
		if current == permission {
			return true
		}
	}
	return false
}

func isSystemSuperAdmin(profile auth.AdminProfile) bool {
	return profile.Role.System && profile.Role.Code == "SUPER_ADMIN"
}

func (h *AdminGameHandler) consumeLimit(ctx context.Context, key string, limit int) error {
	retry, allowed, err := h.service.Data.Allow(ctx, key, limit, gameRateLimitWindow, time.Now().UTC())
	if err != nil {
		return gameError(gameTechnicalCode, http.StatusInternalServerError, err)
	}
	if !allowed {
		return &GameError{Code: gameRateLimitCode, HTTPStatus: http.StatusTooManyRequests, RetryAfter: retry}
	}
	return nil
}

func (h *AdminGameHandler) list(writer http.ResponseWriter, request *http.Request) {
	filter, err := parseGameFilter(request.URL.Query())
	if err != nil {
		writeGameError(writer, gameError(gameInvalidCode, http.StatusBadRequest, err))
		return
	}
	page, err := h.service.List(request.Context(), filter)
	if err != nil {
		writeGameError(writer, err)
		return
	}
	writeGameList(writer, page)
}

func parseGameFilter(values url.Values) (GameFilter, error) {
	allowed := map[string]bool{"q": true, "isActive": true, "isPopular": true, "page": true, "limit": true}
	for key, value := range values {
		if !allowed[key] {
			continue
		}
		if len(value) != 1 {
			return GameFilter{}, ErrInvalidGame
		}
	}
	filter := GameFilter{Page: 1, Limit: 20}
	if value, ok := values["q"]; ok {
		filter.Query = strings.TrimSpace(value[0])
		if len([]rune(filter.Query)) > 100 {
			return GameFilter{}, ErrInvalidGame
		}
	}
	if value, ok := values["isActive"]; ok {
		parsed, err := strconv.ParseBool(value[0])
		if err != nil || value[0] != "true" && value[0] != "false" {
			return GameFilter{}, ErrInvalidGame
		}
		filter.IsActive = &parsed
	}
	if value, ok := values["isPopular"]; ok {
		parsed, err := strconv.ParseBool(value[0])
		if err != nil || value[0] != "true" && value[0] != "false" {
			return GameFilter{}, ErrInvalidGame
		}
		filter.IsPopular = &parsed
	}
	for key, destination := range map[string]*int64{"page": &filter.Page, "limit": &filter.Limit} {
		value, ok := values[key]
		if !ok {
			continue
		}
		parsed, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || parsed <= 0 || parsed > math.MaxInt32 {
			return GameFilter{}, ErrInvalidGame
		}
		*destination = parsed
	}
	if filter.Limit > 100 {
		return GameFilter{}, ErrInvalidGame
	}
	return filter, nil
}

func (h *AdminGameHandler) detail(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) {
		return
	}
	game, err := h.service.Detail(request.Context(), request.PathValue("gameId"))
	if err != nil {
		writeGameError(writer, err)
		return
	}
	writeGameResponse(writer, http.StatusOK, gameSuccessCode, "Game detail retrieved", game)
}

func (h *AdminGameHandler) create(writer http.ResponseWriter, request *http.Request) {
	var body CreateGameRequest
	if !decodeGameJSON(writer, request, &body) {
		return
	}
	profile, _ := auth.AdminProfileFromRequest(request)
	if err := h.service.Create(request.Context(), body, isSystemSuperAdmin(profile)); err != nil {
		writeGameError(writer, err)
		return
	}
	writeGameResponse(writer, http.StatusOK, gameSuccessCode, "Game created", nil)
}

func (h *AdminGameHandler) update(writer http.ResponseWriter, request *http.Request) {
	var body UpdateGameRequest
	if !decodeGameJSON(writer, request, &body) {
		return
	}
	profile, _ := auth.AdminProfileFromRequest(request)
	if err := h.service.Update(request.Context(), request.PathValue("gameId"), body, isSystemSuperAdmin(profile)); err != nil {
		writeGameError(writer, err)
		return
	}
	writeGameResponse(writer, http.StatusOK, gameSuccessCode, "Game updated", nil)
}

func (h *AdminGameHandler) delete(writer http.ResponseWriter, request *http.Request) {
	if !requireNoBody(writer, request) {
		return
	}
	if err := h.service.Delete(request.Context(), request.PathValue("gameId")); err != nil {
		writeGameError(writer, err)
		return
	}
	writeGameResponse(writer, http.StatusOK, gameSuccessCode, "Game deleted", nil)
}

func decodeGameJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(request.Header.Get("Content-Type"), ";", 2)[0]))
	if mediaType != "application/json" {
		writeGameError(writer, gameError(gameInvalidCode, http.StatusBadRequest, nil))
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 16*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeGameError(writer, gameError(gameInvalidCode, http.StatusBadRequest, err))
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeGameError(writer, gameError(gameInvalidCode, http.StatusBadRequest, err))
		return false
	}
	return true
}

func requireNoBody(writer http.ResponseWriter, request *http.Request) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1)
	body, err := io.ReadAll(request.Body)
	if err != nil || len(body) != 0 {
		writeGameError(writer, gameError(gameInvalidCode, http.StatusBadRequest, err))
		return false
	}
	return true
}

func writeGameList(writer http.ResponseWriter, page GamePage) {
	writeJSON(writer, http.StatusOK, struct {
		Code       string           `json:"code"`
		Message    string           `json:"message"`
		Data       []Game           `json:"data"`
		Pagination GameListMetadata `json:"pagination"`
	}{Code: gameSuccessCode, Message: "Games retrieved", Data: page.Items, Pagination: GameListMetadata{Page: page.Page, Limit: page.Limit, Total: page.Total, TotalPages: page.TotalPages}})
}

func writeGameResponse(writer http.ResponseWriter, status int, code, message string, data any) {
	writeJSON(writer, status, utils.ResponseEnvelope{Code: code, Message: message, Data: data})
}

func writeGameError(writer http.ResponseWriter, err error) {
	var typed *GameError
	if !errors.As(err, &typed) {
		typed = &GameError{Code: gameTechnicalCode, HTTPStatus: http.StatusInternalServerError}
	}
	message := "Game catalog is temporarily unavailable."
	switch typed.Code {
	case gameInvalidCode:
		message = "Invalid game request."
	case gameConflictCode:
		message = "Game name, slug, or sortOrder is already in use."
	case gamePopularStateCode:
		message = "Popular games must also be active."
	case gameNotFoundCode:
		message = "Game not found."
	case gameUnauthorizedCode:
		message = "Administrator is not authenticated."
	case gameForbiddenCode:
		message = "Game catalog access is not permitted."
	case gameRateLimitCode:
		message = "Rate limit exceeded."
	}
	if typed.HTTPStatus == http.StatusTooManyRequests {
		seconds := int(math.Ceil(typed.RetryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeGameResponse(writer, typed.HTTPStatus, typed.Code, message, nil)
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
