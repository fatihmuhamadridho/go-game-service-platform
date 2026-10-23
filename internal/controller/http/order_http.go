package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"example.com/learn-golang/internal/controller"
	"example.com/learn-golang/internal/model"
)

type OrderHTTP struct{ orders *controller.OrderController }

func RegisterOrderRoutes(mux *http.ServeMux, orders *controller.OrderController) {
	h := &OrderHTTP{orders: orders}
	mux.HandleFunc("POST /api/orders/v1/checkout", h.checkout)
	mux.HandleFunc("POST /api/orders/v1/payments/{paymentId}/report-paid", h.reportPayment)
	mux.HandleFunc("POST /api/orders/v1/tracking/lookup", h.lookupTracking)
	mux.HandleFunc("GET /api/orders/v1/tracking/{orderNumber}/status", h.trackingStatus)
	mux.HandleFunc("GET /api/orders/v1/tracking/{orderNumber}/payment", h.trackingPayment)
	mux.HandleFunc("POST /api/orders/v1/tracking/{orderNumber}/payment", h.trackingReportPayment)
	mux.HandleFunc("GET /api/orders/v1/payments/{paymentId}/status", h.paymentStatus)
}

type checkoutBody struct {
	GameSlug, ServiceID, PaymentMethod string
	Quantity                           int32
	CustomerName, WhatsApp             string
	GameAccount                        string
	GameServer, CustomerNotes          *string
}

