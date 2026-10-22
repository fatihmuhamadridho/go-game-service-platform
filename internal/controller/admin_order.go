package controller

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type AdminOrderError struct {
	Code, Message string
	Status        int
}

func (e *AdminOrderError) Error() string { return e.Message }
func ToAdminOrderError(err error) *AdminOrderError {
	var e *AdminOrderError
	if errors.As(err, &e) {
		return e
	}
	return &AdminOrderError{Code: "ORDT03001", Message: "Data pesanan sementara tidak tersedia.", Status: 500}
}
func adminOrderFail(code, msg string, status int) error { return &AdminOrderError{code, msg, status} }

type AdminOrderController struct{ repo model.AdminOrderRepository }

func NewAdminOrderController(r model.AdminOrderRepository) *AdminOrderController {
	return &AdminOrderController{repo: r}
}

var orderStatusLabels = map[string]string{"PENDING_PAYMENT": "Menunggu Pembayaran", "PENDING_VALIDATION": "Menunggu Validasi", "PROCESSING": "Diproses", "COMPLETED": "Selesai", "CANCELLED": "Dibatalkan"}
var paymentStatusLabels = map[string]string{"UNPAID": "Belum Dibayar", "PENDING": "Menunggu Validasi", "PENDING_REVIEW": "Menunggu Validasi", "PENDING_VALIDATION": "Menunggu Validasi", "PAID": "Dibayar", "FAILED": "Gagal", "EXPIRED": "Gagal", "REFUNDED": "Dikembalikan"}

