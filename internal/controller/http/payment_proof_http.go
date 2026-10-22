package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"time"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type PaymentProofHTTP struct {
	auth   *controller.AuthPresenter
	orders *controller.OrderController
}

func RegisterPaymentProofRoutes(mux *http.ServeMux, auth *controller.AuthPresenter, orders *controller.OrderController) {
	h := &PaymentProofHTTP{auth: auth, orders: orders}
	mux.HandleFunc("POST /api/orders/v1/payments/{paymentId}/proofs/upload", h.upload)
	mux.HandleFunc("POST /api/orders/v1/payments/{paymentId}/proofs", h.finalize)
	mux.HandleFunc("GET /api/admin/orders/v1/proofs/{proofId}/image", h.image)
}
func (h *PaymentProofHTTP) upload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !featureEnabled("FEATURE_SPRINT_2") {
		writeJSON(w, 404, "ORDT04004", "Fitur bukti pembayaran sedang dinonaktifkan.", nil)
		return
	}
	if !controller.ValidObjectID(r.PathValue("paymentId")) {
		writeJSON(w, 400, "ORDB02001", "Payment ID tidak valid.", nil)
		return
	}
	if !h.orders.ProofStorageConfigured() {
		writeJSON(w, 503, "ORDT04001", "Penyimpanan bukti pembayaran belum dikonfigurasi.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-proof", "") {
		return
	}
	var raw json.RawMessage
	if decodeBody(r, &raw) != nil {
		writeJSON(w, 400, "ORDB02001", "Data upload bukti tidak valid.", nil)
		return
	}
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			Pathname      string `json:"pathname"`
			ClientPayload string `json:"clientPayload"`
			Multipart     bool   `json:"multipart"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &event) != nil {
		writeJSON(w, 400, "ORDB02001", "Data upload bukti tidak valid.", nil)
		return
	}
	if event.Type == "blob.upload-completed" {
		if !h.orders.VerifyProofUploadCallback(raw, r.Header.Get("x-vercel-signature")) {
			writeJSON(w, 400, "ORDT03001", "Callback upload tidak valid.", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"blob.upload-completed","response":"ok"}`))
		return
	}
	if event.Type != "blob.generate-client-token" {
		writeJSON(w, 400, "ORDB02001", "Data upload bukti tidak valid.", nil)
		return
	}
	var payload any
	if json.Unmarshal([]byte(event.Payload.ClientPayload), &payload) != nil {
		writeJSON(w, 400, "ORDB02002", "Akses pesanan perlu diverifikasi.", nil)
		return
	}
	fields, ok := payload.(map[string]any)
	if !ok || fields == nil {
		writeJSON(w, 400, "ORDB02002", "Data upload bukti tidak valid.", nil)
		return
	}
	orderNumber, orderNumberOK := fields["orderNumber"].(string)
	_, trackingTokenOK := fields["trackingToken"].(string)
	if !orderNumberOK || !trackingTokenOK || !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(orderNumber) || !controller.ValidProofPath(r.PathValue("paymentId"), event.Payload.Pathname) {
		writeJSON(w, 400, "ORDB02002", "Data upload bukti tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-proof", r.PathValue("paymentId")) {
		return
	}
	token, err := h.orders.IssueProofUploadToken(r.Context(), r.PathValue("paymentId"), event.Payload.Pathname, event.Payload.ClientPayload, blobCallbackURL(r), time.Now().UTC().Add(5*time.Minute))
	if err != nil {
		e := controller.ToOrderError(err)
		writeJSON(w, 400, "ORDB02002", e.Message, nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(token)
}

func blobCallbackURL(r *http.Request) string {
	path := r.URL.RequestURI()
	if base := os.Getenv("VERCEL_BLOB_CALLBACK_URL"); base != "" {
		return base + path
	}
	if os.Getenv("VERCEL") != "1" {
		return ""
	}
	switch os.Getenv("VERCEL_ENV") {
	case "preview":
		if host := os.Getenv("VERCEL_BRANCH_URL"); host != "" {
			return "https://" + host + path
		}
		if host := os.Getenv("VERCEL_URL"); host != "" {
			return "https://" + host + path
		}
	case "production":
		if host := os.Getenv("VERCEL_PROJECT_PRODUCTION_URL"); host != "" {
			return "https://" + host + path
		}
	}
	return ""
}

func (h *PaymentProofHTTP) finalize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !featureEnabled("FEATURE_SPRINT_2") {
		writeJSON(w, 404, "ORDT04004", "Fitur bukti pembayaran sedang dinonaktifkan.", nil)
		return
	}
	if !controller.ValidObjectID(r.PathValue("paymentId")) {
		writeJSON(w, 400, "ORDB02001", "Payment ID tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-proof", "") {
		return
	}
	token := trackingBearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeJSON(w, 401, "ORDB02002", "Akses pesanan perlu diverifikasi.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-proof", r.PathValue("paymentId")) {
		return
	}
	var body map[string]json.RawMessage
	if decodeBody(r, &body) != nil {
		writeJSON(w, 400, "ORDB02001", "Data bukti pembayaran tidak valid.", nil)
		return
	}
	orderNumber, orderNumberOK := rawJSONString(body["orderNumber"])
	pathname, pathnameOK := rawJSONString(body["pathname"])
	validFields := len(body) == 2
	for key := range body {
		if key != "orderNumber" && key != "pathname" {
			validFields = false
		}
	}
	if !validFields || !orderNumberOK || !pathnameOK || !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(orderNumber) || !controller.ValidProofPath(r.PathValue("paymentId"), pathname) {
		writeJSON(w, 400, "ORDB02003", "Pilih gambar JPG atau PNG dengan ukuran maksimal 5 MB.", nil)
		return
	}
	if !h.orders.ProofStorageConfigured() {
		writeJSON(w, 503, "ORDT04001", "Penyimpanan bukti pembayaran belum dikonfigurasi.", nil)
		return
	}
	data, e := h.orders.FinalizeProof(r.Context(), r.PathValue("paymentId"), orderNumber, token, pathname)
	if e != nil {
		fail := controller.ToOrderError(e)
		writeJSON(w, fail.Status, fail.Code, fail.Message, nil)
		return
	}
	status := 201
	message := "Bukti diterima dan menunggu pemeriksaan admin."
	if replay, _ := data["idempotentReplay"].(bool); replay {
		status = 200
		message = "Bukti pembayaran sudah diterima."
	}
	delete(data, "idempotentReplay")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, "ORDS00000", message, data)
}
func (h *PaymentProofHTTP) image(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !featureEnabled("FEATURE_SPRINT_2") {
		writeJSON(w, 404, "ORDT04004", "Fitur bukti pembayaran sedang dinonaktifkan.", nil)
		return
	}
	claims, e := h.auth.VerifyAccessToken(r.Context(), authToken(r, adminAccessCookie), model.AdminAccountType)
	if e != nil || claims == nil {
		writeJSON(w, 401, "ORDB01001", "Sesi administrator tidak tersedia.", nil)
		return
	}
	if !controller.AdminPermissionAllowed(claims, "orders-manage") {
		writeJSON(w, 403, "ORDB01002", "Administrator tidak memiliki izin untuk tindakan ini.", nil)
		return
	}
	data, e := h.orders.ProofImage(r.Context(), r.PathValue("proofId"))
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `inline; filename="payment-proof.png"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
