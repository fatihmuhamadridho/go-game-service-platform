package operation

type CheckoutRequest struct {
	GameSlug      string           `json:"gameSlug"`
	ServiceID     string           `json:"serviceId"`
	PaymentMethod string           `json:"paymentMethod"`
	Quantity      int              `json:"quantity"`
	Customer      CheckoutCustomer `json:"customer"`
	Item          CheckoutItem     `json:"item"`
	CustomerNotes string           `json:"customerNotes"`
}

type CheckoutCustomer struct {
	Name     string `json:"name"`
	WhatsApp string `json:"whatsapp"`
}

type CheckoutItem struct {
	GameAccount string `json:"gameAccount"`
	GameServer  string `json:"gameServer"`
}

type PaymentReviewRequest struct {
	OrderID   string `json:"orderId"`
	PaymentID string `json:"paymentId"`
	Decision  string `json:"decision"`
}

type TrackingLookupRequest struct {
	OrderNumber string `json:"orderNumber"`
	WhatsApp    string `json:"whatsapp"`
}
