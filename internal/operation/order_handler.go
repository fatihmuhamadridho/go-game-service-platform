package operation

import (
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"net/http"
	"strings"
)

func (h *Handler) mongoOrders(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/orders/v1/checkout" && r.Method == http.MethodPost {
		if !h.allowPublicRequest(w, r, "checkout", 20) {
			return
		}
		body := map[string]any{}
		if !decode(w, r, &body) {
			writeError(w, http.StatusBadRequest, "Payload tidak valid.")
			return
		}
		result, replay, err := h.service.SubmitCheckout(r.Context(), body, r.Header.Get("Idempotency-Key"))
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidCheckout), errors.Is(err, ErrInvalidIdempotencyKey):
				writeError(w, http.StatusBadRequest, "Data checkout atau Idempotency-Key tidak valid.")
			case errors.Is(err, ErrUnsupportedPayment):
				writeError(w, http.StatusBadRequest, "Metode pembayaran harus QRIS atau BCA_TRANSFER.")
			case errors.Is(err, ErrUnavailableCheckout):
				writeError(w, http.StatusUnprocessableEntity, "Game atau layanan tidak tersedia.")
			case errors.Is(err, ErrUnpricedCheckout):
				writeError(w, http.StatusUnprocessableEntity, "Layanan ini membutuhkan konfirmasi harga terlebih dahulu.")
			case errors.Is(err, ErrIdempotencyConflict):
				writeError(w, http.StatusConflict, "Idempotency-Key sudah dipakai untuk payload yang berbeda.")
			default:
				writeError(w, http.StatusServiceUnavailable, "Pesanan tidak dapat disimpan atau katalog tidak dapat dimuat.")
			}
			return
		}
		status := http.StatusCreated
		message := "Pesanan dibuat. Instruksi pembayaran menunggu konfigurasi resmi."
		if replay {
			status = http.StatusOK
			message = "Pesanan diproses."
		}
		write(w, status, message, result)
		return
	}
	if path == "/api/orders/v1/tracking/lookup" && r.Method == http.MethodPost {
		if !h.allowPublicRequest(w, r, "tracking-lookup", 20) {
			return
		}
		var request TrackingLookupRequest
		if !decode(w, r, &request) {
			writeError(w, 400, "Payload tidak valid.")
			return
		}
		data, err := h.service.LookupTrackingOrder(r.Context(), request.OrderNumber, request.WhatsApp)
		if errors.Is(err, ErrOrderNotFound) {
			writeError(w, 404, "Pesanan tidak ditemukan atau WhatsApp tidak sesuai.")
			return
		}
		if err != nil {
			writeError(w, 503, "Pesanan tidak dapat dicari.")
			return
		}
		write(w, 200, "OK", data)
		return
	}
	if strings.HasPrefix(path, "/api/orders/v1/tracking/") {
		h.mongoTracking(w, r)
		return
	}
	if strings.HasPrefix(path, "/api/admin/orders/v1/") {
		h.mongoAdminOrder(w, r)
		return
	}
	writeError(w, 404, "Endpoint tidak ditemukan.")
}

func (h *Handler) mongoTracking(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 {
		writeError(w, 404, "Pesanan tidak ditemukan.")
		return
	}
	number, action := parts[4], parts[5]
	entry, err := h.service.FindOrder(r.Context(), bson.D{{Key: "order.orderNumber", Value: number}})
	if err != nil {
		writeError(w, 503, "Pesanan tidak dapat dimuat.")
		return
	}
	if entry == nil {
		writeError(w, 404, "Pesanan tidak ditemukan.")
		return
	}
	if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != entry["trackingToken"] {
		writeError(w, 401, "Sesi pelacakan tidak valid.")
		return
	}
	order, _ := entry["order"].(map[string]any)
	payment, _ := entry["payment"].(map[string]any)
	if action == "payment" && r.Method == http.MethodGet {
		write(w, 200, "OK", map[string]any{"order": map[string]any{"orderNumber": number, "paymentStatus": order["paymentStatus"], "totalAmount": order["totalAmount"], "expiresAt": order["expiresAt"], "game": order["gameName"], "service": order["serviceName"], "quantity": order["quantity"]}, "payment": payment})
		return
	}
	if action == "payment" && r.Method == http.MethodPost {
		if err := h.service.ReportPayment(r.Context(), entry); err != nil {
			if errors.Is(err, ErrInvalidOrderTransition) || errors.Is(err, ErrOrderTransitionConflict) {
				writeError(w, 409, "Status pembayaran berubah atau tidak dapat dilaporkan.")
				return
			}
			writeError(w, 503, "Pembayaran tidak dapat dicatat.")
			return
		}
		write(w, 200, "Pembayaran dicatat untuk tinjauan admin manual.", map[string]any{"paymentStatus": "PENDING_REVIEW"})
		return
	}
	if action == "status" && r.Method == http.MethodGet {
		write(w, 200, "OK", map[string]any{"order": map[string]any{"orderNumber": number, "status": order["orderStatus"], "statusLabel": order["orderStatus"], "gameName": order["gameName"], "serviceName": order["serviceName"], "customerWhatsappMasked": maskPhone(fmt.Sprint(order["customerWhatsapp"])), "paymentStatusLabel": order["paymentStatus"], "paymentStatus": order["paymentStatus"]}, "timeline": []any{map[string]any{"status": order["orderStatus"], "label": order["orderStatus"], "description": nil, "occurredAt": order["createdAt"]}}})
		return
	}
	writeError(w, 404, "Endpoint tidak ditemukan.")
}

