package operation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/catalog"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/publicgame"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var (
	ErrRecordNotFound        = errors.New("operation record not found")
	ErrInvalidRecord         = errors.New("invalid operation record")
	ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")
	ErrOrderNotFound         = errors.New("order not found")
	ErrPaymentNotFound       = errors.New("payment not found")
)

type CheckoutCatalog interface {
	FindActiveServiceForCheckout(context.Context, string, string) (catalog.Service, error)
}

// Repository defines the persistent operations required by the service.
type Repository interface {
	GetSettings(context.Context) (map[string]any, error)
	SaveSettings(context.Context, map[string]any) error
	List(context.Context, string) ([]map[string]any, error)
	Get(context.Context, string, string) (map[string]any, error)
	Put(context.Context, string, string, map[string]any) error
	Delete(context.Context, string, string) error
	ListOrders(context.Context) ([]map[string]any, error)
	FindOrder(context.Context, bson.D) (map[string]any, error)
	CreateOrder(context.Context, map[string]any, string, string) (map[string]any, bool, error)
	TransitionOrder(context.Context, map[string]any, string, string) (bool, error)
	ClearPrimaryBankAccounts(context.Context) error
}

// Service owns application rules while Repository owns Mongo persistence.
type Service struct {
	repository Repository
	games      publicgame.Data
	catalog    CheckoutCatalog
}

type OperationService interface {
	GetSettings(context.Context) (map[string]any, error)
	UpdateSettings(context.Context, map[string]any) (map[string]any, error)
	List(context.Context, string) ([]map[string]any, error)
	Get(context.Context, string, string) (map[string]any, error)
	Put(context.Context, string, string, map[string]any) error
	Delete(context.Context, string, string) error
	ListOrders(context.Context) ([]map[string]any, error)
	ListTransactions(context.Context) ([]map[string]any, error)
	ListCollectionPage(context.Context, string, string, string, string, string, int, int) (map[string]any, error)
	ListOrderPage(context.Context, string, string, string, int, int) (map[string]any, error)
	LookupTrackingOrder(context.Context, string, string) (map[string]any, error)
	ReviewOrderPayment(context.Context, string, string, string) (string, string, error)
	CompleteOrderByID(context.Context, string) error
	Dashboard(context.Context, string, string, string) (map[string]any, error)
	CatalogServiceCount(context.Context) (int, error)
	FindOrder(context.Context, bson.D) (map[string]any, error)
	CreateOrder(context.Context, map[string]any, string, string) (map[string]any, bool, error)
	TransitionOrder(context.Context, map[string]any, string, string) (bool, error)
	SetPrimaryBankAccount(context.Context, string) error
	ValidateBankAccount(map[string]any) error
	PaymentInstructions(context.Context, string, string) map[string]any
	CheckoutService(context.Context, string, string) (catalog.Service, error)
	ValidateSettingsPatch(map[string]any) error
	ReportPayment(context.Context, map[string]any) error
	ReviewPayment(context.Context, map[string]any, string) (string, string, error)
	CompleteOrder(context.Context, map[string]any) error
	NewID(string) (string, error)
	CreateRecord(context.Context, string, map[string]any) (map[string]any, error)
	UpdateRecord(context.Context, string, string, map[string]any) (map[string]any, error)
	DeleteRecord(context.Context, string, string) error
	FindGameBySlug(context.Context, string) (string, string, bool, error)
	HasGameCatalog() bool
	NewTrackingToken() (string, error)
	SubmitCheckout(context.Context, map[string]any, string) (map[string]any, bool, error)
}

func NewService(repository Repository, games publicgame.Data, catalogStore CheckoutCatalog) *Service {
	return &Service{repository: repository, games: games, catalog: catalogStore}
}

func (s *Service) GetSettings(ctx context.Context) (map[string]any, error) {
	return s.repository.GetSettings(ctx)
}

