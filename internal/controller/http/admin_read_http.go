package controllers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminReadHTTP struct {
	auth *controller.AuthPresenter
	read *controller.AdminReadController
}

func RegisterAdminReadRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, read *controller.AdminReadController) {
	h := &AdminReadHTTP{auth: auth, read: read}
	mux.HandleFunc("GET /api/admin/transactions/v1/list", h.listTransactions)
	mux.HandleFunc("GET /api/admin/transactions/v1/detail/{transactionId}", h.getTransaction)
	mux.HandleFunc("GET /api/admin/activity-logs/v1/list", h.listActivity)
	mux.HandleFunc("GET /api/admin/settings/v1/general", h.getSettings)
	mux.HandleFunc("PUT /api/admin/settings/v1/general", h.updateSettings)
	mux.HandleFunc("GET /api/admin/dashboard/v1/overview", h.dashboard)
}
func (h *AdminReadHTTP) authorize(w http.ResponseWriter, r *http.Request, code, permission string) *controller.VerifiedToken {
	claims, e := h.auth.VerifyAccessToken(r.Context(), authToken(r, adminAccessCookie), model.AdminAccountType)
	if e != nil || claims == nil {
		writeJSON(w, 401, code, "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if !controller.AdminPermissionAllowed(claims, permission) {
		writeJSON(w, 403, strings.Replace(code, "01001", "01002", 1), "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}
func (h *AdminReadHTTP) fail(w http.ResponseWriter, e error, prefix string) {
	x := controller.ToAdminReadError(e, prefix)
	writeJSON(w, x.Status, x.Code, x.Message, nil)
}
func (h *AdminReadHTTP) listTransactions(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "TRXB01001", "finance-read") == nil {
		return
	}
	q := r.URL.Query()
	page, e := parseOptionalPositive(q, "page", 1, 0)
	if e != nil {
		writeJSON(w, 400, "TRNB02001", "Filter transaksi tidak valid.", nil)
		return
	}
	size, e := parseOptionalPositive(q, "pageSize", 20, 100)
	if e != nil {
		writeJSON(w, 400, "TRNB02001", "Filter transaksi tidak valid.", nil)
		return
	}
	search, status, method, typ := q.Get("search"), q.Get("status"), q.Get("method"), q.Get("type")
	for k := range q {
		if !containsKey([]string{"page", "pageSize", "search", "status", "method", "type"}, k) {
			writeJSON(w, 400, "TRNB02001", "Filter transaksi tidak valid.", nil)
			return
		}
	}
	if controller.JSStringLength(search) > 100 || controller.JSStringLength(status) > 32 || controller.JSStringLength(method) > 40 || controller.JSStringLength(typ) > 24 {
		writeJSON(w, 400, "TRNB02001", "Filter transaksi tidak valid.", nil)
		return
	}
	data, e := h.read.ListTransactions(r.Context(), search, status, method, typ, page, size)
	if e != nil {
		h.fail(w, e, "TRX")
		return
	}
	writeJSON(w, 200, "TRNS00000", "Daftar transaksi berhasil diambil.", data)
}
func (h *AdminReadHTTP) getTransaction(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "TRXB01001", "finance-read") == nil {
		return
	}
	data, e := h.read.GetTransaction(r.Context(), r.PathValue("transactionId"))
	if e != nil {
		h.fail(w, e, "TRX")
		return
	}
	writeJSON(w, 200, "TRNS00000", "Detail transaksi berhasil diambil.", map[string]any{"transaction": data})
}
func (h *AdminReadHTTP) listActivity(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "ACTB01001", "")
	if claims == nil {
		return
	}
	if !controller.AdminPermissionAllowed(claims, "dashboard-read") && !controller.AdminPermissionAllowed(claims, "settings-manage") {
		writeJSON(w, 403, "ACTB01002", "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return
	}
	q := r.URL.Query()
	page, e := parseOptionalPositive(q, "page", 1, 0)
	if e != nil {
		writeJSON(w, 400, "ACTB02001", "Filter aktivitas tidak valid.", nil)
		return
	}
	size, e := parseOptionalPositive(q, "pageSize", 50, 100)
	if e != nil {
		writeJSON(w, 400, "ACTB02001", "Filter aktivitas tidak valid.", nil)
		return
	}
	search, category, from, to := q.Get("search"), q.Get("category"), q.Get("from"), q.Get("to")
	for k := range q {
		if !containsKey([]string{"page", "pageSize", "search", "category", "from", "to"}, k) {
			writeJSON(w, 400, "ACTB02001", "Filter aktivitas tidak valid.", nil)
			return
		}
	}
	date := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	if controller.JSStringLength(search) > 100 || controller.JSStringLength(category) > 32 || (from != "" && !date.MatchString(from)) || (to != "" && !date.MatchString(to)) || (from != "" && to != "" && from > to) {
		writeJSON(w, 400, "ACTB02001", "Filter aktivitas tidak valid.", nil)
		return
	}
	data, e := h.read.ListActivity(r.Context(), search, category, from, to, page, size)
	if e != nil {
		h.fail(w, e, "ACT")
		return
	}
	writeJSON(w, 200, "ACTS00000", "Log aktivitas berhasil diambil.", data)
}
func (h *AdminReadHTTP) getSettings(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "SETB01001", "settings-manage") == nil {
		return
	}
	data, e := h.read.GetSettings(r.Context())
	if e != nil {
		h.fail(w, e, "SET")
		return
	}
	writeJSON(w, 200, "SETS00000", "Pengaturan berhasil diambil.", data)
}
func (h *AdminReadHTTP) updateSettings(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r, "SETB01001", "settings-manage")
	if claims == nil {
		return
	}
	var payload map[string]json.RawMessage
	if decodeBody(r, &payload) != nil || !validSettingsPayload(payload) {
		writeJSON(w, 400, "SETB02001", "Pengaturan tidak valid.", nil)
		return
	}
	var in controller.SettingsInput
	if err := json.Unmarshal(mustJSON(payload), &in); err != nil {
		writeJSON(w, 400, "SETB02001", "Pengaturan tidak valid.", nil)
		return
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]
	data, e := h.read.UpdateSettings(r.Context(), in, claims.Subject, model.SettingsActivityContext{RequestID: r.Header.Get("X-Request-ID"), SourceIP: strings.TrimSpace(forwarded), UserAgent: r.UserAgent()})
	if e != nil {
		h.fail(w, e, "SET")
		return
	}
	writeJSON(w, 200, "SETS00000", "Pengaturan berhasil disimpan.", data)
}

