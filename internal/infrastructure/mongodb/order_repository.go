package mongodb

import (
	"context"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type OrderRepository struct{ database *mongo.Database }

func NewOrderRepository(database *mongo.Database) *OrderRepository {
	return &OrderRepository{database: database}
}

func (r *OrderRepository) GetCheckoutSettings(ctx context.Context) (*model.PlatformSettings, error) {
	var settings model.PlatformSettings
	err := r.database.Collection("platform_settings").FindOne(ctx, bson.D{{Key: "singleton_key", Value: "current"}}).Decode(&settings)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &settings, err
}

func (r *OrderRepository) FindActiveGameBySlugForOrder(ctx context.Context, slug string) (*model.GameCatalog, error) {
	var game model.GameCatalog
	err := r.database.Collection("game_catalog").FindOne(ctx, bson.D{{Key: "slug", Value: slug}, {Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}).Decode(&game)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &game, err
}

func (r *OrderRepository) FindCheckoutService(ctx context.Context, gameID, serviceID bson.ObjectID) (*model.GameService, *model.GameCatalog, *model.ServiceCategory, error) {
	var service model.GameService
	if err := r.database.Collection("game_service").FindOne(ctx, bson.D{{Key: "_id", Value: serviceID}, {Key: "game_id", Value: gameID}, {Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}).Decode(&service); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	var game model.GameCatalog
	if err := r.database.Collection("game_catalog").FindOne(ctx, bson.D{{Key: "_id", Value: gameID}, {Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}).Decode(&game); err != nil {
		return nil, nil, nil, err
	}
	var category model.ServiceCategory
	if err := r.database.Collection("service_category").FindOne(ctx, bson.D{{Key: "_id", Value: service.CategoryID}, {Key: "is_active", Value: true}, {Key: "deleted_at", Value: nil}}).Decode(&category); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, &game, nil, nil
		}
		return nil, nil, nil, err
	}
	return &service, &game, &category, nil
}

func (r *OrderRepository) FindIdempotency(ctx context.Context, keyHash string) (*model.CheckoutIdempotency, error) {
	var row model.CheckoutIdempotency
	err := r.database.Collection("checkout_idempotency").FindOne(ctx, bson.D{{Key: "key_hash", Value: keyHash}}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}

func (r *OrderRepository) CreateIdempotency(ctx context.Context, row model.CheckoutIdempotency) error {
	_, err := r.database.Collection("checkout_idempotency").InsertOne(ctx, row)
	return err
}
func (r *OrderRepository) CompleteIdempotency(ctx context.Context, id bson.ObjectID, response any) error {
	_, err := r.database.Collection("checkout_idempotency").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, bson.D{{Key: "$set", Value: bson.D{{Key: "response_json", Value: response}}}})
	return err
}
func (r *OrderRepository) DeleteIdempotency(ctx context.Context, id bson.ObjectID) error {
	_, err := r.database.Collection("checkout_idempotency").DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	return err
}

func (r *OrderRepository) CreateTrackingToken(ctx context.Context, row model.OrderTrackingToken) error {
	c := r.database.Collection("order_tracking_token")
	previous, err := findAll[model.OrderTrackingToken](ctx, c, bson.D{{Key: "order_id", Value: row.OrderID}})
	if err != nil {
		return err
	}
	if _, err = c.InsertOne(ctx, row); err != nil {
		return err
	}
	for _, old := range previous {
		if old.RevokedAt != nil || !old.ExpiresAt.After(row.CreatedAt) {
			if _, err = c.DeleteOne(ctx, bson.D{{Key: "_id", Value: old.ID}}); err != nil {
				return err
			}
			continue
		}
		if _, err = c.UpdateOne(ctx, bson.D{{Key: "_id", Value: old.ID}, {Key: "revoked_at", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: row.CreatedAt}}}}); err != nil {
			return err
		}
	}
	return nil
}