func (s *Service) UpdateSettings(ctx context.Context, patch map[string]any) (map[string]any, error) {
	if err := s.ValidateSettingsPatch(patch); err != nil {
		return nil, err
	}
	settings, err := s.repository.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	for key, value := range patch {
		settings[key] = value
	}
	if err := s.repository.SaveSettings(ctx, settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *Service) List(ctx context.Context, bucket string) ([]map[string]any, error) {
	return s.repository.List(ctx, bucket)
}

func (s *Service) Get(ctx context.Context, bucket, id string) (map[string]any, error) {
	return s.repository.Get(ctx, bucket, id)
}

func (s *Service) Put(ctx context.Context, bucket, id string, value map[string]any) error {
	return s.repository.Put(ctx, bucket, id, value)
}

func (s *Service) Delete(ctx context.Context, bucket, id string) error {
	return s.repository.Delete(ctx, bucket, id)
}

func (s *Service) ListOrders(ctx context.Context) ([]map[string]any, error) {
	return s.repository.ListOrders(ctx)
}

func (s *Service) ListTransactions(ctx context.Context) ([]map[string]any, error) {
	return s.Transactions(ctx)
}

func (s *Service) CatalogServiceCount(ctx context.Context) (int, error) {
	store, ok := s.catalog.(catalog.Store)
	if !ok {
		return 0, nil
	}
	services, err := store.ListServices(ctx, "", false)
	if err != nil {
		return 0, err
	}
	return len(services), nil
}

func (s *Service) FindOrder(ctx context.Context, filter bson.D) (map[string]any, error) {
	return s.repository.FindOrder(ctx, filter)
}

func (s *Service) CreateOrder(ctx context.Context, entry map[string]any, key, hash string) (map[string]any, bool, error) {
	return s.repository.CreateOrder(ctx, entry, key, hash)
}

func (s *Service) SubmitCheckout(ctx context.Context, body map[string]any, key string) (map[string]any, bool, error) {
	if key != "" && (strings.TrimSpace(key) != key || len(key) < 16 || len(key) > 128) {
		return nil, false, ErrInvalidIdempotencyKey
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, false, ErrInvalidCheckout
	}
	var request CheckoutRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return nil, false, ErrInvalidCheckout
	}
	slug, serviceID := request.GameSlug, request.ServiceID
	if slug == "" || strings.TrimSpace(slug) != slug || len(slug) > 100 || serviceID == "" || strings.TrimSpace(serviceID) != serviceID || len(serviceID) > 100 {
		return nil, false, ErrInvalidCheckout
	}
	method := request.PaymentMethod
	if method != "QRIS" && method != "BCA_TRANSFER" {
		return nil, false, ErrUnsupportedPayment
	}
	gameID, gameName, foundGame, err := s.FindGameBySlug(ctx, slug)
	if err != nil {
		return nil, false, fmt.Errorf("lookup game: %w", err)
	}
	if s.HasGameCatalog() && !foundGame {
		return nil, false, ErrUnavailableCheckout
	}
	service, err := s.CheckoutService(ctx, gameID, serviceID)
	if errors.Is(err, catalog.ErrServiceNotFound) {
		return nil, false, ErrUnavailableCheckout
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup service: %w", err)
	}
	if service.PriceAmount == nil || *service.PriceAmount < 1 {
		return nil, false, ErrUnpricedCheckout
	}
	quantity := 1
	if _, exists := body["quantity"]; exists {
		if request.Quantity < 1 || request.Quantity > 100 {
			return nil, false, ErrInvalidCheckout
		}
		quantity = request.Quantity
	}
	name, phone, account := strings.TrimSpace(request.Customer.Name), strings.TrimSpace(request.Customer.WhatsApp), strings.TrimSpace(request.Item.GameAccount)
	if name == "" || len([]rune(name)) > 100 || !validWhatsApp(phone) || account == "" || len([]rune(account)) > 100 || len([]rune(request.Item.GameServer)) > 100 || len([]rune(request.CustomerNotes)) > 500 {
		return nil, false, ErrInvalidCheckout
	}
	now := time.Now().UTC()
	orderID, err := s.NewID("ord")
	if err != nil {
		return nil, false, err
	}
	suffix := strings.ToUpper(strings.TrimPrefix(orderID, "ord_"))
	number := "BG-" + now.Format("20060102") + "-" + suffix[:12]
	paymentID, err := s.NewID("pay")
	if err != nil {
		return nil, false, err
	}
	token, err := s.NewTrackingToken()
	if err != nil {
		return nil, false, err
	}
	if gameName == "" {
		gameName = slug
	}
	total := float64(*service.PriceAmount) * float64(quantity)
	order := map[string]any{"id": orderID, "internalId": orderID, "orderNumber": number, "orderStatus": "AWAITING_PAYMENT", "paymentStatus": string(PaymentPending), "totalAmount": total, "currency": "IDR", "expiresAt": now.Add(24 * time.Hour).Format(time.RFC3339), "game": map[string]any{"name": gameName}, "service": map[string]any{"id": serviceID, "name": service.Name}, "quantity": quantity, "gameName": gameName, "serviceName": service.Name, "customerName": name, "customerWhatsapp": phone, "gameAccount": account, "gameServer": request.Item.GameServer, "customerNotes": request.CustomerNotes, "createdAt": now.Format(time.RFC3339), "paymentId": paymentID, "paymentMethod": method}
	payment := map[string]any{"id": paymentID, "method": method, "status": string(PaymentPending), "amount": total, "instructions": s.PaymentInstructions(ctx, method, paymentID)}
	entry := map[string]any{"order": order, "payment": payment, "trackingToken": token}
	digest := sha256.Sum256(encoded)
	replay, isReplay, err := s.CreateOrder(ctx, entry, key, hex.EncodeToString(digest[:]))
	if err != nil {
		return nil, false, err
	}
	if isReplay {
		storedOrder, _ := replay["order"].(map[string]any)
		storedPayment, _ := replay["payment"].(map[string]any)
		return map[string]any{"order": storedOrder, "payment": storedPayment, "trackingToken": replay["trackingToken"], "trackingTokenExpiresAt": storedOrder["expiresAt"], "nextAction": "PAYMENT", "idempotentReplay": true}, true, nil
	}
	return map[string]any{"order": order, "payment": payment, "trackingToken": token, "trackingTokenExpiresAt": order["expiresAt"], "nextAction": "PAYMENT", "idempotentReplay": false}, false, nil
}

