package model

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type GameCatalog struct {
	ID             bson.ObjectID `bson:"_id"`
	Name           string        `bson:"name"`
	Slug           string        `bson:"slug"`
	ServiceSummary *string       `bson:"service_summary"`
	ArtworkKey     *string       `bson:"artwork_key"`
	IsActive       bool          `bson:"is_active"`
	IsPopular      bool          `bson:"is_popular"`
	SortOrder      int64         `bson:"sort_order"`
	CreatedAt      time.Time     `bson:"created_at"`
	UpdatedAt      time.Time     `bson:"updated_at"`
	DeletedAt      *time.Time    `bson:"deleted_at"`
}

type ServiceCategory struct {
	ID          bson.ObjectID `bson:"_id"`
	Name        string        `bson:"name"`
	Slug        string        `bson:"slug"`
	Description *string       `bson:"description"`
	IsActive    bool          `bson:"is_active"`
	CreatedAt   time.Time     `bson:"created_at"`
	UpdatedAt   time.Time     `bson:"updated_at"`
	DeletedAt   *time.Time    `bson:"deleted_at"`
}

type GameService struct {
	ID            bson.ObjectID    `bson:"_id"`
	GameID        bson.ObjectID    `bson:"game_id"`
	CategoryID    bson.ObjectID    `bson:"category_id"`
	Name          string           `bson:"name"`
	PriceAmount   *bson.Decimal128 `bson:"price_amount"`
	IsCustomPrice bool             `bson:"is_custom_price"`
	EstimateText  string           `bson:"estimate_text"`
	IsActive      bool             `bson:"is_active"`
	CreatedAt     time.Time        `bson:"created_at"`
	UpdatedAt     time.Time        `bson:"updated_at"`
	DeletedAt     *time.Time       `bson:"deleted_at"`
}

type PlatformSettings struct {
	ID                bson.ObjectID  `bson:"_id,omitempty"`
	SingletonKey      string         `bson:"singleton_key"`
	SiteName          string         `bson:"site_name"`
	SiteDescription   *string        `bson:"site_description"`
	ContactEmail      string         `bson:"contact_email"`
	ContactWhatsApp   string         `bson:"contact_whatsapp"`
	InstagramURL      *string        `bson:"instagram_url"`
	SocialLinks       any            `bson:"social_links"`
	BusinessHours     *string        `bson:"business_hours"`
	MaintenanceMode   bool           `bson:"maintenance_mode"`
	AllowRegistration bool           `bson:"allow_registration"`
	DefaultCurrency   string         `bson:"default_currency"`
	Timezone          string         `bson:"timezone"`
	Version           int32          `bson:"version"`
	UpdatedAt         time.Time      `bson:"updated_at"`
	CreatedAt         time.Time      `bson:"created_at"`
	UpdatedBy         *bson.ObjectID `bson:"updated_by"`
}

