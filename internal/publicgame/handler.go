package publicgame

import (
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

type Handler struct {
	service        *Service
	trustedProxies []*net.IPNet
}

func NewHandler(service *Service, trustedProxies []*net.IPNet) *Handler {
	return &Handler{service: service, trustedProxies: trustedProxies}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/games/v1/list", h.list)
	mux.HandleFunc("GET /api/games/v1/popular", h.popular)
}

func (h *Handler) list(writer http.ResponseWriter, request *http.Request) {
	if err := h.limit(request); err != nil {
		writeError(writer, err)
		return
	}
	filter, err := parseListQuery(request.URL.Query())
	if err != nil {
		writeError(writer, err)
		return
	}
	page, err := h.service.List(request.Context(), filter)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeList(writer, page, "Games retrieved")
}

func (h *Handler) popular(writer http.ResponseWriter, request *http.Request) {
	if err := h.limit(request); err != nil {
		writeError(writer, err)
		return
	}
	filter, err := parsePopularQuery(request.URL.Query())
	if err != nil {
		writeError(writer, err)
		return
	}
	page, err := h.service.List(request.Context(), filter)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeList(writer, page, "Popular games retrieved")
}

func (h *Handler) limit(request *http.Request) error {
	ip := auth.ClientIP(request, h.trustedProxies)
	return h.service.Allow(request.Context(), "public-game:ip:"+utils.DigestToken(ip))
}

func parseListQuery(values url.Values) (Filter, error) {
	for _, key := range []string{"search", "platform", "sort", "page", "limit"} {
		if len(values[key]) > 1 {
			return Filter{}, invalidError()
		}
	}
	filter := Filter{Page: 1, Limit: 20, Sort: "SORT_ORDER_ASC"}
	if value, ok := oneValue(values, "search"); ok {
		filter.Search = strings.TrimSpace(value)
		if len([]rune(filter.Search)) > 100 {
			return Filter{}, invalidError()
		}
	}
	if value, ok := oneValue(values, "platform"); ok {
		filter.Platform = value
	}
	if value, ok := oneValue(values, "sort"); ok {
		filter.Sort = value
	}
	if err := parsePagination(values, &filter.Page, &filter.Limit); err != nil {
		return Filter{}, err
	}
	return filter, nil
}

func parsePopularQuery(values url.Values) (Filter, error) {
	filter := Filter{Page: 1, Limit: 6, Sort: "SORT_ORDER_ASC", Popular: true}
	if err := parsePagination(values, &filter.Page, &filter.Limit); err != nil {
		return Filter{}, err
	}
	return filter, nil
}

func parsePagination(values url.Values, page, limit *int64) error {
	for key, target := range map[string]*int64{"page": page, "limit": limit} {
		value, exists := oneValue(values, key)
		if !exists {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 || parsed > math.MaxInt32 {
			return invalidError()
		}
		*target = parsed
	}
	return nil
}

func oneValue(values url.Values, key string) (string, bool) {
	value, exists := values[key]
	if !exists {
		return "", false
	}
	if len(value) != 1 {
		return "\x00", true
	}
	return value[0], true
}

func writeList(writer http.ResponseWriter, page Page, message string) {
	writeJSON(writer, http.StatusOK, listResponse{
		Code: successCode, Message: message, Data: page.Items,
		Pagination: Pagination{Page: page.Page, Limit: page.Limit, Total: page.Total, TotalPages: page.TotalPages},
	})
}

func writeError(writer http.ResponseWriter, err error) {
	var typed *ServiceError
	if !errors.As(err, &typed) {
		typed = &ServiceError{Code: technicalCode, HTTPStatus: http.StatusInternalServerError}
	}
	if typed.HTTPStatus <= 0 {
		typed.HTTPStatus = http.StatusInternalServerError
	}
	message := "Game catalog is temporarily unavailable"
	switch typed.Code {
	case invalidCode:
		message = "Invalid game-list query parameters"
	case rateCode:
		message = "Rate limit exceeded"
	}
	if typed.HTTPStatus == http.StatusTooManyRequests {
		seconds := typed.RetryAfter
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeJSON(writer, typed.HTTPStatus, errorResponse{Code: typed.Code, Message: message, Data: nil})
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
