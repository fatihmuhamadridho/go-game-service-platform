package operation

import (
	"encoding/json"
	"errors"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Handler translates HTTP requests into operation service calls.
type Handler struct {
	service    OperationService
	rateMu     sync.Mutex
	rateEvents map[string][]time.Time
}

func NewHandler(service OperationService) *Handler {
	return &Handler{service: service, rateEvents: map[string][]time.Time{}}
}

func (h *Handler) Register(mux *http.ServeMux, protect func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /api/settings/v1/public", h.publicSettings)
	mux.HandleFunc("GET /api/pages/v1/{slug}", h.publicPage)
	mux.Handle("/api/admin/", protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		profile, ok := auth.AdminProfileFromRequest(r)
		if !ok || !profile.Role.System || profile.Role.Code != "SUPER_ADMIN" {
			writeError(w, http.StatusForbidden, "Akses hanya tersedia untuk Super Admin sampai permission modul dikonfigurasi.")
			return
		}
		h.admin(w, r)
	})))
	mux.HandleFunc("/api/orders/", h.orders)
}

func (h *Handler) publicSettings(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, 503, "Penyimpanan operasi belum tersedia.")
		return
	}
	settings, err := h.service.GetSettings(r.Context())
	if err != nil {
		writeError(w, 503, "Pengaturan belum dapat dimuat.")
		return
	}
	write(w, 200, "OK", settings)
}

func (h *Handler) publicPage(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, 503, "Penyimpanan operasi belum tersedia.")
		return
	}
	slug := r.PathValue("slug")
	items, err := h.service.List(r.Context(), "pages")
	if err != nil {
		writeError(w, 503, "Halaman tidak dapat dimuat.")
		return
	}
	for _, item := range items {
		status, _ := item["status"].(string)
		if item["slug"] == slug && (strings.EqualFold(status, "PUBLISHED") || strings.EqualFold(status, "ACTIVE")) {
			write(w, 200, "Halaman dimuat.", map[string]any{"page": item})
			return
		}
	}
	writeError(w, 404, "Halaman tidak ditemukan.")
}
func (h *Handler) admin(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, 503, "Penyimpanan operasi belum tersedia.")
		return
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/admin/orders/v1/") {
		h.mongoAdminOrder(w, r)
		return
	}
	if path == "/api/admin/settings/v1/general" {
		if r.Method == http.MethodGet {
			settings, err := h.service.GetSettings(r.Context())
			if err != nil {
				writeError(w, 503, "Pengaturan tidak dapat dimuat.")
				return
			}
			write(w, 200, "OK", map[string]any{"settings": settings})
			return
		}
		if r.Method == http.MethodPut {
			body := map[string]any{}
			if !decode(w, r, &body) {
				writeError(w, 400, "Payload tidak valid.")
				return
			}
			if err := h.service.ValidateSettingsPatch(body); err != nil {
				writeError(w, 400, err.Error())
				return
			}
			settings, err := h.service.UpdateSettings(r.Context(), body)
			if err != nil {
				writeError(w, 503, "Pengaturan tidak dapat disimpan.")
				return
			}
			write(w, 200, "Pengaturan berhasil disimpan.", map[string]any{"settings": settings})
			return
		}
	}
	if path == "/api/admin/dashboard/v1/overview" && r.Method == http.MethodGet {
		data, err := h.service.Dashboard(r.Context(), r.URL.Query().Get("from"), r.URL.Query().Get("to"), r.URL.Query().Get("granularity"))
		if err != nil {
			writeError(w, 503, "Ringkasan dashboard tidak dapat dimuat.")
			return
		}
		write(w, 200, "OK", data)
		return
	}
	for _, route := range []struct{ path, bucket, key string }{
		{"activity-logs", "activityLogs", "activityLogs"}, {"transactions", "transactions", "transactions"}, {"users", "users", "users"}, {"members", "users", "users"}, {"banners", "banners", "banners"}, {"bank-accounts", "bankAccounts", "bankAccounts"}, {"pages", "pages", "pages"},
	} {
		prefix := "/api/admin/" + route.path + "/v1/"
		if strings.HasPrefix(path, prefix) {
			h.collection(w, r, route.bucket, route.key, strings.TrimPrefix(path, prefix))
			return
		}
	}
	if strings.HasPrefix(path, "/api/admin/admin-users/v1/") && r.Method != http.MethodGet {
		writeError(w, 501, "Mutasi akun admin belum tersedia. Penetapan kredensial awal harus dikonfirmasi.")
		return
	}
	writeError(w, 404, "Endpoint tidak ditemukan.")
}
func (h *Handler) collection(w http.ResponseWriter, r *http.Request, bucket, resource, action string) {
	if h.service == nil {
		writeError(w, 503, "Penyimpanan operasi belum tersedia.")
		return
	}
	h.mongoCollection(w, r, bucket, resource, action)
}
func (h *Handler) mongoCollection(w http.ResponseWriter, r *http.Request, bucket, resource, action string) {
	parts := strings.Split(strings.Trim(action, "/"), "/")
	if len(parts) == 0 {
		writeError(w, 404, "Endpoint tidak ditemukan.")
		return
	}
	op := parts[0]
	id := ""
	if len(parts) > 1 {
		id = parts[1]
	}
	if op == "list" && r.Method == http.MethodGet {
		data, err := h.service.ListCollectionPage(r.Context(), bucket, r.URL.Query().Get("search"), r.URL.Query().Get("status"), r.URL.Query().Get("method"), r.URL.Query().Get("category"), intQuery(r, "page", 1), intQuery(r, "pageSize", 20))
		if err != nil {
			writeError(w, 503, "Data tidak dapat dimuat.")
			return
		}
		data[resource] = data["items"]
		delete(data, "items")
		write(w, 200, "OK", data)
		return
	}
	if op == "detail" && r.Method == http.MethodGet && id != "" {
		if bucket == "transactions" {
			transactions, err := h.service.ListTransactions(r.Context())
			if err != nil {
				writeError(w, 503, "Transaksi tidak dapat dimuat.")
				return
			}
			for _, transaction := range transactions {
				if transaction["internalId"] == id || transaction["id"] == id {
					write(w, 200, "OK", map[string]any{"transaction": transaction})
					return
				}
			}
			writeError(w, 404, "Data tidak ditemukan.")
			return
		}
		item, err := h.service.Get(r.Context(), bucket, id)
		if err != nil {
			writeError(w, 503, "Data tidak dapat dimuat.")
			return
		}
		if item == nil {
			writeError(w, 404, "Data tidak ditemukan.")
			return
		}
		write(w, 200, "OK", map[string]any{singular(resource): item})
		return
	}
	if op == "create" && r.Method == http.MethodPost {
		body := map[string]any{}
		if !decode(w, r, &body) {
			writeError(w, 400, "Payload tidak valid.")
			return
		}
		created, err := h.service.CreateRecord(r.Context(), bucket, body)
		if err != nil {
			if errors.Is(err, ErrInvalidRecord) || bucket == "bankAccounts" {
				writeError(w, 400, err.Error())
				return
			}
			writeError(w, 503, "Data tidak dapat disimpan.")
			return
		}
		write(w, 201, "Data berhasil dibuat.", map[string]any{singular(resource): created})
		return
	}
	if op == "set-primary" && r.Method == http.MethodPost && bucket == "bankAccounts" && id != "" {
		if err := h.service.SetPrimaryBankAccount(r.Context(), id); err != nil {
			if errors.Is(err, ErrRecordNotFound) {
				writeError(w, 404, "Rekening tidak ditemukan.")
				return
			}
			writeError(w, 503, "Rekening utama tidak dapat disimpan.")
			return
		}
		item, _ := h.service.Get(r.Context(), bucket, id)
		write(w, 200, "Rekening utama diperbarui.", map[string]any{"bankAccount": item})
		return
	}
	if (op == "update" || op == "delete") && id != "" {
		if op == "delete" && r.Method == http.MethodDelete {
			if err := h.service.DeleteRecord(r.Context(), bucket, id); err != nil {
				writeError(w, 503, "Data tidak dapat dihapus.")
				return
			}
			write(w, 200, "Data berhasil dihapus.", map[string]any{"deleted": true})
			return
		}
		if op == "update" && (r.Method == http.MethodPatch || r.Method == http.MethodPut) {
			body := map[string]any{}
			if !decode(w, r, &body) {
				writeError(w, 400, "Payload tidak valid.")
				return
			}
			updated, err := h.service.UpdateRecord(r.Context(), bucket, id, body)
			if err != nil {
				if errors.Is(err, ErrRecordNotFound) {
					writeError(w, 404, "Data tidak ditemukan.")
					return
				}
				if errors.Is(err, ErrInvalidRecord) || bucket == "bankAccounts" {
					writeError(w, 400, err.Error())
					return
				}
				writeError(w, 503, "Data tidak dapat disimpan.")
				return
			}
			write(w, 200, "Data berhasil diperbarui.", map[string]any{singular(resource): updated})
			return
		}
	}
	writeError(w, 404, "Data atau endpoint tidak ditemukan.")
}