type FinancialTransaction struct {
	ID                      bson.ObjectID   `bson:"_id"`
	TransactionNumber       string          `bson:"transaction_number"`
	OrderID                 bson.ObjectID   `bson:"order_id"`
	OrderPaymentID          *bson.ObjectID  `bson:"order_payment_id"`
	RefundRequestID         *bson.ObjectID  `bson:"refund_request_id"`
	Type                    string          `bson:"type"`
	Method                  string          `bson:"method"`
	Amount                  bson.Decimal128 `bson:"amount"`
	Currency                string          `bson:"currency"`
	Status                  string          `bson:"status"`
	ProviderReference       *string         `bson:"provider_reference"`
	CustomerNameSnapshot    *string         `bson:"customer_name_snapshot"`
	CustomerContactSnapshot *string         `bson:"customer_contact_snapshot"`
	CreatedAt               time.Time       `bson:"created_at"`
	UpdatedAt               time.Time       `bson:"updated_at"`
	SettledAt               *time.Time      `bson:"settled_at"`
}
type TransactionStatusHistory struct {
	ID            bson.ObjectID  `bson:"_id"`
	TransactionID bson.ObjectID  `bson:"transaction_id"`
	OldStatus     *string        `bson:"old_status"`
	NewStatus     string         `bson:"new_status"`
	ChangedBy     *bson.ObjectID `bson:"changed_by"`
	Reason        *string        `bson:"reason"`
	ChangedAt     time.Time      `bson:"changed_at"`
}
type ActivityLog struct {
	ID              bson.ObjectID `bson:"_id"`
	ActorName       *string       `bson:"actor_name_snapshot"`
	ActorEmail      *string       `bson:"actor_email_snapshot"`
	ActionLabel     string        `bson:"action_label"`
	TargetReference *string       `bson:"target_reference"`
	TargetType      string        `bson:"target_type"`
	Category        string        `bson:"category"`
	SourceIP        *string       `bson:"source_ip"`
	Result          string        `bson:"result"`
	OccurredAt      time.Time     `bson:"occurred_at"`
}
type AdminReadRepository interface {
	ListTransactions(context.Context) ([]FinancialTransaction, error)
	FindTransaction(context.Context, bson.ObjectID) (*FinancialTransaction, error)
	ListTransactionHistory(context.Context, bson.ObjectID) ([]TransactionStatusHistory, error)
	ListActivity(context.Context) ([]ActivityLog, error)
	ListAllOrders(context.Context) ([]Order, error)
	ListAllOrderItems(context.Context) ([]OrderItem, error)
	ListAllUsers(context.Context) ([]Account, error)
	ListAllGameServices(context.Context) ([]GameService, error)
	UpsertSettings(context.Context, PlatformSettings) error
	RecordSettingsActivity(context.Context, bson.ObjectID, PlatformSettings, SettingsActivityContext) error
	ListSettings(context.Context) (*PlatformSettings, error)
}

type SettingsActivityContext struct {
	RequestID     string
	SourceIP      string
	UserAgent     string
	ChangedFields map[string]any
	TargetID      *bson.ObjectID
}

type AdminOrderRepository interface {
	ListOrderGraphs(context.Context) ([]Order, []OrderItem, []OrderPayment, error)
	GetOrderGraph(context.Context, bson.ObjectID) (*Order, []OrderItem, []OrderPayment, []OrderStatusHistory, []PaymentStatusHistory, []PaymentProof, error)
	ReviewPayment(context.Context, bson.ObjectID, bson.ObjectID, string, *string, bson.ObjectID) (string, string, error)
	CompleteOrder(context.Context, bson.ObjectID, bson.ObjectID, time.Time) error
}
type PaymentProofRepository interface {
	FindPaymentProof(context.Context, bson.ObjectID) (*PaymentProof, error)
	ListPaymentProofs(context.Context, bson.ObjectID) ([]PaymentProof, error)
	FinalizeProof(context.Context, bson.ObjectID, string, string, string, int64, time.Time) (bson.ObjectID, error)
}
type PrivateObjectStorage interface {
	Configured() bool
	ClientUploadToken(string, string, string, time.Time) (string, error)
	VerifyUploadCallback([]byte, string) bool
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte, string) (string, error)
	Delete(context.Context, string) error
}

type Order struct {
	ID               bson.ObjectID   `bson:"_id"`
	OrderNumber      string          `bson:"order_number"`
	UserID           *bson.ObjectID  `bson:"user_id"`
	CustomerName     string          `bson:"customer_name"`
	CustomerWhatsApp string          `bson:"customer_whatsapp"`
	OrderStatus      string          `bson:"order_status"`
	PaymentStatus    string          `bson:"payment_status"`
	SubtotalAmount   bson.Decimal128 `bson:"subtotal_amount"`
	DiscountAmount   bson.Decimal128 `bson:"discount_amount"`
	TotalAmount      bson.Decimal128 `bson:"total_amount"`
	Currency         string          `bson:"currency"`
	ExpiresAt        *time.Time      `bson:"expires_at"`
	CreatedAt        time.Time       `bson:"created_at"`
	UpdatedAt        time.Time       `bson:"updated_at"`
}