func (r *OrderRepository) FindTrackingToken(ctx context.Context, hash string) (*model.OrderTrackingToken, error) {
	var row model.OrderTrackingToken
	err := r.database.Collection("order_tracking_token").FindOne(ctx, bson.D{{Key: "token_hash", Value: hash}}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}

func (r *OrderRepository) CreateCheckout(ctx context.Context, order model.Order, item model.OrderItem, payment model.OrderPayment, orderHistory model.OrderStatusHistory, paymentHistory model.PaymentStatusHistory) error {
	collections := []struct {
		name  string
		value any
		id    bson.ObjectID
	}{{"customer_order", order, order.ID}, {"order_item", item, item.ID}, {"order_payment", payment, payment.ID}, {"order_status_history", orderHistory, orderHistory.ID}, {"payment_status_history", paymentHistory, paymentHistory.ID}}
	inserted := make([]struct {
		name string
		id   bson.ObjectID
	}, 0, len(collections))
	for _, row := range collections {
		if _, err := r.database.Collection(row.name).InsertOne(ctx, row.value); err != nil {
			for i := len(inserted) - 1; i >= 0; i-- {
				_, _ = r.database.Collection(inserted[i].name).DeleteOne(context.Background(), bson.D{{Key: "_id", Value: inserted[i].id}})
			}
			return err
		}
		inserted = append(inserted, struct {
			name string
			id   bson.ObjectID
		}{row.name, row.id})
	}
	return nil
}

func (r *OrderRepository) RollbackCheckout(ctx context.Context, orderID, itemID, paymentID, orderHistoryID, paymentHistoryID bson.ObjectID) error {
	deletions := []struct {
		collection string
		id         bson.ObjectID
	}{
		{"order_status_history", orderHistoryID},
		{"payment_status_history", paymentHistoryID},
		{"order_payment", paymentID},
		{"order_item", itemID},
		{"customer_order", orderID},
	}
	var firstErr error
	for _, deletion := range deletions {
		if _, err := r.database.Collection(deletion.collection).DeleteOne(ctx, bson.D{{Key: "_id", Value: deletion.id}}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *OrderRepository) FindPaymentOrder(ctx context.Context, id bson.ObjectID) (*model.OrderPayment, *model.Order, error) {
	var payment model.OrderPayment
	if err := r.database.Collection("order_payment").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&payment); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var order model.Order
	if err := r.database.Collection("customer_order").FindOne(ctx, bson.D{{Key: "_id", Value: payment.OrderID}}).Decode(&order); err != nil {
		if err == mongo.ErrNoDocuments {
			return &payment, nil, nil
		}
		return nil, nil, err
	}
	return &payment, &order, nil
}
func (r *OrderRepository) SetPaymentPendingReview(ctx context.Context, id bson.ObjectID, now time.Time) (bool, error) {
	res, err := r.database.Collection("order_payment").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}, {Key: "status", Value: "UNPAID"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "PENDING_REVIEW"}, {Key: "submitted_at", Value: now}, {Key: "updated_at", Value: now}}}})
	return res != nil && res.ModifiedCount == 1, err
}
func (r *OrderRepository) UpdateOrderPaymentStatus(ctx context.Context, id bson.ObjectID, paymentStatus, orderStatus string, now time.Time) error {
	_, err := r.database.Collection("customer_order").UpdateOne(ctx, bson.D{{Key: "_id", Value: id}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: paymentStatus}, {Key: "order_status", Value: orderStatus}, {Key: "updated_at", Value: now}}}})
	return err
}
func (r *OrderRepository) CreateOrderHistory(ctx context.Context, row model.OrderStatusHistory) error {
	_, err := r.database.Collection("order_status_history").InsertOne(ctx, row)
	return err
}
func (r *OrderRepository) CreatePaymentHistory(ctx context.Context, row model.PaymentStatusHistory) error {
	_, err := r.database.Collection("payment_status_history").InsertOne(ctx, row)
	return err
}

func (r *OrderRepository) RollbackPaymentReport(ctx context.Context, paymentID, orderID, paymentHistoryID, orderHistoryID bson.ObjectID, paymentStatus, orderStatus string, now time.Time) error {
	var firstErr error
	if _, err := r.database.Collection("order_status_history").DeleteOne(ctx, bson.D{{Key: "_id", Value: orderHistoryID}}); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := r.database.Collection("payment_status_history").DeleteOne(ctx, bson.D{{Key: "_id", Value: paymentHistoryID}}); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := r.database.Collection("customer_order").UpdateOne(ctx, bson.D{{Key: "_id", Value: orderID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: paymentStatus}, {Key: "order_status", Value: orderStatus}, {Key: "updated_at", Value: now}}}}); err != nil && firstErr == nil {
		firstErr = err
	}
	if _, err := r.database.Collection("order_payment").UpdateOne(ctx, bson.D{{Key: "_id", Value: paymentID}, {Key: "status", Value: "PENDING_REVIEW"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "UNPAID"}, {Key: "submitted_at", Value: nil}, {Key: "updated_at", Value: now}}}}); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (r *OrderRepository) FindOrderByNumber(ctx context.Context, number string) (*model.Order, error) {
	var row model.Order
	err := r.database.Collection("customer_order").FindOne(ctx, bson.D{{Key: "order_number", Value: number}}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &row, err
}
func (r *OrderRepository) GetOrderGraph(ctx context.Context, id bson.ObjectID) (*model.Order, []model.OrderItem, []model.OrderPayment, []model.OrderStatusHistory, []model.PaymentStatusHistory, error) {
	var order model.Order
	if err := r.database.Collection("customer_order").FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&order); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil, nil, nil, nil, nil
		}
		return nil, nil, nil, nil, nil, err
	}
	items, err := findAll[model.OrderItem](ctx, r.database.Collection("order_item"), bson.D{{Key: "order_id", Value: id}})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	payments, err := findAll[model.OrderPayment](ctx, r.database.Collection("order_payment"), bson.D{{Key: "order_id", Value: id}})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	orders, err := findAll[model.OrderStatusHistory](ctx, r.database.Collection("order_status_history"), bson.D{{Key: "order_id", Value: id}})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	paymentHistories := make([]model.PaymentStatusHistory, 0)
	for _, payment := range payments {
		rows, e := findAll[model.PaymentStatusHistory](ctx, r.database.Collection("payment_status_history"), bson.D{{Key: "payment_id", Value: payment.ID}})
		if e != nil {
			return nil, nil, nil, nil, nil, e
		}
		paymentHistories = append(paymentHistories, rows...)
	}
	return &order, items, payments, orders, paymentHistories, nil
}

var _ model.OrderRepository = (*OrderRepository)(nil)