func checkoutText(fields map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil {
		return ""
	}
	return strings.TrimFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

func checkoutObject(raw json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	return fields
}

func checkoutOptionalText(fields map[string]json.RawMessage, key string) *string {
	raw, exists := fields[key]
	if !exists || string(raw) == "null" {
		return nil
	}
	value := checkoutText(fields, key)
	if value == "" {
		return nil
	}
	return &value
}

func decodeCheckoutBody(r *http.Request) (checkoutBody, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	var fields map[string]json.RawMessage
	if decoder.Decode(&fields) != nil || fields == nil {
		return checkoutBody{}, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return checkoutBody{}, false
	}
	allowed := map[string]bool{"gameSlug": true, "serviceId": true, "quantity": true, "paymentMethod": true, "customer": true, "item": true}
	for key := range fields {
		if !allowed[key] {
			return checkoutBody{}, false
		}
	}
	customer, item := checkoutObject(fields["customer"]), checkoutObject(fields["item"])
	var quantity int32
	var number json.Number
	if json.Unmarshal(fields["quantity"], &number) == nil {
		if parsed, err := strconv.ParseInt(number.String(), 10, 32); err == nil {
			quantity = int32(parsed)
		} else if parsedFloat, err := strconv.ParseFloat(number.String(), 64); err == nil && parsedFloat == float64(int32(parsedFloat)) && parsedFloat >= -2147483648 && parsedFloat <= 2147483647 {
			quantity = int32(parsedFloat)
		}
	}
	return checkoutBody{
		GameSlug: checkoutText(fields, "gameSlug"), ServiceID: checkoutText(fields, "serviceId"),
		PaymentMethod: checkoutText(fields, "paymentMethod"), Quantity: quantity,
		CustomerName: checkoutText(customer, "name"), WhatsApp: checkoutText(customer, "whatsapp"),
		GameAccount: checkoutText(item, "gameAccount"), GameServer: checkoutOptionalText(item, "gameServer"),
		CustomerNotes: checkoutOptionalText(item, "customerNotes"),
	}, true
}

func (h *OrderHTTP) checkout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if enforceRateLimit(w, r, "checkout", "") {
		return
	}
	body, validBody := decodeCheckoutBody(r)
	if !validBody {
		writeJSON(w, 400, "ORDB02001", "Data pesanan tidak valid.", nil)
		return
	}
	key := strings.TrimFunc(r.Header.Get("Idempotency-Key"), func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
	gameSlug := strings.ToLower(strings.TrimSpace(body.GameSlug))
	serviceID := strings.TrimSpace(body.ServiceID)
	paymentMethod := strings.TrimSpace(body.PaymentMethod)
	phone := normalizeCheckoutPhone(body.WhatsApp)
	gameServer := body.GameServer
	customerNotes := body.CustomerNotes
	if !regexp.MustCompile(`^[A-Za-z0-9._:-]{8,200}$`).MatchString(key) || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(gameSlug) || controller.JSStringLength(serviceID) == 0 || controller.JSStringLength(serviceID) > 64 || body.Quantity < 1 || body.Quantity > 100 || (paymentMethod != "QRIS" && paymentMethod != "BCA_TRANSFER") || body.CustomerName == "" || controller.JSStringLength(body.CustomerName) > 150 || !regexp.MustCompile(`^\+[1-9]\d{7,14}$`).MatchString(phone) || body.GameAccount == "" || controller.JSStringLength(body.GameAccount) > 100 || (gameServer != nil && controller.JSStringLength(*gameServer) > 100) || (customerNotes != nil && controller.JSStringLength(*customerNotes) > 1000) || containsSensitiveOrderFields(body.GameAccount, customerNotes) {
		writeJSON(w, 400, "ORDB02001", "Periksa kembali detail pesanan dan data kontak.", nil)
		return
	}
	if enforceRateLimit(w, r, "checkout", phone) {
		return
	}
	result, err := h.orders.Checkout(r.Context(), controller.CheckoutInput{GameSlug: gameSlug, ServiceID: serviceID, Quantity: body.Quantity, PaymentMethod: paymentMethod, CustomerName: body.CustomerName, WhatsApp: phone, GameAccount: body.GameAccount, GameServer: gameServer, CustomerNotes: customerNotes, IdempotencyKey: key}, featureEnabled("FEATURE_SPRINT_2"))
	if err != nil {
		failure := controller.ToOrderError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	orderID, ok := result.Order["id"].(string)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Layanan checkout sedang tidak tersedia.", nil)
		return
	}
	id, parseErr := controller.ParseObjectID(orderID)
	if parseErr != nil {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Layanan checkout sedang tidak tersedia.", nil)
		return
	}
	token, expires, tokenErr := h.orders.IssueTrackingToken(r.Context(), id)
	if tokenErr != nil {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Layanan checkout sedang tidak tersedia.", nil)
		return
	}
	result.TrackingToken = token
	result.TrackingTokenExpiresAt = expires.Format(time.RFC3339Nano)
	status := http.StatusCreated
	if result.IdempotentReplay {
		status = http.StatusOK
	}
	writeJSON(w, status, "ORDS00000", "Pesanan berhasil dibuat.", result)
}

type reportPaidBody struct {
	OrderNumber string `json:"orderNumber"`
	WhatsApp    string `json:"whatsapp"`
}

func (h *OrderHTTP) reportPayment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := controller.ParseObjectID(r.PathValue("paymentId"))
	if err != nil {
		writeJSON(w, 400, "ORDB02001", "Payment ID tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-status", "") {
		return
	}
	var body reportPaidBody
	if decodeBody(r, &body) != nil || !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(body.OrderNumber) || controller.JSStringLength(body.WhatsApp) < 8 || controller.JSStringLength(body.WhatsApp) > 32 {
		writeJSON(w, 400, "ORDB02001", "Data pembayaran tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-status", strings.Join([]string{r.PathValue("paymentId"), body.OrderNumber, normalizeSubmittedPhone(body.WhatsApp)}, ":")) {
		return
	}
	result, err := h.orders.ReportPayment(r.Context(), id, body.OrderNumber, normalizeSubmittedPhone(body.WhatsApp), featureEnabled("FEATURE_SPRINT_2"))
	if err != nil {
		failure := controller.ToOrderError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Konfirmasi pembayaran terkirim untuk validasi admin.", result)
}

type trackingLookupBody struct {
	OrderNumber string `json:"orderNumber"`
	WhatsApp    string `json:"whatsapp"`
}

func (h *OrderHTTP) lookupTracking(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if enforceRateLimit(w, r, "order-tracking", "") {
		return
	}
	if r.ContentLength > 4096 {
		writeJSON(w, 400, "ORDB02001", "Data pelacakan tidak valid.", nil)
		return
	}
	var body trackingLookupBody
	if decodeBody(r, &body) != nil {
		writeJSON(w, 400, "ORDB02001", "Data pelacakan tidak valid.", nil)
		return
	}
	body.OrderNumber = strings.ToUpper(strings.TrimSpace(body.OrderNumber))
	if !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(body.OrderNumber) {
		writeJSON(w, 400, "ORDB02001", "Data pelacakan tidak valid.", nil)
		return
	}
	rawPhone := strings.TrimSpace(body.WhatsApp)
	if controller.JSStringLength(rawPhone) < 8 || controller.JSStringLength(rawPhone) > 32 || !validTrackingPhoneCharacters(rawPhone) || !regexp.MustCompile(`^\+[1-9]\d{7,14}$`).MatchString(normalizeTrackingPhone(rawPhone)) {
		writeJSON(w, 400, "ORDB02001", "Data pelacakan tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "order-tracking", body.OrderNumber+":"+normalizeTrackingPhone(rawPhone)) {
		return
	}
	result, err := h.orders.LookupTracking(r.Context(), body.OrderNumber, body.WhatsApp)
	if err != nil {
		failure := controller.ToOrderError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, "ORDS00000", "Status pesanan berhasil diambil.", result)
}
func (h *OrderHTTP) trackingStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	number := strings.ToUpper(r.PathValue("orderNumber"))
	query := r.URL.Query()
	resultView := query.Get("view") == "result"
	if len(query) > 1 || (len(query) == 1 && !resultView) {
		writeJSON(w, 400, "ORDB02002", "Pesanan tidak dapat diverifikasi. Silakan cek pesanan kembali.", nil)
		return
	}
	token := trackingBearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeJSON(w, 400, "ORDB02002", "Pesanan tidak dapat diverifikasi. Silakan cek pesanan kembali.", nil)
		return
	}
	if enforceRateLimit(w, r, "order-tracking", "") || enforceRateLimit(w, r, "order-tracking", token) {
		return
	}
	result, err := h.orders.TrackingByToken(r.Context(), token, number, resultView)
	if err != nil {
		failure := controller.ToOrderError(err)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Status pesanan berhasil diambil.", result)
}

func (h *OrderHTTP) trackingPayment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	number := strings.ToUpper(r.PathValue("orderNumber"))
	if !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(number) {
		writeJSON(w, 400, "ORDB02001", "Nomor pesanan tidak valid.", nil)
		return
	}
	token := trackingBearerToken(r.Header.Get("Authorization"))
	if enforceRateLimit(w, r, "order-tracking", "") || enforceRateLimit(w, r, "order-tracking", token) {
		return
	}
	scope, e := h.orders.AuthorizedOrderPayment(r.Context(), token, number)
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	payment, ok := scope["payment"].(model.OrderPayment)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Detail pembayaran sedang tidak tersedia.", nil)
		return
	}
	orderValue, ok := scope["order"].(model.Order)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Detail pembayaran sedang tidak tersedia.", nil)
		return
	}
	order := &orderValue
	item, ok := scope["item"].(model.OrderItem)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Detail pembayaran sedang tidak tersedia.", nil)
		return
	}
	instructions, e := h.orders.PaymentInstructions(payment.Method)
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	data := map[string]any{"order": map[string]any{"orderNumber": order.OrderNumber, "orderStatus": order.OrderStatus, "paymentStatus": payment.Status, "totalAmount": decimalAmount(order.TotalAmount), "expiresAt": order.ExpiresAt, "game": item.GameNameSnapshot, "service": item.ServiceNameSnapshot, "quantity": item.Quantity}, "payment": map[string]any{"id": payment.ID.Hex(), "method": payment.Method, "amount": decimalAmount(payment.Amount), "instructions": map[string]any{"qrImageUrl": instructions["qrImageUrl"], "bankName": instructions["bankName"], "accountNumber": instructions["accountNumber"], "accountHolder": instructions["accountHolder"], "reference": payment.ReferenceNote}}}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, "ORDS00000", "Detail pembayaran berhasil diambil.", data)
}
func (h *OrderHTTP) trackingReportPayment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	number := strings.ToUpper(r.PathValue("orderNumber"))
	if !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(number) {
		writeJSON(w, 400, "ORDB02001", "Nomor pesanan tidak valid.", nil)
		return
	}
	token := trackingBearerToken(r.Header.Get("Authorization"))
	if enforceRateLimit(w, r, "order-tracking", "") || enforceRateLimit(w, r, "order-tracking", token) {
		return
	}
	scope, e := h.orders.AuthorizedOrderPayment(r.Context(), token, number)
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	order, ok := scope["order"].(model.Order)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, "ORDT03001", "Konfirmasi pembayaran tidak dapat diproses.", nil)
		return
	}
	payment := scope["payment"].(model.OrderPayment)
	data, e := h.orders.ReportPayment(r.Context(), payment.ID, number, order.CustomerWhatsApp, featureEnabled("FEATURE_SPRINT_2"))
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, 200, "ORDS00000", "Konfirmasi pembayaran berhasil dikirim.", data)
}
func (h *OrderHTTP) paymentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, e := controller.ParseObjectID(r.PathValue("paymentId"))
	if e != nil {
		writeJSON(w, 400, "ORDB02001", "Payment ID tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-status", "") {
		return
	}
	q := r.URL.Query()
	for k := range q {
		if k != "orderNumber" && k != "whatsapp" {
			writeJSON(w, 400, "ORDB02001", "Data verifikasi pembayaran tidak valid.", nil)
			return
		}
	}
	number, phone := controller.JSTrim(q.Get("orderNumber")), controller.JSTrim(q.Get("whatsapp"))
	if !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(number) || controller.JSStringLength(phone) < 8 || controller.JSStringLength(phone) > 32 {
		writeJSON(w, 400, "ORDB02001", "Data verifikasi pembayaran tidak valid.", nil)
		return
	}
	if enforceRateLimit(w, r, "payment-status", strings.Join([]string{r.PathValue("paymentId"), number, phone}, ":")) {
		return
	}
	data, e := h.orders.PaymentStatus(r.Context(), id, number, phone)
	if e != nil {
		failure := controller.ToOrderError(e)
		writeJSON(w, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	writeJSON(w, 200, "ORDS00000", "Status pembayaran berhasil diambil.", data)
}
func decimalAmount(v interface{ String() string }) float64 {
	x, _ := strconv.ParseFloat(v.String(), 64)
	return x
}

func normalizeCheckoutPhone(value string) string {
	value = strings.TrimSpace(value)
	normalized := strings.Builder{}
	for _, r := range value {
		if !unicode.IsSpace(r) && r != 0xFEFF && r != '(' && r != ')' && r != '-' {
			normalized.WriteRune(r)
		}
	}
	result := normalized.String()
	if strings.HasPrefix(result, "+") {
		return result
	}
	return "+" + result
}

func normalizeSubmittedPhone(value string) string {
	digits := strings.Builder{}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	raw := digits.String()
	if strings.HasPrefix(raw, "0") {
		return "+62" + raw[1:]
	}
	if strings.HasPrefix(raw, "62") {
		return "+" + raw
	}
	return "+" + raw
}

func normalizeTrackingPhone(value string) string {
	digits := strings.Builder{}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	raw := digits.String()
	if strings.HasPrefix(raw, "0") {
		return "+62" + raw[1:]
	}
	if strings.HasPrefix(raw, "62") {
		return "+" + raw
	}
	if strings.HasPrefix(strings.TrimSpace(value), "+") {
		return "+" + raw
	}
	return "+62" + raw
}

func validTrackingPhoneCharacters(value string) bool {
	if value == "" {
		return false
	}
	for index, r := range value {
		if index == 0 && r == '+' {
			continue
		}
		if (r >= '0' && r <= '9') || unicode.IsSpace(r) || r == 0xFEFF || r == '(' || r == ')' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func containsSensitiveOrderFields(account string, notes *string) bool {
	value := account
	if notes != nil {
		value += " " + *notes
	}
	return regexp.MustCompile(`(?i)password|\bpass\b|otp|token|credential`).MatchString(value)
}