func (h *Handler) mongoAdminOrder(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasSuffix(path, "/list") && r.Method == http.MethodGet {
		data, err := h.service.ListOrderPage(r.Context(), r.URL.Query().Get("search"), r.URL.Query().Get("status"), r.URL.Query().Get("method"), intQuery(r, "page", 1), intQuery(r, "pageSize", 20))
		if err != nil {
			writeError(w, 503, "Pesanan tidak dapat dimuat.")
			return
		}
		data["orders"] = data["items"]
		delete(data, "items")
		write(w, 200, "OK", data)
		return
	}
	if strings.Contains(path, "/detail/") && r.Method == http.MethodGet {
		id := path[strings.LastIndex(path, "/")+1:]
		filter := bson.D{{Key: "order.id", Value: id}}
		entry, err := h.service.FindOrder(r.Context(), filter)
		if err != nil {
			writeError(w, 503, "Pesanan tidak dapat dimuat.")
			return
		}
		if entry == nil {
			entry, err = h.service.FindOrder(r.Context(), bson.D{{Key: "order.orderNumber", Value: id}})
			if err != nil {
				writeError(w, 503, "Pesanan tidak dapat dimuat.")
				return
			}
		}
		if entry != nil {
			write(w, 200, "OK", map[string]any{"order": entry["order"]})
			return
		}
	}
	if strings.HasSuffix(path, "/payment-review") && r.Method == http.MethodPost {
		var request PaymentReviewRequest
		if !decode(w, r, &request) {
			writeError(w, 400, "Payload tidak valid.")
			return
		}
		paymentStatus, orderStatus, err := h.service.ReviewOrderPayment(r.Context(), request.OrderID, request.PaymentID, request.Decision)
		if errors.Is(err, ErrInvalidPaymentDecision) {
			writeError(w, 400, "decision harus APPROVE atau REJECT.")
			return
		}
		if errors.Is(err, ErrOrderNotFound) {
			writeError(w, 404, "Pesanan tidak ditemukan.")
			return
		}
		if errors.Is(err, ErrPaymentNotFound) {
			writeError(w, 404, "Pembayaran tidak ditemukan.")
			return
		}
		if errors.Is(err, ErrInvalidOrderTransition) || errors.Is(err, ErrOrderTransitionConflict) {
			writeError(w, 409, "Pembayaran tidak berada pada status PENDING_REVIEW atau status berubah.")
			return
		}
		if err != nil {
			writeError(w, 503, "Keputusan pembayaran tidak dapat disimpan.")
			return
		}
		write(w, 200, "Keputusan pembayaran disimpan.", map[string]any{"paymentStatus": paymentStatus, "orderStatus": orderStatus})
		return
	}
	if strings.Contains(path, "/complete/") && r.Method == http.MethodPost {
		id := path[strings.LastIndex(path, "/")+1:]
		err := h.service.CompleteOrderByID(r.Context(), id)
		if errors.Is(err, ErrOrderNotFound) {
			writeError(w, 404, "Pesanan tidak ditemukan.")
			return
		}
		if errors.Is(err, ErrInvalidOrderTransition) || errors.Is(err, ErrOrderTransitionConflict) {
			writeError(w, 409, "Pesanan belum berstatus PAID dan IN_PROGRESS atau status berubah.")
			return
		} else if err != nil {
			writeError(w, 503, "Pesanan tidak dapat diselesaikan.")
			return
		}
		write(w, 200, "Pesanan ditandai selesai.", map[string]any{"orderStatus": "COMPLETED"})
		return
	}
	writeError(w, 404, "Pesanan tidak ditemukan.")
}