func (s *Service) TransitionOrder(ctx context.Context, entry map[string]any, paymentStatus, orderStatus string) (bool, error) {
	return s.repository.TransitionOrder(ctx, entry, paymentStatus, orderStatus)
}

func (s *Service) NewID(prefix string) (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", fmt.Errorf("generate operation id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(entropy[:]), nil
}

func (s *Service) CreateRecord(ctx context.Context, bucket string, record map[string]any) (map[string]any, error) {
	if bucket == "banners" && !validRemoteImage(record["imageUrl"]) {
		return nil, ErrInvalidRecord
	}
	if bucket == "bankAccounts" {
		if err := s.ValidateBankAccount(record); err != nil {
			return nil, err
		}
	}
	id, err := s.NewID(bucket)
	if err != nil {
		return nil, err
	}
	record["id"] = id
	record["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	primary := bucket == "bankAccounts" && record["isPrimary"] == true
	if bucket == "bankAccounts" {
		record["isPrimary"] = false
	}
	if err := s.repository.Put(ctx, bucket, id, record); err != nil {
		return nil, err
	}
	if primary {
		if err := s.SetPrimaryBankAccount(ctx, id); err != nil {
			return nil, err
		}
		record["isPrimary"] = true
	}
	return record, nil
}

func (s *Service) UpdateRecord(ctx context.Context, bucket, id string, patch map[string]any) (map[string]any, error) {
	current, err := s.repository.Get(ctx, bucket, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrRecordNotFound
	}
	for key, value := range patch {
		current[key] = value
	}
	current["id"] = id
	current["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	if bucket == "banners" && !validRemoteImage(current["imageUrl"]) {
		return nil, ErrInvalidRecord
	}
	primary := bucket == "bankAccounts" && current["isPrimary"] == true
	if bucket == "bankAccounts" {
		if err := s.ValidateBankAccount(current); err != nil {
			return nil, err
		}
		current["isPrimary"] = false
	}
	if err := s.repository.Put(ctx, bucket, id, current); err != nil {
		return nil, err
	}
	if primary {
		if err := s.SetPrimaryBankAccount(ctx, id); err != nil {
			return nil, err
		}
		current["isPrimary"] = true
	}
	return current, nil
}

func (s *Service) DeleteRecord(ctx context.Context, bucket, id string) error {
	return s.repository.Delete(ctx, bucket, id)
}

func (s *Service) SetPrimaryBankAccount(ctx context.Context, id string) error {
	accounts, err := s.repository.List(ctx, "bankAccounts")
	if err != nil {
		return err
	}
	var target map[string]any
	for _, row := range accounts {
		if row["id"] == id {
			target = row
			break
		}
	}
	if target == nil {
		return ErrRecordNotFound
	}
	if err := s.repository.ClearPrimaryBankAccounts(ctx); err != nil {
		return err
	}
	target["isPrimary"] = true
	return s.repository.Put(ctx, "bankAccounts", id, target)
}

func (s *Service) ValidateBankAccount(row map[string]any) error {
	for key, max := range map[string]int{"bankName": 80, "accountNumber": 40, "accountHolder": 100, "label": 80} {
		value, ok := row[key].(string)
		if !ok || strings.TrimSpace(value) != value || value == "" || len([]rune(value)) > max {
			return fmt.Errorf("%s tidak valid", key)
		}
	}
	if row["status"] != "Aktif" && row["status"] != "Nonaktif" {
		return errors.New("status harus Aktif atau Nonaktif")
	}
	return nil
}

func (s *Service) PaymentInstructions(ctx context.Context, method, paymentID string) map[string]any {
	result := map[string]any{"qrImageUrl": nil, "bankName": nil, "accountNumber": nil, "accountHolder": nil, "reference": "PENDING-" + paymentID}
	if method == "BCA_TRANSFER" {
		accounts, err := s.repository.List(ctx, "bankAccounts")
		if err == nil {
			for _, account := range accounts {
				if account["isPrimary"] == true && account["status"] == "Aktif" {
					result["bankName"], result["accountNumber"], result["accountHolder"] = account["bankName"], account["accountNumber"], account["accountHolder"]
					break
				}
			}
		}
	}
	if method == "QRIS" {
		settings, err := s.repository.GetSettings(ctx)
		if err == nil && validRemoteImage(settings["qrisImageUrl"]) {
			result["qrImageUrl"] = settings["qrisImageUrl"]
		}
	}
	return result
}

func (s *Service) CheckoutService(ctx context.Context, gameID, serviceID string) (catalog.Service, error) {
	if s.catalog == nil {
		return catalog.Service{}, errors.New("catalog unavailable")
	}
	return s.catalog.FindActiveServiceForCheckout(ctx, gameID, serviceID)
}

func (s *Service) HasGameCatalog() bool { return s.games != nil }

func (s *Service) NewTrackingToken() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

func (s *Service) FindGameBySlug(ctx context.Context, slug string) (string, string, bool, error) {
	if s.games == nil {
		return "", "", false, nil
	}
	for pageNumber := int64(1); pageNumber <= 1000; pageNumber++ {
		page, err := s.games.List(ctx, publicgame.Filter{Page: pageNumber, Limit: 100})
		if err != nil {
			return "", "", false, err
		}
		for _, game := range page.Items {
			if game.Slug == slug {
				return game.ID, game.Name, true, nil
			}
		}
		if pageNumber >= page.TotalPages || len(page.Items) == 0 {
			break
		}
	}
	return "", "", false, nil
}

func (s *Service) ValidateSettingsPatch(value map[string]any) error {
	if qris, exists := value["qrisImageUrl"]; exists && qris != nil && qris != "" && !validRemoteImage(qris) {
		return errors.New("qrisImageUrl harus berupa URL HTTPS publik.")
	}
	return nil
}

func validRemoteImage(value any) bool {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) != text || len(text) > 2048 {
		return false
	}
	parsed, err := url.ParseRequestURI(text)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func normalizePhone(phone string) string {
	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	value := digits.String()
	if strings.HasPrefix(value, "0") {
		return "62" + value[1:]
	}
	if strings.HasPrefix(value, "8") {
		return "62" + value
	}
	return value
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func validWhatsApp(value string) bool {
	if len(value) > 25 || strings.TrimSpace(value) != value || value == "" {
		return false
	}
	if strings.Contains(value, "+") && !strings.HasPrefix(value, "+") {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != '+' && r != ' ' && r != '-' && r != '(' && r != ')' {
			return false
		}
	}
	digits := normalizePhone(value)
	if len(digits) < 9 || len(digits) > 15 {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) ReportPayment(ctx context.Context, entry map[string]any) error {
	order, _ := entry["order"].(map[string]any)
	payment, _ := entry["payment"].(map[string]any)
	status, orderStatus := stringField(order, "paymentStatus"), stringField(order, "orderStatus")
	if status == string(PaymentPendingReview) {
		return nil
	}
	if !((status == string(PaymentPending) && orderStatus == "AWAITING_PAYMENT") || (status == string(PaymentFailed) && orderStatus == "AWAITING_PAYMENT")) {
		return ErrInvalidOrderTransition
	}
	order["paymentStatus"], order["orderStatus"], payment["status"] = string(PaymentPendingReview), string(PaymentPendingReview), string(PaymentPendingReview)
	ok, err := s.repository.TransitionOrder(ctx, entry, status, orderStatus)
	if err != nil {
		return err
	}
	if !ok {
		return ErrOrderTransitionConflict
	}
	return nil
}

func (s *Service) ReviewPayment(ctx context.Context, entry map[string]any, decision string) (string, string, error) {
	if decision != "APPROVE" && decision != "REJECT" {
		return "", "", ErrInvalidPaymentDecision
	}
	order, _ := entry["order"].(map[string]any)
	payment, _ := entry["payment"].(map[string]any)
	if stringField(order, "paymentStatus") != string(PaymentPendingReview) || stringField(order, "orderStatus") != string(PaymentPendingReview) {
		return "", "", ErrInvalidOrderTransition
	}
	paymentStatus, orderStatus := string(PaymentFailed), "AWAITING_PAYMENT"
	if decision == "APPROVE" {
		paymentStatus, orderStatus = string(PaymentPaid), "IN_PROGRESS"
	}
	order["paymentStatus"], order["orderStatus"], payment["status"] = paymentStatus, orderStatus, paymentStatus
	ok, err := s.repository.TransitionOrder(ctx, entry, string(PaymentPendingReview), string(PaymentPendingReview))
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrOrderTransitionConflict
	}
	return paymentStatus, orderStatus, nil
}

func (s *Service) CompleteOrder(ctx context.Context, entry map[string]any) error {
	order, _ := entry["order"].(map[string]any)
	if stringField(order, "paymentStatus") != string(PaymentPaid) || stringField(order, "orderStatus") != "IN_PROGRESS" {
		return ErrInvalidOrderTransition
	}
	order["orderStatus"] = "COMPLETED"
	ok, err := s.repository.TransitionOrder(ctx, entry, string(PaymentPaid), "IN_PROGRESS")
	if err != nil {
		return err
	}
	if !ok {
		return ErrOrderTransitionConflict
	}
	return nil
}

func (s *Service) ReviewOrderPayment(ctx context.Context, orderID, paymentID, decision string) (string, string, error) {
	if decision != "APPROVE" && decision != "REJECT" {
		return "", "", ErrInvalidPaymentDecision
	}
	entry, err := s.repository.FindOrder(ctx, bson.D{{Key: "order.id", Value: orderID}})
	if err != nil {
		return "", "", err
	}
	if entry == nil {
		return "", "", ErrOrderNotFound
	}
	order, _ := entry["order"].(map[string]any)
	if stringField(order, "paymentId") != paymentID {
		return "", "", ErrPaymentNotFound
	}
	return s.ReviewPayment(ctx, entry, decision)
}

func (s *Service) CompleteOrderByID(ctx context.Context, id string) error {
	entry, err := s.repository.FindOrder(ctx, bson.D{{Key: "order.id", Value: id}})
	if err != nil {
		return err
	}
	if entry == nil {
		entry, err = s.repository.FindOrder(ctx, bson.D{{Key: "order.orderNumber", Value: id}})
		if err != nil {
			return err
		}
	}
	if entry == nil {
		return ErrOrderNotFound
	}
	return s.CompleteOrder(ctx, entry)
}
