package operation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/catalog"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCheckoutRejectsUnpricedCustomServiceAndUnsupportedPayment(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"custom service", `{"gameSlug":"mobile-legends","serviceId":"custom","paymentMethod":"QRIS","quantity":1}`, http.StatusUnprocessableEntity},
		{"unsupported payment", `{"gameSlug":"mobile-legends","serviceId":"priced","paymentMethod":"CASH","quantity":1}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHandler(checkoutCatalogFake{services: map[string]catalog.Service{
				"custom": {ID: "custom", Name: "Custom", PriceAmount: nil, IsActive: true},
				"priced": {ID: "priced", Name: "Rank", PriceAmount: testPrice(25000), IsActive: true},
			}})
			req := httptest.NewRequest(http.MethodPost, "/api/orders/v1/checkout", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			h.orders(res, req)
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", res.Code, tc.status, res.Body.String())
			}
		})
	}
}

func TestCheckoutRejectsMissingRequiredFieldsWithoutCreatingOrder(t *testing.T) {
	h, repo := newTestHandler(checkoutCatalogFake{services: map[string]catalog.Service{"svc": {ID: "svc", Name: "Rank", PriceAmount: testPrice(25000), IsActive: true}}})
	response := checkoutRequest(h, "missing-fields-key-001", `{"gameSlug":"mobile-legends","serviceId":"svc","paymentMethod":"QRIS","quantity":1,"customer":{"name":"Test"},"item":{}}`)
	if response.Code != http.StatusBadRequest || repo.OrderCount() != 0 {
		t.Fatalf("missing checkout fields accepted or persisted: status=%d orders=%d body=%s", response.Code, repo.OrderCount(), response.Body.String())
	}
}

func TestHandlerWithoutServiceFailsClosed(t *testing.T) {
	h := NewHandler(nil)
	response := httptest.NewRecorder()
	h.publicSettings(response, httptest.NewRequest(http.MethodGet, "/api/settings/v1/public", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", response.Code)
	}
}

func TestPublicSettingsResponseUsesCanonicalEnvelopeOrder(t *testing.T) {
	mux := http.NewServeMux()
	h, _ := newTestHandler(nil)
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	request := httptest.NewRequest(http.MethodGet, "/api/settings/v1/public", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	codeAt := strings.Index(body, `"code"`)
	messageAt := strings.Index(body, `"message"`)
	dataAt := strings.Index(body, `"data"`)
	if codeAt < 0 || messageAt <= codeAt || dataAt <= messageAt {
		t.Fatalf("outer response keys are not ordered code, message, data: %s", body)
	}
}

func TestCustomerPaymentReportOnlySetsPendingReview(t *testing.T) {
	h, repo := newTestHandler(nil)
	entry := testOrderEntry("JH-20261101-000001", "AWAITING_PAYMENT", "PENDING")
	repo.AddOrder(entry)
	req := httptest.NewRequest(http.MethodPost, "/api/orders/v1/tracking/JH-20261101-000001/payment", nil)
	req.Header.Set("Authorization", "Bearer tracking-token")
	res := httptest.NewRecorder()
	h.orders(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	stored, _ := repo.FindOrder(context.Background(), bson.D{{Key: "order.orderNumber", Value: "JH-20261101-000001"}})
	order := stored["order"].(map[string]any)
	payment := stored["payment"].(map[string]any)
	if order["paymentStatus"] != "PENDING_REVIEW" || order["orderStatus"] != "PENDING_REVIEW" || payment["status"] != "PENDING_REVIEW" {
		t.Fatalf("customer report set unexpected statuses: order=%v payment=%v", order, payment)
	}
}

func TestCheckoutIssuesOpaqueTrackingTokens(t *testing.T) {
	h, _ := newTestHandler(checkoutCatalogFake{services: map[string]catalog.Service{"svc": {ID: "svc", Name: "Rank", PriceAmount: testPrice(25000), IsActive: true}}})
	body := `{"gameSlug":"mobile-legends","serviceId":"svc","paymentMethod":"QRIS","quantity":1,"customer":{"name":"Test","whatsapp":"+628123456789"},"item":{"gameAccount":"account"}}`
	first := checkoutRequest(h, "request-000000001", body)
	second := checkoutRequest(h, "request-000000002", body)
	firstToken, _ := decodeData(t, first)["trackingToken"].(string)
	secondToken, _ := decodeData(t, second)["trackingToken"].(string)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("checkout status: %d, %d", first.Code, second.Code)
	}
	firstBytes, err := base64.RawURLEncoding.DecodeString(firstToken)
	if err != nil || len(firstBytes) != 32 {
		t.Fatalf("tracking token is not 256-bit base64url: token=%q decoded=%d err=%v", firstToken, len(firstBytes), err)
	}
	if strings.HasPrefix(firstToken, "track-") || firstToken == secondToken {
		t.Fatalf("tracking tokens are predictable or reused: %q %q", firstToken, secondToken)
	}
}

func TestCheckoutIdempotencyKeyRejectsDifferentPayload(t *testing.T) {
	h, repo := newTestHandler(checkoutCatalogFake{services: map[string]catalog.Service{"svc": {ID: "svc", Name: "Rank", PriceAmount: testPrice(25000), IsActive: true}}})
	body := `{"gameSlug":"mobile-legends","serviceId":"svc","paymentMethod":"QRIS","quantity":1,"customer":{"name":"Test","whatsapp":"+628123456789"},"item":{"gameAccount":"account"}}`
	first := checkoutRequest(h, "same-idempotency-key", body)
	replay := checkoutRequest(h, "same-idempotency-key", body)
	if first.Code != http.StatusCreated || replay.Code != http.StatusOK || repo.OrderCount() != 1 {
		t.Fatalf("identical key replay failed: first=%d replay=%d orders=%d", first.Code, replay.Code, repo.OrderCount())
	}
	changed := strings.Replace(body, "\"quantity\":1", "\"quantity\":2", 1)
	conflict := checkoutRequest(h, "same-idempotency-key", changed)
	if conflict.Code != http.StatusConflict || repo.OrderCount() != 1 {
		t.Fatalf("different payload reused key: status=%d orders=%d body=%s", conflict.Code, repo.OrderCount(), conflict.Body.String())
	}
}

func TestTrackingLookupIsRateLimited(t *testing.T) {
	h, _ := newTestHandler(nil)
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/orders/v1/tracking/lookup", strings.NewReader(`{"orderNumber":"missing","whatsapp":"08123456789"}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		h.orders(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("request %d status=%d", i+1, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orders/v1/tracking/lookup", strings.NewReader(`{"orderNumber":"missing","whatsapp":"08123456789"}`))
	res := httptest.NewRecorder()
	h.orders(res, req)
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("21st lookup status=%d want %d", res.Code, http.StatusTooManyRequests)
	}
}

func checkoutRequest(h *Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/orders/v1/checkout", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	res := httptest.NewRecorder()
	h.orders(res, req)
	return res
}

func TestAdminPaymentReviewValidatesAndSynchronizesStatuses(t *testing.T) {
	for _, tc := range []struct {
		name, decision, paymentStatus, wantPayment, wantOrder string
		wantHTTP                                              int
	}{
		{"approve", "APPROVE", "PENDING_REVIEW", "PAID", "IN_PROGRESS", http.StatusOK},
		{"reject", "REJECT", "PENDING_REVIEW", "FAILED", "AWAITING_PAYMENT", http.StatusOK},
		{"invalid decision", "CANCEL", "PENDING_REVIEW", "PENDING_REVIEW", "PENDING_REVIEW", http.StatusBadRequest},
		{"duplicate decision", "APPROVE", "PAID", "PAID", "IN_PROGRESS", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newTestHandler(nil)
			initialOrderStatus := tc.paymentStatus
			if tc.name == "duplicate decision" {
				initialOrderStatus = "IN_PROGRESS"
			}
			entry := testOrderEntry("JH-20261101-000002", initialOrderStatus, tc.paymentStatus)
			repo.AddOrder(entry)
			body := `{"orderId":"order-1","paymentId":"payment-1","decision":"` + tc.decision + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/admin/orders/v1/payment-review", strings.NewReader(body))
			res := httptest.NewRecorder()
			h.mongoAdminOrder(res, req)
			if res.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d; body=%s", res.Code, tc.wantHTTP, res.Body.String())
			}
			stored, _ := repo.FindOrder(context.Background(), bson.D{{Key: "order.id", Value: "order-1"}})
			order := stored["order"].(map[string]any)
			payment := stored["payment"].(map[string]any)
			if order["paymentStatus"] != tc.wantPayment || payment["status"] != tc.wantPayment || order["orderStatus"] != tc.wantOrder {
				t.Fatalf("statuses not synchronized: order=%v payment=%v", order, payment)
			}
		})
	}
}

