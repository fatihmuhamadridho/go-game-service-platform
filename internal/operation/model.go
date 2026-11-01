package operation

import (
	"errors"
	"time"
)

var (
	ErrOrderTransitionConflict = errors.New("order status changed concurrently")
	ErrInvalidPaymentDecision  = errors.New("payment decision must be APPROVE or REJECT")
	ErrInvalidOrderTransition  = errors.New("order is not in a valid state for this action")
	ErrInvalidCheckout         = errors.New("invalid checkout request")
	ErrUnpricedCheckout        = errors.New("checkout service is not priced")
	ErrUnavailableCheckout     = errors.New("checkout game or service is unavailable")
	ErrUnsupportedPayment      = errors.New("unsupported payment method")
)

// PaymentState is the persisted manual-payment lifecycle for an order.
type PaymentState string

const (
	PaymentPending       PaymentState = "PENDING"
	PaymentPendingReview PaymentState = "PENDING_REVIEW"
	PaymentPaid          PaymentState = "PAID"
	PaymentFailed        PaymentState = "FAILED"
)

// BankAccountRecord is persisted in MongoDB. Collections are intentionally
// empty until Bongastore supplies real account details.
type BankAccountRecord struct {
	ID            string `json:"id" bson:"id"`
	BankName      string `json:"bankName" bson:"bankName"`
	AccountNumber string `json:"accountNumber" bson:"accountNumber"`
	AccountHolder string `json:"accountHolder" bson:"accountHolder"`
	Label         string `json:"label" bson:"label"`
	Status        string `json:"status" bson:"status"`
	IsPrimary     bool   `json:"isPrimary" bson:"isPrimary"`
}

type OrderRecord map[string]any

func DefaultSettings() map[string]any {
	return map[string]any{
		"siteName": "Bongastore", "siteDescription": "Layanan game untuk kebutuhan digital.",
		"contactEmail": "", "contactWhatsapp": "", "socialLinks": []any{},
		"businessHours": "", "maintenanceMode": false, "allowRegistration": false,
		"defaultCurrency": "IDR", "timezone": "Asia/Jakarta",
	}
}

func DefaultAboutPage() map[string]any {
	return map[string]any{
		"id": "page-about", "title": "Tentang Bongastore", "slug": "tentang-kami",
		"summary": "Bongastore adalah konsep storefront layanan game yang membantu pemain menemukan pilihan top up, joki, voucher, dan kebutuhan digital dalam satu pengalaman.",
		"content": "Menemukan layanan yang tepat bisa terasa membingungkan. Bongastore dirancang untuk mengumpulkan berbagai kebutuhan gaming dalam tampilan yang ringkas, membantu kamu membandingkan pilihan dan melanjutkan pemesanan dengan lebih mudah.\n\nSaat ini Bongastore masih berupa prototipe. Katalog, harga, estimasi, jaminan, dan informasi kontak perlu dikonfirmasi sebelum digunakan sebagai penawaran resmi.",
		"status":  "Published", "updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
}