func (h *AdminReadHTTP) dashboard(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r, "DASHB01001", "dashboard-read") == nil {
		return
	}
	q := r.URL.Query()
	for k := range q {
		if k != "from" && k != "to" && k != "granularity" {
			writeJSON(w, 400, "DASHB02001", "Rentang dashboard tidak valid.", nil)
			return
		}
	}
	gran := q.Get("granularity")
	if gran == "" {
		gran = "daily"
	}
	from, to := q.Get("from"), q.Get("to")
	fromDate, validFrom := parseDashboardDate(from)
	toDate, validTo := parseDashboardDate(to)
	if !containsKey([]string{"daily", "weekly", "monthly"}, gran) || !validFrom || !validTo || (from != "" && to != "" && from > to) {
		writeJSON(w, 400, "DASHB02001", "Rentang dashboard tidak valid.", nil)
		return
	}
	now := time.Now().UTC()
	end := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999000000, time.UTC)
	if to != "" {
		end = toDate
		end = end.Add(24*time.Hour - time.Millisecond)
	}
	start := end.Add(-6 * 24 * time.Hour)
	if from != "" {
		start = fromDate
	}
	if from != "" && to != "" {
		if toDate.Sub(fromDate) > 366*24*time.Hour {
			writeJSON(w, 400, "DASHB02001", "Rentang dashboard tidak valid.", nil)
			return
		}
	}
	data, e := h.read.Dashboard(r.Context(), start, end, gran)
	if e != nil {
		h.fail(w, e, "DSH")
		return
	}
	writeJSON(w, 200, "DASH00000", "Ringkasan dashboard berhasil diambil.", map[string]any{"overview": data})
}

func validSettingsPayload(payload map[string]json.RawMessage) bool {
	keys := []string{"siteName", "siteDescription", "contactEmail", "contactWhatsapp", "socialLinks", "businessHours", "maintenanceMode", "allowRegistration", "defaultCurrency", "timezone"}
	if len(payload) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := payload[key]; !ok {
			return false
		}
	}
	var in controller.SettingsInput
	raw, err := json.Marshal(payload)
	if err != nil || json.Unmarshal(raw, &in) != nil {
		return false
	}
	var types map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&types) != nil {
		return false
	}
	for _, key := range []string{"siteName", "contactEmail", "contactWhatsapp", "defaultCurrency", "timezone"} {
		if _, ok := types[key].(string); !ok {
			return false
		}
	}
	for _, key := range []string{"maintenanceMode", "allowRegistration"} {
		if _, ok := types[key].(bool); !ok {
			return false
		}
	}
	for _, key := range []string{"siteDescription", "businessHours"} {
		if v, ok := types[key]; !ok || (v != nil && func() bool { _, ok := v.(string); return !ok }()) {
			return false
		}
	}
	if _, ok := types["socialLinks"].([]any); !ok {
		return false
	}
	return true
}

func parseDashboardDate(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	match := regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`).FindStringSubmatch(value)
	if len(match) != 4 {
		return time.Time{}, false
	}
	year, yearErr := strconv.Atoi(match[1])
	month, monthErr := strconv.Atoi(match[2])
	day, dayErr := strconv.Atoi(match[3])
	if yearErr != nil || monthErr != nil || dayErr != nil || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}

func validISODate(value string) bool {
	parsed, ok := parseDashboardDate(value)
	if !ok {
		return false
	}
	return value == "" || parsed.Format("2006-01-02") == value
}

func mustJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
func containsKey(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

var _ = json.Valid
var _ = time.UTC