type OrderItem struct {
	ID                   bson.ObjectID    `bson:"_id"`
	OrderID              bson.ObjectID    `bson:"order_id"`
	GameID               bson.ObjectID    `bson:"game_id"`
	CategoryID           bson.ObjectID    `bson:"category_id"`
	ServiceID            bson.ObjectID    `bson:"service_id"`
	GameNameSnapshot     string           `bson:"game_name_snapshot"`
	ServiceNameSnapshot  string           `bson:"service_name_snapshot"`
	CategoryNameSnapshot *string          `bson:"category_name_snapshot"`
	GameAccount          string           `bson:"game_account"`
	GameServer           *string          `bson:"game_server"`
	CustomerNotes        *string          `bson:"customer_notes"`
	Quantity             int32            `bson:"quantity"`
	UnitPrice            *bson.Decimal128 `bson:"unit_price"`
	LineTotal            *bson.Decimal128 `bson:"line_total"`
	EstimateSnapshot     string           `bson:"estimate_snapshot"`
}

type OrderPayment struct {
	ID            bson.ObjectID   `bson:"_id"`
	OrderID       bson.ObjectID   `bson:"order_id"`
	Method        string          `bson:"method"`
	Status        string          `bson:"status"`
	Amount        bson.Decimal128 `bson:"amount"`
	ReferenceNote string          `bson:"reference_note"`
	SubmittedAt   *time.Time      `bson:"submitted_at"`
	ReviewedBy    *bson.ObjectID  `bson:"reviewed_by"`
	ReviewedAt    *time.Time      `bson:"reviewed_at"`
	ReviewNote    *string         `bson:"review_note"`
	CreatedAt     time.Time       `bson:"created_at"`
	UpdatedAt     time.Time       `bson:"updated_at"`
}

type PaymentProof struct {
	ID            bson.ObjectID  `bson:"_id"`
	PaymentID     bson.ObjectID  `bson:"payment_id"`
	ObjectKey     string         `bson:"object_key"`
	MimeType      string         `bson:"mime_type"`
	FileSizeBytes int32          `bson:"file_size_bytes"`
	UploadedAt    time.Time      `bson:"uploaded_at"`
	UploadedBy    *bson.ObjectID `bson:"uploaded_by"`
}

type OrderStatusHistory struct {
	ID        bson.ObjectID  `bson:"_id"`
	OrderID   bson.ObjectID  `bson:"order_id"`
	OldStatus *string        `bson:"old_status"`
	NewStatus string         `bson:"new_status"`
	ChangedBy *bson.ObjectID `bson:"changed_by"`
	Reason    *string        `bson:"reason"`
	ChangedAt time.Time      `bson:"changed_at"`
}

type PaymentStatusHistory struct {
	ID        bson.ObjectID  `bson:"_id"`
	PaymentID bson.ObjectID  `bson:"payment_id"`
	OldStatus *string        `bson:"old_status"`
	NewStatus string         `bson:"new_status"`
	ChangedBy *bson.ObjectID `bson:"changed_by"`
	Reason    *string        `bson:"reason"`
	ChangedAt time.Time      `bson:"changed_at"`
}

type CheckoutIdempotency struct {
	ID           bson.ObjectID `bson:"_id"`
	KeyHash      string        `bson:"key_hash"`
	RequestHash  string        `bson:"request_hash"`
	ResponseJSON any           `bson:"response_json"`
	CreatedAt    time.Time     `bson:"created_at"`
	ExpiresAt    time.Time     `bson:"expires_at"`
}

type OrderTrackingToken struct {
	ID        bson.ObjectID `bson:"_id"`
	TokenHash string        `bson:"token_hash"`
	OrderID   bson.ObjectID `bson:"order_id"`
	ExpiresAt time.Time     `bson:"expires_at"`
	RevokedAt *time.Time    `bson:"revoked_at"`
	CreatedAt time.Time     `bson:"created_at"`
}

type CatalogRepository interface {
	ListActiveGames(context.Context) ([]GameCatalog, error)
	FindActiveGameBySlug(context.Context, string) (*GameCatalog, error)
	ListActiveCategories(context.Context) ([]ServiceCategory, error)
	ListActiveServices(context.Context, *bson.ObjectID) ([]GameService, error)
	GetPublicSettings(context.Context) (*PlatformSettings, error)
}

