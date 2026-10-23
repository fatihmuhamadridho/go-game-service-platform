package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/disintegration/imaging"
	"github.com/rwcarlsen/goexif/exif"
	"image"
	_ "image/jpeg"
	"image/png"
	"math/big"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type OrderError struct {
	Code    string
	Message string
	Status  int
}

func (e *OrderError) Error() string { return e.Message }
func ToOrderError(err error) *OrderError {
	var target *OrderError
	if errors.As(err, &target) {
		return target
	}
	return &OrderError{"ORDT03001", "Layanan pesanan sedang tidak tersedia.", 500}
}

type CheckoutInput struct {
	GameSlug, ServiceID                                string
	Quantity                                           int32
	PaymentMethod, CustomerName, WhatsApp, GameAccount string
	GameServer, CustomerNotes                          *string
	IdempotencyKey                                     string
}
type CheckoutResult struct {
	Order                  map[string]any `json:"order"`
	Payment                map[string]any `json:"payment"`
	TrackingToken          string         `json:"trackingToken,omitempty"`
	TrackingTokenExpiresAt string         `json:"trackingTokenExpiresAt,omitempty"`
	NextAction             string         `json:"nextAction"`
	IdempotentReplay       bool           `json:"idempotentReplay"`
}
type OrderController struct {
	repository      model.OrderRepository
	proofRepository model.PaymentProofRepository
	storage         model.PrivateObjectStorage
}

type TrackingResult struct {
	Order                  map[string]any   `json:"order"`
	Timeline               []map[string]any `json:"timeline"`
	DataAsOf               string           `json:"dataAsOf,omitempty"`
	TrackingToken          string           `json:"trackingToken,omitempty"`
	TrackingTokenExpiresAt string           `json:"trackingTokenExpiresAt,omitempty"`
}

func (o *OrderController) IssueTrackingToken(ctx context.Context, orderID bson.ObjectID) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := "trk_" + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().UTC().Add(15 * time.Minute)
	err := o.repository.CreateTrackingToken(ctx, model.OrderTrackingToken{ID: bson.NewObjectID(), TokenHash: hex.EncodeToString(hash[:]), OrderID: orderID, ExpiresAt: expires, CreatedAt: time.Now().UTC()})
	return token, expires, err
}