func TestAdminCompleteRequiresPaidInProgressOrder(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		paymentStatus, orderStatus, wantStatus string
		wantHTTP                               int
	}{
		{"unpaid blocked", "PENDING", "AWAITING_PAYMENT", "AWAITING_PAYMENT", http.StatusConflict},
		{"paid can complete", "PAID", "IN_PROGRESS", "COMPLETED", http.StatusOK},
		{"duplicate completion blocked", "PAID", "COMPLETED", "COMPLETED", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newTestHandler(nil)
			entry := testOrderEntry("JH-20261101-000003", tc.orderStatus, tc.paymentStatus)
			repo.AddOrder(entry)
			req := httptest.NewRequest(http.MethodPost, "/api/admin/orders/v1/complete/order-1", nil)
			res := httptest.NewRecorder()
			h.mongoAdminOrder(res, req)
			if res.Code != tc.wantHTTP {
				t.Fatalf("status = %d, want %d; body=%s", res.Code, tc.wantHTTP, res.Body.String())
			}
			stored, _ := repo.FindOrder(context.Background(), bson.D{{Key: "order.id", Value: "order-1"}})
			order := stored["order"].(map[string]any)
			if order["orderStatus"] != tc.wantStatus {
				t.Fatalf("order status=%v, want %s", order["orderStatus"], tc.wantStatus)
			}
		})
	}
}