func (c *AdminOrderController) List(ctx context.Context, search, status string, page, size int) (map[string]any, error) {
	orders, items, payments, e := c.repo.ListOrderGraphs(ctx)
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, o := range orders {
		v := orderListDTO(o, items, payments)
		if status != "" && status != "Semua Status" && v["status"] != status {
			continue
		}
		hay := cases.Lower(language.Indonesian).String(strings.Join([]string{v["id"].(string), o.CustomerName, o.CustomerWhatsApp, v["game"].(string), v["service"].(string)}, " "))
		needle := cases.Lower(language.Indonesian).String(JSTrim(search))
		if search != "" && !strings.Contains(hay, needle) {
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["createdAt"].(time.Time).After(out[j]["createdAt"].(time.Time)) })
	total := len(out)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return map[string]any{"orders": out[start:end], "pagination": map[string]int{"page": page, "pageSize": size, "total": total, "totalPages": (total + size - 1) / size}}, nil
}
func (c *AdminOrderController) Get(ctx context.Context, raw string) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil {
		return nil, adminOrderFail("ORDB02001", "ID pesanan tidak valid.", 400)
	}
	o, items, payments, oh, ph, proofs, e := c.repo.GetOrderGraph(ctx, id)
	if e != nil {
		return nil, e
	}
	if o == nil {
		return nil, adminOrderFail("ORDB02002", "Pesanan tidak ditemukan.", 404)
	}
	v := orderListDTO(*o, items, payments)
	v["customerWhatsapp"] = o.CustomerWhatsApp
	v["subtotal"] = decimalFloat(o.SubtotalAmount)
	v["discount"] = decimalFloat(o.DiscountAmount)
	v["currency"] = o.Currency
	v["expiresAt"] = o.ExpiresAt
	itemRows := []map[string]any{}
	for _, i := range items {
		itemRows = append(itemRows, map[string]any{"id": i.ID.Hex(), "gameName": i.GameNameSnapshot, "serviceName": i.ServiceNameSnapshot, "categoryName": i.CategoryNameSnapshot, "gameAccount": i.GameAccount, "gameServer": i.GameServer, "customerNotes": i.CustomerNotes, "quantity": i.Quantity, "unitPrice": nullableDecimalFloat(i.UnitPrice), "lineTotal": nullableDecimalFloat(i.LineTotal), "estimate": i.EstimateSnapshot})
	}
	payRows := []map[string]any{}
	for _, p := range payments {
		payRows = append(payRows, map[string]any{"id": p.ID.Hex(), "method": p.Method, "status": p.Status, "amount": decimalFloat(p.Amount), "referenceNote": p.ReferenceNote, "submittedAt": p.SubmittedAt, "reviewedAt": p.ReviewedAt, "reviewNote": p.ReviewNote})
	}
	proofRows := []map[string]any{}
	for _, p := range proofs {
		proofRows = append(proofRows, map[string]any{"id": p.ID.Hex(), "paymentId": p.PaymentID.Hex(), "mimeType": p.MimeType, "fileSizeBytes": p.FileSizeBytes, "uploadedAt": p.UploadedAt, "viewUrl": "/api/admin/orders/v1/proofs/" + p.ID.Hex() + "/image"})
	}
	sort.SliceStable(oh, func(i, j int) bool { return oh[i].ChangedAt.Before(oh[j].ChangedAt) })
	history := []map[string]any{}
	for _, h := range oh {
		history = append(history, map[string]any{"oldStatus": h.OldStatus, "newStatus": h.NewStatus, "reason": h.Reason, "occurredAt": h.ChangedAt})
	}
	v["items"], v["payments"], v["proofs"], v["statusHistory"] = itemRows, payRows, proofRows, history
	_ = ph
	return v, nil
}
func (c *AdminOrderController) Review(ctx context.Context, orderRaw, paymentRaw, decision string, note *string, actor bson.ObjectID) (map[string]any, error) {
	orderID, e1 := bson.ObjectIDFromHex(orderRaw)
	paymentID, e2 := bson.ObjectIDFromHex(paymentRaw)
	if e1 != nil || e2 != nil || actor.IsZero() {
		return nil, adminOrderFail("ORDB02001", "ID pesanan atau pembayaran tidak valid.", 400)
	}
	pay, order, e := c.repo.ReviewPayment(ctx, orderID, paymentID, decision, note, actor)
	if errors.Is(e, model.ErrAdminOrderNotFound) || errors.Is(e, model.ErrAdminOrderPaymentNotFound) {
		return nil, adminOrderFail("ORDB02002", "Pesanan atau pembayaran tidak ditemukan.", 404)
	}
	if errors.Is(e, model.ErrAdminPaymentNotReviewable) {
		return nil, adminOrderFail("ORDB02004", "Pembayaran sudah ditinjau atau tidak menunggu validasi.", 409)
	}
	if errors.Is(e, model.ErrAdminPaymentReviewConflict) {
		return nil, adminOrderFail("ORDB02004", "Pembayaran sedang ditinjau oleh admin lain.", 409)
	}
	if e != nil {
		return nil, e
	}
	return map[string]any{"paymentStatus": pay, "orderStatus": order}, nil
}
func (c *AdminOrderController) Complete(ctx context.Context, raw string, actor bson.ObjectID) (map[string]any, error) {
	id, e := bson.ObjectIDFromHex(raw)
	if e != nil || actor.IsZero() {
		return nil, adminOrderFail("ORDB02001", "ID pesanan atau admin tidak valid.", 400)
	}
	e = c.repo.CompleteOrder(ctx, id, actor, time.Now().UTC())
	if errors.Is(e, model.ErrAdminOrderNotFound) {
		return nil, adminOrderFail("ORDB02002", "Pesanan tidak ditemukan.", 404)
	}
	if errors.Is(e, model.ErrAdminOrderNotCompletable) {
		return nil, adminOrderFail("ORDB02004", "Hanya pesanan diproses dengan pembayaran tervalidasi yang dapat diselesaikan.", 409)
	}
	if errors.Is(e, model.ErrAdminOrderCompletionConflict) {
		return nil, adminOrderFail("ORDB02004", "Status pesanan sedang diperbarui. Muat ulang pesanan.", 409)
	}
	if e != nil {
		return nil, e
	}
	return map[string]any{"orderStatus": "COMPLETED"}, nil
}
func orderListDTO(o model.Order, items []model.OrderItem, payments []model.OrderPayment) map[string]any {
	game, service := "Game", "Layanan"
	for _, i := range items {
		if i.OrderID == o.ID {
			game, service = i.GameNameSnapshot, i.ServiceNameSnapshot
			break
		}
	}
	method, payStatus := "—", o.PaymentStatus
	var latest time.Time
	for _, p := range payments {
		if p.OrderID == o.ID && (latest.IsZero() || p.CreatedAt.After(latest)) {
			latest = p.CreatedAt
			method = p.Method
			payStatus = p.Status
		}
	}
	return map[string]any{"id": o.OrderNumber, "internalId": o.ID.Hex(), "customerName": o.CustomerName, "whatsapp": o.CustomerWhatsApp, "game": game, "service": service, "amount": decimalFloat(o.TotalAmount), "status": defaultStringValue(orderStatusLabels[o.OrderStatus], o.OrderStatus), "rawStatus": o.OrderStatus, "paymentMethod": method, "paymentStatus": defaultStringValue(paymentStatusLabels[payStatus], payStatus), "createdAt": o.CreatedAt}
}

func nullableDecimalFloat(value *bson.Decimal128) any {
	if value == nil {
		return nil
	}
	return decimalFloat(*value)
}