func (o *OrderController) TrackingByToken(ctx context.Context, token, orderNumber string, resultView ...bool) (TrackingResult, error) {
	if !regexp.MustCompile(`^trk_[A-Za-z0-9_-]{43}$`).MatchString(token) {
		return TrackingResult{}, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi. Silakan cek pesanan kembali.", 400)
	}
	hash := sha256.Sum256([]byte(token))
	record, err := o.repository.FindTrackingToken(ctx, hex.EncodeToString(hash[:]))
	if err != nil {
		return TrackingResult{}, orderFailure("ORDT03001", "Pelacakan pesanan sedang tidak tersedia.", 500)
	}
	if record == nil || record.RevokedAt != nil || !record.ExpiresAt.After(time.Now()) {
		return TrackingResult{}, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi. Silakan cek pesanan kembali.", 400)
	}
	order, items, _, orderEvents, paymentEvents, err := o.repository.GetOrderGraph(ctx, record.OrderID)
	if err != nil {
		return TrackingResult{}, orderFailure("ORDT03001", "Pelacakan pesanan sedang tidak tersedia.", 500)
	}
	if order == nil || order.OrderNumber != orderNumber || len(items) == 0 {
		return TrackingResult{}, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi. Silakan cek pesanan kembali.", 400)
	}
	if len(resultView) > 0 && resultView[0] && order.PaymentStatus != "PENDING_REVIEW" && order.PaymentStatus != "PAID" {
		return TrackingResult{}, orderFailure("ORDB02004", "Hasil pembayaran tersedia setelah pembayaran dilaporkan.", 409)
	}
	ol := map[string]string{"PENDING_PAYMENT": "Menunggu pembayaran", "PENDING_VALIDATION": "Menunggu verifikasi pembayaran", "PROCESSING": "Pesanan sedang diproses", "COMPLETED": "Pesanan selesai", "CANCELLED": "Pesanan dibatalkan"}
	pl := map[string]string{"UNPAID": "Menunggu pembayaran", "PENDING_REVIEW": "Menunggu verifikasi pembayaran", "PAID": "Pembayaran terverifikasi", "FAILED": "Pembayaran ditolak atau kedaluwarsa", "EXPIRED": "Pembayaran kedaluwarsa", "REFUNDED": "Pembayaran dikembalikan"}
	timeline := make([]map[string]any, 0, len(orderEvents)+len(paymentEvents))
	for _, e := range orderEvents {
		timeline = append(timeline, map[string]any{"status": e.NewStatus, "label": trackingLabel(ol, e.NewStatus, "Status pesanan diperbarui"), "description": nil, "occurredAt": e.ChangedAt})
	}
	for _, e := range paymentEvents {
		timeline = append(timeline, map[string]any{"status": e.NewStatus, "label": trackingLabel(pl, e.NewStatus, "Status pembayaran diperbarui"), "description": nil, "occurredAt": e.ChangedAt})
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		return timeline[i]["occurredAt"].(time.Time).Before(timeline[j]["occurredAt"].(time.Time))
	})
	if len(timeline) > 50 {
		timeline = timeline[len(timeline)-50:]
	}
	for _, e := range timeline {
		e["occurredAt"] = e["occurredAt"].(time.Time).Format(time.RFC3339Nano)
	}
	phone := order.CustomerWhatsApp
	masked := "••••••"
	if len(phone) > 7 {
		masked = phone[:5] + "••••" + phone[len(phone)-3:]
	}
	return TrackingResult{Order: map[string]any{"orderNumber": order.OrderNumber, "status": order.OrderStatus, "statusLabel": trackingLabel(ol, order.OrderStatus, "Status pesanan diperbarui"), "paymentStatus": order.PaymentStatus, "paymentStatusLabel": trackingLabel(pl, order.PaymentStatus, "Status pembayaran diperbarui"), "gameName": defaultStringValue(items[0].GameNameSnapshot, "Game"), "serviceName": defaultStringValue(items[0].ServiceNameSnapshot, "Layanan"), "createdAt": order.CreatedAt.Format(time.RFC3339Nano), "updatedAt": order.UpdatedAt.Format(time.RFC3339Nano), "customerWhatsappMasked": masked}, Timeline: timeline, DataAsOf: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func trackingLabel(labels map[string]string, status, fallback string) string {
	if label := labels[status]; label != "" {
		return label
	}
	return fallback
}

func NewOrderController(repository model.OrderRepository) *OrderController {
	return &OrderController{repository: repository}
}
func (o *OrderController) ConfigureProofs(repo model.PaymentProofRepository, storage model.PrivateObjectStorage) {
	o.proofRepository = repo
	o.storage = storage
}

func (o *OrderController) IssueProofUploadToken(ctx context.Context, paymentID, pathname, clientPayload, callbackURL string, validUntil time.Time) (map[string]any, error) {
	if o.storage == nil || !o.storage.Configured() {
		return nil, orderFailure("ORDT04001", "Penyimpanan bukti pembayaran belum dikonfigurasi.", 503)
	}
	if !validProofPath(paymentID, pathname) {
		return nil, orderFailure("ORDB02003", "Pilih gambar JPG atau PNG dengan ukuran maksimal 5 MB.", 400)
	}
	var payload struct {
		OrderNumber   string `json:"orderNumber"`
		TrackingToken string `json:"trackingToken"`
	}
	if json.Unmarshal([]byte(clientPayload), &payload) != nil || !regexp.MustCompile(`^JH-\d{8}-[A-F0-9]{6}$`).MatchString(payload.OrderNumber) || payload.TrackingToken == "" {
		return nil, orderFailure("ORDB02002", "Akses pesanan perlu diverifikasi.", 401)
	}
	scope, e := o.AuthorizedOrderPayment(ctx, payload.TrackingToken, payload.OrderNumber)
	if e != nil {
		return nil, e
	}
	p := scope["payment"].(model.OrderPayment)
	order := scope["order"].(model.Order)
	if p.ID.Hex() != paymentID || p.Status != "UNPAID" || order.ExpiresAt == nil || !order.ExpiresAt.After(time.Now()) {
		return nil, orderFailure("ORDB02002", "Pesanan tidak dapat menerima bukti pembayaran.", 400)
	}
	if validUntil.After(time.Now().Add(5 * time.Minute)) {
		validUntil = time.Now().Add(5 * time.Minute)
	}
	callbackPayload, _ := json.Marshal(map[string]string{"paymentId": paymentID})
	token, e := o.storage.ClientUploadToken(pathname, string(callbackPayload), callbackURL, validUntil)
	if e != nil {
		return nil, e
	}
	return map[string]any{"type": "blob.generate-client-token", "clientToken": token}, nil
}
func (o *OrderController) VerifyProofUploadCallback(body []byte, signature string) bool {
	return o.storage != nil && o.storage.VerifyUploadCallback(body, signature)
}

func (o *OrderController) ProofStorageConfigured() bool {
	return o.storage != nil && o.storage.Configured()
}
func validProofPath(paymentID, pathname string) bool {
	return regexp.MustCompile(`(?i)^payment-proofs/` + regexp.QuoteMeta(paymentID) + `/[a-f0-9-]{36}\.(?:jpg|jpeg|png)$`).MatchString(pathname)
}

func ValidProofPath(paymentID, pathname string) bool { return validProofPath(paymentID, pathname) }
func (o *OrderController) FinalizeProof(ctx context.Context, paymentID, orderNumber, trackingToken, pathname string) (map[string]any, error) {
	if o.storage == nil || o.proofRepository == nil || !o.storage.Configured() {
		return nil, orderFailure("ORDT04001", "Penyimpanan bukti pembayaran belum dikonfigurasi.", 503)
	}
	id, e := bson.ObjectIDFromHex(paymentID)
	if e != nil {
		return nil, orderFailure("ORDB02001", "Payment ID tidak valid.", 400)
	}
	if !validProofPath(paymentID, pathname) {
		return nil, orderFailure("ORDB02003", "Pilih gambar JPG atau PNG dengan ukuran maksimal 5 MB.", 400)
	}
	scope, e := o.AuthorizedOrderPayment(ctx, trackingToken, orderNumber)
	if e != nil {
		return nil, e
	}
	payment := scope["payment"].(model.OrderPayment)
	order := scope["order"].(model.Order)
	if payment.ID != id {
		return nil, orderFailure("ORDB02002", "Pesanan sudah kedaluwarsa atau tidak cocok.", 404)
	}
	if order.ExpiresAt == nil || !order.ExpiresAt.After(time.Now()) {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02002", "Pesanan sudah kedaluwarsa.", 404)
	}
	proofs, e := o.proofRepository.ListPaymentProofs(ctx, id)
	if e != nil {
		return nil, e
	}
	if payment.Status == "PENDING_REVIEW" && len(proofs) > 0 {
		_ = o.storage.Delete(ctx, pathname)
		return map[string]any{"status": "PENDING_REVIEW", "idempotentReplay": true}, nil
	}
	if payment.Status != "UNPAID" {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02002", "Payment tidak dapat menerima bukti pembayaran.", 409)
	}
	input, e := o.storage.Get(ctx, pathname)
	if e != nil {
		_ = o.storage.Delete(ctx, pathname)
		return nil, proofReadFailure(e)
	}
	if len(input) == 0 || len(input) > 5*1024*1024 {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02003", "Ukuran gambar maksimal 5 MB.", 400)
	}
	config, format, e := image.DecodeConfig(bytes.NewReader(input))
	if e != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02003", "File bukan gambar JPG atau PNG yang valid.", 400)
	}
	decoded, format, e := image.Decode(bytes.NewReader(input))
	if e != nil || (format != "jpeg" && format != "png") {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02003", "File bukan gambar JPG atau PNG yang valid.", 400)
	}
	if format == "jpeg" {
		if metadata, exifErr := exif.Decode(bytes.NewReader(input)); exifErr == nil {
			if orientation, orientationErr := metadata.Get(exif.Orientation); orientationErr == nil {
				if value, numberErr := orientation.Int(0); numberErr == nil {
					switch value {
					case 2:
						decoded = imaging.FlipH(decoded)
					case 3:
						decoded = imaging.Rotate180(decoded)
					case 4:
						decoded = imaging.FlipV(decoded)
					case 5:
						decoded = imaging.Rotate90(imaging.FlipH(decoded))
					case 6:
						decoded = imaging.Rotate270(decoded)
					case 7:
						decoded = imaging.Rotate270(imaging.FlipH(decoded))
					case 8:
						decoded = imaging.Rotate90(decoded)
					}
				}
			}
		}
	}
	var normalized bytes.Buffer
	if e = png.Encode(&normalized, decoded); e != nil {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDB02003", "Gambar tidak dapat diproses.", 400)
	}
	raw := make([]byte, 24)
	if _, e = rand.Read(raw); e != nil {
		_ = o.storage.Delete(ctx, pathname)
		return nil, e
	}
	objectKey := "payment-proofs/" + paymentID + "/" + hex.EncodeToString(raw) + ".png"
	stored, e := o.storage.Put(ctx, objectKey, normalized.Bytes(), "image/png")
	if e != nil {
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDT04001", "Penyimpanan bukti pembayaran sedang tidak tersedia.", 503)
	}
	if e = o.storage.Delete(ctx, pathname); e != nil {
		_ = o.storage.Delete(ctx, stored)
		_ = o.storage.Delete(ctx, pathname)
		return nil, orderFailure("ORDT04001", "Penyimpanan bukti pembayaran sedang tidak tersedia.", 503)
	}
	proofID, e := o.proofRepository.FinalizeProof(ctx, id, orderNumber, pathname, stored, int64(normalized.Len()), time.Now().UTC())
	if e != nil {
		_ = o.storage.Delete(ctx, stored)
		if errors.Is(e, model.ErrPaymentProofConflict) {
			proofs, proofErr := o.proofRepository.ListPaymentProofs(ctx, id)
			if proofErr != nil {
				return nil, orderFailure("ORDT03001", "Bukti pembayaran tidak dapat disimpan saat ini.", 500)
			}
			if len(proofs) > 0 {
				return map[string]any{"status": "PENDING_REVIEW", "idempotentReplay": true}, nil
			}
			return nil, orderFailure("ORDB02004", "Payment sedang berubah. Coba kirim ulang.", 409)
		}
		return nil, e
	}
	return map[string]any{"id": proofID.Hex(), "status": "PENDING_REVIEW", "idempotentReplay": false}, nil
}
func (o *OrderController) ProofImage(ctx context.Context, rawID string) ([]byte, error) {
	id, e := bson.ObjectIDFromHex(rawID)
	if e != nil {
		return nil, orderFailure("ORDB02001", "ID bukti pembayaran tidak valid.", 400)
	}
	proof, e := o.proofRepository.FindPaymentProof(ctx, id)
	if e != nil {
		return nil, e
	}
	if proof == nil {
		return nil, orderFailure("ORDB02002", "Bukti pembayaran tidak ditemukan.", 404)
	}
	if o.storage == nil || !o.storage.Configured() {
		return nil, orderFailure("ORDT04001", "Penyimpanan bukti pembayaran belum tersedia.", 503)
	}
	data, e := o.storage.Get(ctx, proof.ObjectKey)
	if e != nil {
		if errors.Is(e, model.ErrPrivateObjectNotFound) {
			return nil, orderFailure("ORDB02002", "Gambar bukti pembayaran tidak ditemukan.", 404)
		}
		return nil, orderFailure("ORDT03001", "Gambar bukti pembayaran sementara tidak tersedia.", 500)
	}
	return data, nil
}