type staticAuthData struct {
	profile    auth.AdminProfile
	csrfDigest string
}

func (d staticAuthData) FindLoginIdentity(context.Context, string, string) (auth.LoginIdentity, error) {
	return auth.LoginIdentity{}, auth.ErrNotFound
}
func (d staticAuthData) CreateLoginSession(context.Context, string, auth.Session) (auth.AdminProfile, error) {
	return d.profile, nil
}
func (d staticAuthData) FindSessionByRefreshDigest(context.Context, string) (auth.Session, error) {
	return auth.Session{}, auth.ErrNotFound
}
func (d staticAuthData) RotateSession(context.Context, string, string, auth.Session, time.Time) (auth.RefreshResult, error) {
	return auth.RefreshResult{}, nil
}
func (d staticAuthData) RevokeSessionFamily(context.Context, string, string, time.Time) error {
	return nil
}
func (d staticAuthData) ResolveAccess(context.Context, string, string, time.Time) (auth.AdminProfile, bool, error) {
	return d.profile, true, nil
}
func (d staticAuthData) ValidateCSRF(_ context.Context, _ string, digest string) (bool, error) {
	return digest != "" && digest == d.csrfDigest, nil
}
func (d staticAuthData) RevokeAccountSessions(context.Context, string, string, time.Time) error {
	return nil
}
func (d staticAuthData) Allow(context.Context, string, int, time.Time) (time.Duration, bool, error) {
	return 0, true, nil
}
func (d staticAuthData) Check(context.Context, string, int, time.Time) (time.Duration, bool, error) {
	return 0, true, nil
}

func TestAdminStaticRoutesRequireSuperAdminAndCSRF(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile auth.AdminProfile
		csrf    string
		want    int
	}{
		{"non-super-admin", auth.AdminProfile{ID: "admin-1", Role: auth.Role{Code: "OPERATOR", System: false}}, "csrf", http.StatusForbidden},
		{"missing csrf", auth.AdminProfile{ID: "admin-1", Role: auth.Role{Code: "SUPER_ADMIN", System: true}}, "", http.StatusForbidden},
		{"super-admin csrf", auth.AdminProfile{ID: "admin-1", Role: auth.Role{Code: "SUPER_ADMIN", System: true}}, "csrf", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := auth.TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "test-issuer", Audience: "test-audience"}
			token, err := manager.IssueAccess("admin-1", "family-1", time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			data := staticAuthData{profile: tc.profile, csrfDigest: utils.DigestToken("csrf")}
			adminAuth := auth.NewAdminAuthHandler(nil, data, manager, nil, nil, nil, true)
			mux := http.NewServeMux()
			h, _ := newTestHandler(nil)
			h.Register(mux, adminAuth.Middleware)
			req := httptest.NewRequest(http.MethodGet, "/api/admin/settings/v1/general", nil)
			req.AddCookie(&http.Cookie{Name: "bongastore-admin-access", Value: token})
			if tc.csrf != "" {
				req.Header.Set("X-CSRF-Token", tc.csrf)
			}
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", res.Code, tc.want, res.Body.String())
			}
		})
	}
}

func testOrderEntry(number, orderStatus, paymentStatus string) map[string]any {
	order := map[string]any{"id": "order-1", "paymentId": "payment-1", "orderNumber": number, "orderStatus": orderStatus, "paymentStatus": paymentStatus, "totalAmount": float64(25000), "expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "gameName": "Mobile Legends", "serviceName": "Rank", "quantity": 1, "customerWhatsapp": "+628123456789", "createdAt": time.Now().UTC().Format(time.RFC3339)}
	payment := map[string]any{"id": "payment-1", "method": "QRIS", "status": paymentStatus, "amount": float64(25000), "instructions": map[string]any{"qrImageUrl": nil, "bankName": nil, "accountNumber": nil, "accountHolder": nil, "reference": "PENDING-payment-1"}}
	return map[string]any{"order": order, "payment": payment, "trackingToken": "tracking-token"}
}

func decodeData(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

type checkoutCatalogFake struct{ services map[string]catalog.Service }

func (fake checkoutCatalogFake) FindActiveServiceForCheckout(_ context.Context, gameID, serviceID string) (catalog.Service, error) {
	service, ok := fake.services[serviceID]
	if !ok || !service.IsActive || gameID != "" && service.GameID != "" && service.GameID != gameID {
		return catalog.Service{}, catalog.ErrServiceNotFound
	}
	return service, nil
}

func testPrice(value int64) *int64 { return &value }

func newTestHandler(catalogStore CheckoutCatalog) (*Handler, *fakeRepository) {
	repository := newFakeRepository()
	service := NewService(repository, nil, catalogStore)
	return NewHandler(service), repository
}
