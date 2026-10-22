package controllers

import (
	"encoding/json"
	"net/http"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type AdminOrderHTTP struct {
	auth   *controller.AuthPresenter
	orders *controller.AdminOrderController
}

func RegisterAdminOrderRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, orders *controller.AdminOrderController) {
	h := &AdminOrderHTTP{auth: auth, orders: orders}
	mux.HandleFunc("GET /api/admin/orders/v1/list", h.list)
	mux.HandleFunc("GET /api/admin/orders/v1/detail/{orderId}", h.detail)
	mux.HandleFunc("POST /api/admin/orders/v1/payment-review", h.review)
	mux.HandleFunc("POST /api/admin/orders/v1/complete/{orderId}", h.complete)
}
func (h *AdminOrderHTTP) authorize(w http.ResponseWriter, r *http.Request) *controller.VerifiedToken {
	claims, e := h.auth.VerifyAccessToken(r.Context(), authToken(r, adminAccessCookie), model.AdminAccountType)
	if e != nil || claims == nil {
		writeJSON(w, 401, "ORDB01001", "Sesi administrator tidak tersedia.", nil)
		return nil
	}
	if !controller.AdminPermissionAllowed(claims, "orders-manage") {
		writeJSON(w, 403, "ORDB01002", "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return nil
	}
	return claims
}
func (h *AdminOrderHTTP) fail(w http.ResponseWriter, e error) {
	x := controller.ToAdminOrderError(e)
	writeJSON(w, x.Status, x.Code, x.Message, nil)
}
func (h *AdminOrderHTTP) list(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	q := r.URL.Query()
	for k := range q {
		if !containsKey([]string{"page", "pageSize", "search", "status"}, k) {
			writeJSON(w, 400, "ORDB02001", "Filter pesanan tidak valid.", nil)
			return
		}
	}
	page, size := 1, 20
	if n, e := parseOptionalPositive(q, "page", 1, 0); e != nil {
		writeJSON(w, 400, "ORDB02001", "Filter pesanan tidak valid.", nil)
		return
	} else {
		page = n
	}
	if n, e := parseOptionalPositive(q, "pageSize", 20, 100); e != nil {
		writeJSON(w, 400, "ORDB02001", "Filter pesanan tidak valid.", nil)
		return
	} else {
		size = n
	}
	search, status := q.Get("search"), q.Get("status")
	if controller.JSStringLength(search) > 100 || controller.JSStringLength(status) > 40 {
		writeJSON(w, 400, "ORDB02001", "Filter pesanan tidak valid.", nil)
		return
	}
	data, e := h.orders.List(r.Context(), search, status, page, size)
	if e != nil {
		h.fail(w, e)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Daftar pesanan berhasil diambil.", data)
}
func (h *AdminOrderHTTP) detail(w http.ResponseWriter, r *http.Request) {
	if h.authorize(w, r) == nil {
		return
	}
	data, e := h.orders.Get(r.Context(), r.PathValue("orderId"))
	if e != nil {
		h.fail(w, e)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Detail pesanan berhasil diambil.", map[string]any{"order": data})
}
func (h *AdminOrderHTTP) review(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	var raw map[string]json.RawMessage
	if decodeBody(r, &raw) != nil || raw == nil || !validPaymentReviewPayload(raw) {
		writeJSON(w, 400, "ORDB02001", "Data pemeriksaan tidak valid.", nil)
		return
	}
	var b struct {
		OrderID   string
		PaymentID string
		Decision  string
		Note      *string
	}
	_ = json.Unmarshal(raw["orderId"], &b.OrderID)
	_ = json.Unmarshal(raw["paymentId"], &b.PaymentID)
	_ = json.Unmarshal(raw["decision"], &b.Decision)
	if value, ok := raw["note"]; ok && string(value) != "null" {
		var note string
		_ = json.Unmarshal(value, &note)
		b.Note = &note
	}
	if b.Note != nil {
		n := controller.JSTrim(*b.Note)
		if n == "" {
			b.Note = nil
		} else {
			b.Note = &n
		}
	}
	data, e := h.orders.Review(r.Context(), b.OrderID, b.PaymentID, b.Decision, b.Note, claims.Subject)
	if e != nil {
		h.fail(w, e)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Pemeriksaan pembayaran berhasil disimpan.", data)
}

func validPaymentReviewPayload(raw map[string]json.RawMessage) bool {
	for key := range raw {
		if !containsKey([]string{"orderId", "paymentId", "decision", "note"}, key) {
			return false
		}
	}
	for _, key := range []string{"orderId", "paymentId", "decision"} {
		value, ok := raw[key]
		if !ok || len(value) == 0 || value[0] != '"' {
			return false
		}
	}
	var decision string
	if json.Unmarshal(raw["decision"], &decision) != nil || (decision != "APPROVE" && decision != "REJECT") {
		return false
	}
	if value, ok := raw["note"]; ok && string(value) != "null" {
		var note string
		if json.Unmarshal(value, &note) != nil || controller.JSStringLength(controller.JSTrim(note)) > 500 {
			return false
		}
	}
	return true
}
func (h *AdminOrderHTTP) complete(w http.ResponseWriter, r *http.Request) {
	claims := h.authorize(w, r)
	if claims == nil {
		return
	}
	data, e := h.orders.Complete(r.Context(), r.PathValue("orderId"), claims.Subject)
	if e != nil {
		h.fail(w, e)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Pesanan ditandai selesai.", data)
}