func paymentInstructions(method string) (map[string]any, error) {
	if method == "QRIS" {
		raw := strings.TrimSpace(os.Getenv("PAYMENT_QRIS_IMAGE_URL"))
		u, err := url.Parse(raw)
		if raw == "" || err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && os.Getenv("NODE_ENV") != "production")) {
			return nil, orderFailure("ORDB02004", "Instruksi metode pembayaran belum dikonfigurasi.", 503)
		}
		return map[string]any{"qrImageUrl": raw, "bankName": nil, "accountNumber": nil, "accountHolder": nil}, nil
	}
	bank := strings.TrimSpace(os.Getenv("PAYMENT_BCA_BANK_NAME"))
	number := strings.Join(strings.Fields(os.Getenv("PAYMENT_BCA_ACCOUNT_NUMBER")), "")
	holder := strings.TrimSpace(os.Getenv("PAYMENT_BCA_ACCOUNT_HOLDER"))
	if bank == "" || holder == "" || len(number) < 5 || len(number) > 30 {
		return nil, orderFailure("ORDB02004", "Instruksi metode pembayaran belum dikonfigurasi.", 503)
	}
	for _, c := range number {
		if c < '0' || c > '9' {
			return nil, orderFailure("ORDB02004", "Instruksi metode pembayaran belum dikonfigurasi.", 503)
		}
	}
	return map[string]any{"qrImageUrl": nil, "bankName": bank, "accountNumber": number, "accountHolder": holder}, nil
}