func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, 503, "Penyimpanan operasi belum tersedia.")
		return
	}
	h.mongoOrders(w, r)
}
func (h *Handler) allowPublicRequest(w http.ResponseWriter, r *http.Request, route string, limit int) bool {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	key := route + ":" + peer
	now := time.Now().UTC()
	cutoff := now.Add(-time.Minute)
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	for current, events := range h.rateEvents {
		kept := events[:0]
		for _, event := range events {
			if event.After(cutoff) {
				kept = append(kept, event)
			}
		}
		if len(kept) == 0 {
			delete(h.rateEvents, current)
		} else {
			h.rateEvents[current] = kept
		}
	}
	if len(h.rateEvents) >= 4096 {
		if _, exists := h.rateEvents[key]; !exists {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "Batas request tercapai.")
			return false
		}
	}
	events := h.rateEvents[key]
	if len(events) >= limit {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "Batas request tercapai.")
		return false
	}
	h.rateEvents[key] = append(events, now)
	return true
}

func singular(v string) string {
	if strings.HasSuffix(v, "ies") {
		return strings.TrimSuffix(v, "ies") + "y"
	}
	if strings.HasSuffix(v, "s") {
		return strings.TrimSuffix(v, "s")
	}
	return v
}
func intQuery(r *http.Request, key string, fallback int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(key))
	if e != nil || v < 1 {
		return fallback
	}
	if v > 100 {
		return 100
	}
	return v
}

func maskPhone(phone string) string {
	if len(phone) < 5 {
		return "***"
	}
	return phone[:3] + strings.Repeat("*", len(phone)-5) + phone[len(phone)-2:]
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(dst) == nil
}
func clone(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	return out
}
func write(w http.ResponseWriter, status int, message string, data any) {
	w.Header().Set("Cache-Control", "no-store")
	utils.WriteJSON(w, status, utils.ResponseEnvelope{Code: "OPR00000", Message: message, Data: data})
}
func writeError(w http.ResponseWriter, status int, message string) { write(w, status, message, nil) }