type OptionalString struct {
	Set   bool
	Value *string
}
type GamePatch struct {
	Name           *string
	Slug           *string
	ServiceSummary OptionalString
	ArtworkKey     OptionalString
	IsActive       *bool
	IsPopular      *bool
	SortOrder      *int64
}
type ActivityContext struct {
	ActorID   bson.ObjectID
	RequestID string
	SourceIP  string
	UserAgent string
}

type AdminGameRepository interface {
	ListAdminGames(context.Context) ([]GameCatalog, error)
	FindGameByID(context.Context, bson.ObjectID) (*GameCatalog, error)
	GameNameOrSlugExists(context.Context, string, string, bson.ObjectID) (bool, error)
	CreateGame(context.Context, GameCatalog) (*GameCatalog, error)
	UpdateGame(context.Context, bson.ObjectID, GamePatch) error
	SoftDeleteGame(context.Context, bson.ObjectID, time.Time) error
	CountActiveServices(context.Context, bson.ObjectID) (int, error)
	RecordCatalogActivity(context.Context, ActivityContext, string, string, bson.ObjectID, map[string]any) error
}

type AdminCatalogRepository interface {
	ListAllCategories(context.Context) ([]ServiceCategory, error)
	FindCategoryByID(context.Context, bson.ObjectID) (*ServiceCategory, error)
	CountServicesByCategory(context.Context, bson.ObjectID, bool) (int, error)
	CategoryNameOrSlugExists(context.Context, string, string, bson.ObjectID) (bool, error)
	CreateCategory(context.Context, ServiceCategory) error
	UpdateCategory(context.Context, bson.ObjectID, map[string]any) error
	SoftDeleteCategory(context.Context, bson.ObjectID, time.Time) error
	ListAllServices(context.Context) ([]GameService, error)
	FindServiceByID(context.Context, bson.ObjectID) (*GameService, error)
	CreateService(context.Context, GameService) error
	UpdateService(context.Context, bson.ObjectID, map[string]any) error
	SoftDeleteService(context.Context, bson.ObjectID, time.Time) error
	RecordCatalogActivity(context.Context, ActivityContext, string, string, bson.ObjectID, map[string]any) error
}

type OrderRepository interface {
	FindActiveGameBySlugForOrder(context.Context, string) (*GameCatalog, error)
	GetCheckoutSettings(context.Context) (*PlatformSettings, error)
	FindCheckoutService(context.Context, bson.ObjectID, bson.ObjectID) (*GameService, *GameCatalog, *ServiceCategory, error)
	FindIdempotency(context.Context, string) (*CheckoutIdempotency, error)
	CreateIdempotency(context.Context, CheckoutIdempotency) error
	CompleteIdempotency(context.Context, bson.ObjectID, any) error
	DeleteIdempotency(context.Context, bson.ObjectID) error
	CreateTrackingToken(context.Context, OrderTrackingToken) error
	FindTrackingToken(context.Context, string) (*OrderTrackingToken, error)
	CreateCheckout(context.Context, Order, OrderItem, OrderPayment, OrderStatusHistory, PaymentStatusHistory) error
	FindPaymentOrder(context.Context, bson.ObjectID) (*OrderPayment, *Order, error)
	SetPaymentPendingReview(context.Context, bson.ObjectID, time.Time) (bool, error)
	UpdateOrderPaymentStatus(context.Context, bson.ObjectID, string, string, time.Time) error
	CreateOrderHistory(context.Context, OrderStatusHistory) error
	CreatePaymentHistory(context.Context, PaymentStatusHistory) error
	RollbackPaymentReport(context.Context, bson.ObjectID, bson.ObjectID, bson.ObjectID, bson.ObjectID, string, string, time.Time) error
	FindOrderByNumber(context.Context, string) (*Order, error)
	GetOrderGraph(context.Context, bson.ObjectID) (*Order, []OrderItem, []OrderPayment, []OrderStatusHistory, []PaymentStatusHistory, error)
}