func (o *OrderController) PaymentInstructions(method string) (map[string]any, error) {
	return paymentInstructions(method)
}
func (o *OrderController) AuthorizedOrderPayment(ctx context.Context, token, number string) (map[string]any, error) {
	if !regexp.MustCompile(`^trk_[A-Za-z0-9_-]{43}$`).MatchString(token) {
		return nil, orderFailure("ORDB02002", "Akses pesanan perlu diverifikasi.", 401)
	}
	hash := sha256.Sum256([]byte(token))
	record, err := o.repository.FindTrackingToken(ctx, hex.EncodeToString(hash[:]))
	if err != nil {
		return nil, err
	}
	if record == nil || record.RevokedAt != nil || !record.ExpiresAt.After(time.Now()) {
		return nil, orderFailure("ORDB02002", "Akses pesanan perlu diverifikasi.", 401)
	}
	order, items, payments, _, _, err := o.repository.GetOrderGraph(ctx, record.OrderID)
	if err != nil {
		return nil, err
	}
	if order == nil || order.OrderNumber != number || len(items) == 0 || len(payments) == 0 {
		return nil, orderFailure("ORDB02002", "Pesanan tidak ditemukan.", 404)
	}
	return map[string]any{"order": *order, "item": items[0], "payment": payments[0]}, nil
}
func (o *OrderController) PaymentStatus(ctx context.Context, paymentID bson.ObjectID, number, whatsapp string) (map[string]any, error) {
	p, order, err := o.repository.FindPaymentOrder(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if p == nil || order == nil || order.OrderNumber != number || normalizeOrderPhone(order.CustomerWhatsApp) != normalizeOrderPhone(whatsapp) {
		return nil, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi.", 404)
	}
	expires := any(nil)
	retry := false
	if order.ExpiresAt != nil {
		expires = *order.ExpiresAt
		retry = p.Status == "UNPAID" && order.ExpiresAt.After(time.Now())
	}
	var note any
	if p.Status == "UNPAID" {
		note = p.ReviewNote
	}
	return map[string]any{"paymentStatus": p.Status, "reviewNote": note, "expiresAt": expires, "retryAllowed": retry}, nil
}

func (o *OrderController) Checkout(ctx context.Context, input CheckoutInput, featureSprint2 bool) (CheckoutResult, error) {
	instructions, err := paymentInstructions(input.PaymentMethod)
	if err != nil {
		return CheckoutResult{}, err
	}
	settings, err := o.repository.GetCheckoutSettings(ctx)
	if err != nil {
		return CheckoutResult{}, err
	}
	if settings == nil || settings.MaintenanceMode || settings.DefaultCurrency != "IDR" {
		return CheckoutResult{}, orderFailure("ORDT03001", "Checkout belum tersedia untuk konfigurasi saat ini.", 503)
	}
	requestHash, keyHash, err := checkoutRequestHashes(input)
	if err != nil {
		return CheckoutResult{}, orderFailure("ORDT03001", "Data pesanan tidak dapat diproses.", 500)
	}
	lookupTime := time.Now().UTC()
	existing, err := o.repository.FindIdempotency(ctx, keyHash)
	if err != nil {
		return CheckoutResult{}, err
	}
	if existing != nil && existing.ExpiresAt.After(lookupTime) {
		if existing.RequestHash != requestHash {
			return CheckoutResult{}, orderFailure("ORDB02005", "Idempotency key sudah digunakan untuk data pesanan berbeda.", 409)
		}
		if existing.ResponseJSON == nil {
			return CheckoutResult{}, orderFailure("ORDB02005", "Pesanan dengan kunci ini sedang diproses. Coba lagi sebentar.", 409)
		}
		var replay CheckoutResult
		raw, _ := bson.Marshal(existing.ResponseJSON)
		_ = bson.Unmarshal(raw, &replay)
		replay.IdempotentReplay = true
		return replay, nil
	}
	if existing != nil {
		_ = o.repository.DeleteIdempotency(ctx, existing.ID)
	}
	reservationTime := time.Now().UTC()
	idem := model.CheckoutIdempotency{ID: bson.NewObjectID(), KeyHash: keyHash, RequestHash: requestHash, CreatedAt: reservationTime, ExpiresAt: reservationTime.Add(24 * time.Hour)}
	if err = o.repository.CreateIdempotency(ctx, idem); err != nil {
		raced, findErr := o.repository.FindIdempotency(ctx, keyHash)
		if findErr != nil {
			return CheckoutResult{}, findErr
		}
		if raced != nil && raced.RequestHash != requestHash {
			return CheckoutResult{}, orderFailure("ORDB02005", "Idempotency key sudah digunakan untuk data pesanan berbeda.", 409)
		}
		if raced != nil && raced.ResponseJSON != nil {
			var replay CheckoutResult
			raw, _ := bson.Marshal(raced.ResponseJSON)
			_ = bson.Unmarshal(raw, &replay)
			replay.IdempotentReplay = true
			return replay, nil
		}
		return CheckoutResult{}, orderFailure("ORDB02005", "Pesanan dengan kunci ini sedang diproses. Coba lagi sebentar.", 409)
	}
	completed := false
	defer func() {
		if !completed {
			_ = o.repository.DeleteIdempotency(ctx, idem.ID)
		}
	}()
	game, err := o.repository.FindActiveGameBySlugForOrder(ctx, input.GameSlug)
	if err != nil {
		return CheckoutResult{}, err
	}
	if game == nil {
		return CheckoutResult{}, orderFailure("ORDB02002", "Game tidak tersedia.", 400)
	}
	serviceID, err := bson.ObjectIDFromHex(input.ServiceID)
	if err != nil {
		return CheckoutResult{}, orderFailure("ORDB02003", "Layanan tidak tersedia untuk game ini.", 400)
	}
	service, game, category, err := o.repository.FindCheckoutService(ctx, game.ID, serviceID)
	if err != nil {
		return CheckoutResult{}, err
	}
	if service == nil || game == nil || category == nil {
		return CheckoutResult{}, orderFailure("ORDB02003", "Layanan tidak tersedia untuk game ini.", 400)
	}
	if service.IsCustomPrice || service.PriceAmount == nil {
		return CheckoutResult{}, orderFailure("ORDB02003", "Layanan ini memerlukan persetujuan harga sebelum checkout.", 400)
	}
	unitMinor, ok := decimalMinorUnits(service.PriceAmount.String())
	if !ok {
		return CheckoutResult{}, orderFailure("ORDT03001", "Harga layanan tidak dapat diproses.", 500)
	}
	unitText := minorUnitsDecimal(unitMinor)
	totalMinor := new(big.Int).Mul(unitMinor, big.NewInt(int64(input.Quantity)))
	totalText := minorUnitsDecimal(totalMinor)
	total, err := bson.ParseDecimal128(totalText)
	if err != nil {
		return CheckoutResult{}, orderFailure("ORDT03001", "Harga layanan tidak dapat diproses.", 500)
	}
	unitPrice, err := bson.ParseDecimal128(unitText)
	if err != nil {
		return CheckoutResult{}, orderFailure("ORDT03001", "Harga layanan tidak dapat diproses.", 500)
	}
	amount, _ := strconv.ParseFloat(totalText, 64)
	now := time.Now().UTC()
	orderID, paymentID, itemID := bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()
	randomNumber := make([]byte, 3)
	if _, err = rand.Read(randomNumber); err != nil {
		return CheckoutResult{}, orderFailure("ORDT03001", "Pesanan tidak dapat disimpan saat ini.", 500)
	}
	orderNumber := fmt.Sprintf("JH-%s-%s", now.Format("20060102"), strings.ToUpper(hex.EncodeToString(randomNumber)))
	expires := now.Add(30 * time.Minute)
	order := model.Order{ID: orderID, OrderNumber: orderNumber, CustomerName: input.CustomerName, CustomerWhatsApp: input.WhatsApp, OrderStatus: "PENDING_PAYMENT", PaymentStatus: "UNPAID", SubtotalAmount: total, DiscountAmount: bson.NewDecimal128(0, 0), TotalAmount: total, Currency: settings.DefaultCurrency, ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}
	categoryName := category.Name
	item := model.OrderItem{ID: itemID, OrderID: orderID, GameID: game.ID, CategoryID: category.ID, ServiceID: service.ID, GameNameSnapshot: game.Name, ServiceNameSnapshot: service.Name, CategoryNameSnapshot: &categoryName, GameAccount: input.GameAccount, GameServer: input.GameServer, CustomerNotes: input.CustomerNotes, Quantity: input.Quantity, UnitPrice: &unitPrice, LineTotal: &total, EstimateSnapshot: service.EstimateText}
	payment := model.OrderPayment{ID: paymentID, OrderID: orderID, Method: input.PaymentMethod, Status: "UNPAID", Amount: total, ReferenceNote: orderNumber, CreatedAt: now, UpdatedAt: now}
	orderReason, paymentReason := "Checkout dibuat.", "Payment attempt dibuat."
	orderHistory := model.OrderStatusHistory{ID: bson.NewObjectID(), OrderID: orderID, NewStatus: "PENDING_PAYMENT", Reason: &orderReason, ChangedAt: now}
	paymentHistory := model.PaymentStatusHistory{ID: bson.NewObjectID(), PaymentID: paymentID, NewStatus: "UNPAID", Reason: &paymentReason, ChangedAt: now}
	if err = o.repository.CreateCheckout(ctx, order, item, payment, orderHistory, paymentHistory); err != nil {
		return CheckoutResult{}, err
	}
	result := CheckoutResult{Order: map[string]any{"id": orderID.Hex(), "orderNumber": orderNumber, "orderStatus": order.OrderStatus, "paymentStatus": order.PaymentStatus, "totalAmount": amount, "currency": order.Currency, "expiresAt": expires.Format(time.RFC3339Nano), "game": map[string]string{"name": game.Name}, "service": map[string]string{"id": service.ID.Hex(), "name": service.Name}, "quantity": input.Quantity}, Payment: map[string]any{"id": paymentID.Hex(), "method": payment.Method, "status": payment.Status, "amount": amount, "instructions": map[string]any{"qrImageUrl": instructions["qrImageUrl"], "bankName": instructions["bankName"], "accountNumber": instructions["accountNumber"], "accountHolder": instructions["accountHolder"], "reference": orderNumber}}, NextAction: "REPORT_PAYMENT"}
	if featureSprint2 {
		result.NextAction = "UPLOAD_PAYMENT_PROOF"
	}
	if err = o.repository.CompleteIdempotency(ctx, idem.ID, result); err != nil {
		if rollback, ok := o.repository.(interface {
			RollbackCheckout(context.Context, bson.ObjectID, bson.ObjectID, bson.ObjectID, bson.ObjectID, bson.ObjectID) error
		}); ok {
			_ = rollback.RollbackCheckout(ctx, orderID, itemID, paymentID, orderHistory.ID, paymentHistory.ID)
		}
		return CheckoutResult{}, err
	}
	completed = true
	return result, nil
}

func (o *OrderController) ReportPayment(ctx context.Context, paymentID bson.ObjectID, orderNumber, whatsapp string, featureSprint2 ...bool) (map[string]any, error) {
	payment, order, err := o.repository.FindPaymentOrder(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if payment == nil || order == nil || order.OrderNumber != orderNumber || order.CustomerWhatsApp != whatsapp {
		return nil, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi.", 404)
	}
	if order.ExpiresAt == nil || !order.ExpiresAt.After(time.Now()) {
		return nil, orderFailure("ORDB02004", "Batas pembayaran pesanan sudah berakhir.", 409)
	}
	if payment.Status != "UNPAID" {
		if payment.Status == "PENDING_REVIEW" {
			if len(featureSprint2) == 0 || !featureSprint2[0] {
				return map[string]any{"paymentStatus": payment.Status}, nil
			}
			if o.proofRepository == nil {
				return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat diverifikasi.", 500)
			}
			proofs, proofErr := o.proofRepository.ListPaymentProofs(ctx, paymentID)
			if proofErr != nil {
				return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat diverifikasi.", 500)
			}
			if len(proofs) > 0 {
				return map[string]any{"paymentStatus": payment.Status}, nil
			}
		}
		return nil, orderFailure("ORDB02004", "Status pembayaran pesanan tidak dapat diubah.", 409)
	}
	if len(featureSprint2) > 0 && featureSprint2[0] {
		if o.proofRepository == nil {
			return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat diverifikasi.", 500)
		}
		proofs, proofErr := o.proofRepository.ListPaymentProofs(ctx, paymentID)
		if proofErr != nil {
			return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat diverifikasi.", 500)
		}
		latestProof := false
		for _, proof := range proofs {
			if proof.UploadedAt.After(payment.UpdatedAt) {
				latestProof = true
				break
			}
		}
		if !latestProof {
			return nil, orderFailure("ORDB02004", "Unggah bukti pembayaran terbaru sebelum mengonfirmasi pembayaran.", 409)
		}
	}
	now := time.Now().UTC()
	changed, err := o.repository.SetPaymentPendingReview(ctx, paymentID, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, orderFailure("ORDB02004", "Status pembayaran sedang diperbarui. Muat ulang pesanan.", 409)
	}
	paymentHistoryID, orderHistoryID := bson.NewObjectID(), bson.NewObjectID()
	rollback := func() {
		_ = o.repository.RollbackPaymentReport(ctx, paymentID, order.ID, paymentHistoryID, orderHistoryID, payment.Status, order.OrderStatus, time.Now().UTC())
	}
	if err = o.repository.UpdateOrderPaymentStatus(ctx, order.ID, "PENDING_REVIEW", "PENDING_VALIDATION", now); err != nil {
		rollback()
		return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat disimpan.", 500)
	}
	oldPay, oldOrder := payment.Status, order.OrderStatus
	paymentReason := "Pelanggan mengonfirmasi pembayaran; menunggu validasi admin."
	if err = o.repository.CreatePaymentHistory(ctx, model.PaymentStatusHistory{ID: paymentHistoryID, PaymentID: paymentID, OldStatus: &oldPay, NewStatus: "PENDING_REVIEW", Reason: &paymentReason, ChangedAt: now}); err != nil {
		rollback()
		return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat disimpan.", 500)
	}
	orderReason := "Pelanggan melaporkan pembayaran; menunggu validasi admin."
	if err = o.repository.CreateOrderHistory(ctx, model.OrderStatusHistory{ID: orderHistoryID, OrderID: order.ID, OldStatus: &oldOrder, NewStatus: "PENDING_VALIDATION", Reason: &orderReason, ChangedAt: now}); err != nil {
		rollback()
		return nil, orderFailure("ORDT03001", "Konfirmasi pembayaran tidak dapat disimpan.", 500)
	}
	return map[string]any{"paymentStatus": "PENDING_REVIEW"}, nil
}

func (o *OrderController) LookupTracking(ctx context.Context, orderNumber, whatsapp string) (TrackingResult, error) {
	order, err := o.repository.FindOrderByNumber(ctx, orderNumber)
	if err != nil {
		return TrackingResult{}, orderFailure("ORDT03001", "Pelacakan pesanan sedang tidak tersedia.", 500)
	}
	if order == nil || normalizeTrackingPhone(order.CustomerWhatsApp) != normalizeTrackingPhone(whatsapp) {
		return TrackingResult{}, orderFailure("ORDB02002", "Pesanan tidak dapat diverifikasi. Periksa nomor pesanan dan WhatsApp.", 400)
	}
	token, expires, err := o.IssueTrackingToken(ctx, order.ID)
	if err != nil {
		return TrackingResult{}, orderFailure("ORDT03001", "Pelacakan pesanan sedang tidak tersedia.", 500)
	}
	result, err := o.TrackingByToken(ctx, token, orderNumber)
	if err != nil {
		return TrackingResult{}, err
	}
	result.DataAsOf = ""
	result.TrackingToken = token
	result.TrackingTokenExpiresAt = expires.Format(time.RFC3339Nano)
	return result, nil
}

func normalizeOrderPhone(value string) string {
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

func decimalMinorUnits(value string) (*big.Int, bool) {
	if !regexp.MustCompile(`^\d+(?:\.\d{1,2})?$`).MatchString(value) {
		return nil, false
	}
	parts := strings.SplitN(value, ".", 2)
	whole, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return nil, false
	}
	whole.Mul(whole, big.NewInt(100))
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		minor, valid := new(big.Int).SetString(fraction, 10)
		if !valid {
			return nil, false
		}
		whole.Add(whole, minor)
	}
	return whole, true
}

func minorUnitsDecimal(value *big.Int) string {
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(value, big.NewInt(100), fraction)
	decimalFraction := fraction.String()
	if len(decimalFraction) < 2 {
		decimalFraction = "0" + decimalFraction
	}
	return whole.String() + "." + decimalFraction
}

func checkoutRequestHashes(input CheckoutInput) (string, string, error) {
	requestBody := struct {
		GameSlug      string `json:"gameSlug"`
		ServiceID     string `json:"serviceId"`
		Quantity      int32  `json:"quantity"`
		PaymentMethod string `json:"paymentMethod"`
		Customer      struct {
			Name     string `json:"name"`
			WhatsApp string `json:"whatsapp"`
		} `json:"customer"`
		Item struct {
			GameAccount   string  `json:"gameAccount"`
			GameServer    *string `json:"gameServer"`
			CustomerNotes *string `json:"customerNotes"`
		} `json:"item"`
	}{
		GameSlug: input.GameSlug, ServiceID: input.ServiceID, Quantity: input.Quantity, PaymentMethod: input.PaymentMethod,
		Customer: struct {
			Name     string `json:"name"`
			WhatsApp string `json:"whatsapp"`
		}{Name: input.CustomerName, WhatsApp: input.WhatsApp},
		Item: struct {
			GameAccount   string  `json:"gameAccount"`
			GameServer    *string `json:"gameServer"`
			CustomerNotes *string `json:"customerNotes"`
		}{GameAccount: input.GameAccount, GameServer: input.GameServer, CustomerNotes: input.CustomerNotes},
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(requestBody); err != nil {
		return "", "", err
	}
	canonical := bytes.TrimSuffix(body.Bytes(), []byte("\n"))
	canonical = bytes.ReplaceAll(canonical, []byte(`\u2028`), []byte("\u2028"))
	canonical = bytes.ReplaceAll(canonical, []byte(`\u2029`), []byte("\u2029"))
	requestDigest := sha256.Sum256(canonical)
	keyDigest := sha256.Sum256([]byte(input.IdempotencyKey))
	return hex.EncodeToString(requestDigest[:]), hex.EncodeToString(keyDigest[:]), nil
}

func orderFailure(code, message string, status int) error {
	return &OrderError{Code: code, Message: message, Status: status}
}

func proofReadFailure(err error) error {
	if errors.Is(err, model.ErrPrivateObjectNotFound) {
		return orderFailure("ORDB02003", "File unggahan tidak ditemukan.", 400)
	}
	if errors.Is(err, model.ErrPrivateObjectInvalid) {
		return orderFailure("ORDB02003", "Ukuran gambar maksimal 5 MB.", 400)
	}
	return orderFailure("ORDT04001", "Penyimpanan bukti pembayaran sedang tidak tersedia.", 503)
}
