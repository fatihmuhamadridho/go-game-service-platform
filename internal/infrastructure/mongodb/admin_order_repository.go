package mongodb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type AdminOrderRepository struct{ db *mongo.Database }

func NewAdminOrderRepository(db *mongo.Database) *AdminOrderRepository {
	return &AdminOrderRepository{db: db}
}
func (r *AdminOrderRepository) ListOrderGraphs(c context.Context) ([]model.Order, []model.OrderItem, []model.OrderPayment, error) {
	o, e := findAll[model.Order](c, r.db.Collection("customer_order"), bson.D{})
	if e != nil {
		return nil, nil, nil, e
	}
	i, e := findAll[model.OrderItem](c, r.db.Collection("order_item"), bson.D{})
	if e != nil {
		return nil, nil, nil, e
	}
	p, e := findAll[model.OrderPayment](c, r.db.Collection("order_payment"), bson.D{})
	return o, i, p, e
}
func (r *AdminOrderRepository) GetOrderGraph(c context.Context, id bson.ObjectID) (*model.Order, []model.OrderItem, []model.OrderPayment, []model.OrderStatusHistory, []model.PaymentStatusHistory, []model.PaymentProof, error) {
	var o model.Order
	e := r.db.Collection("customer_order").FindOne(c, bson.D{{Key: "_id", Value: id}}).Decode(&o)
	if e == mongo.ErrNoDocuments {
		return nil, nil, nil, nil, nil, nil, nil
	}
	if e != nil {
		return nil, nil, nil, nil, nil, nil, e
	}
	items, e := findAll[model.OrderItem](c, r.db.Collection("order_item"), bson.D{{Key: "order_id", Value: id}})
	if e != nil {
		return nil, nil, nil, nil, nil, nil, e
	}
	payments, e := findAll[model.OrderPayment](c, r.db.Collection("order_payment"), bson.D{{Key: "order_id", Value: id}})
	if e != nil {
		return nil, nil, nil, nil, nil, nil, e
	}
	oh, e := findAll[model.OrderStatusHistory](c, r.db.Collection("order_status_history"), bson.D{{Key: "order_id", Value: id}})
	if e != nil {
		return nil, nil, nil, nil, nil, nil, e
	}
	var ph []model.PaymentStatusHistory
	var proofs []model.PaymentProof
	for _, p := range payments {
		a, x := findAll[model.PaymentStatusHistory](c, r.db.Collection("payment_status_history"), bson.D{{Key: "payment_id", Value: p.ID}})
		if x != nil {
			return nil, nil, nil, nil, nil, nil, x
		}
		ph = append(ph, a...)
		b, x := findAll[model.PaymentProof](c, r.db.Collection("payment_proof"), bson.D{{Key: "payment_id", Value: p.ID}})
		if x != nil {
			return nil, nil, nil, nil, nil, nil, x
		}
		proofs = append(proofs, b...)
	}
	return &o, items, payments, oh, ph, proofs, nil
}
func (r *AdminOrderRepository) ReviewPayment(c context.Context, orderID, paymentID bson.ObjectID, decision string, note *string, adminID bson.ObjectID) (string, string, error) {
	var order model.Order
	if e := r.db.Collection("customer_order").FindOne(c, bson.D{{Key: "_id", Value: orderID}}).Decode(&order); e != nil {
		if e == mongo.ErrNoDocuments {
			return "", "", model.ErrAdminOrderNotFound
		}
		return "", "", e
	}
	var payment model.OrderPayment
	if e := r.db.Collection("order_payment").FindOne(c, bson.D{{Key: "_id", Value: paymentID}, {Key: "order_id", Value: orderID}}).Decode(&payment); e != nil {
		if e == mongo.ErrNoDocuments {
			return "", "", model.ErrAdminOrderPaymentNotFound
		}
		return "", "", e
	}
	if payment.Status != "PENDING_REVIEW" {
		return "", "", model.ErrAdminPaymentNotReviewable
	}
	now := time.Now().UTC()
	retry := order.ExpiresAt != nil && order.ExpiresAt.After(now)
	nextPay, nextOrder := "FAILED", "CANCELLED"
	if decision == "APPROVE" {
		nextPay, nextOrder = "PAID", "PROCESSING"
	} else if retry {
		nextPay, nextOrder = "UNPAID", "PENDING_PAYMENT"
	}
	res, e := r.db.Collection("order_payment").UpdateOne(c, bson.D{{Key: "_id", Value: paymentID}, {Key: "order_id", Value: orderID}, {Key: "status", Value: "PENDING_REVIEW"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: nextPay}, {Key: "reviewed_by", Value: adminID}, {Key: "reviewed_at", Value: now}, {Key: "review_note", Value: note}, {Key: "submitted_at", Value: func() any {
		if decision == "REJECT" && retry {
			return nil
		}
		return payment.SubmittedAt
	}()}, {Key: "updated_at", Value: now}}}})
	if e != nil {
		return "", "", e
	}
	if res.ModifiedCount != 1 {
		return "", "", model.ErrAdminPaymentReviewConflict
	}
	paymentHistoryID, orderHistoryID := bson.NewObjectID(), bson.NewObjectID()
	transactionID, transactionHistoryID := bson.NilObjectID, bson.NilObjectID
	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !transactionHistoryID.IsZero() {
			_, _ = r.db.Collection("transaction_status_history").DeleteOne(cleanupCtx, bson.D{{Key: "_id", Value: transactionHistoryID}})
		}
		if !transactionID.IsZero() {
			_, _ = r.db.Collection("financial_transaction").DeleteOne(cleanupCtx, bson.D{{Key: "_id", Value: transactionID}})
		}
		_, _ = r.db.Collection("payment_status_history").DeleteOne(cleanupCtx, bson.D{{Key: "_id", Value: paymentHistoryID}})
		_, _ = r.db.Collection("order_status_history").DeleteOne(cleanupCtx, bson.D{{Key: "_id", Value: orderHistoryID}})
		_, _ = r.db.Collection("customer_order").UpdateOne(cleanupCtx, bson.D{{Key: "_id", Value: orderID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: order.PaymentStatus}, {Key: "order_status", Value: order.OrderStatus}, {Key: "updated_at", Value: time.Now().UTC()}}}})
		_, _ = r.db.Collection("order_payment").UpdateOne(cleanupCtx, bson.D{{Key: "_id", Value: paymentID}, {Key: "status", Value: nextPay}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "PENDING_REVIEW"}, {Key: "reviewed_by", Value: nil}, {Key: "reviewed_at", Value: nil}, {Key: "review_note", Value: nil}, {Key: "submitted_at", Value: payment.SubmittedAt}, {Key: "updated_at", Value: time.Now().UTC()}}}})
	}
	if _, e = r.db.Collection("customer_order").UpdateOne(c, bson.D{{Key: "_id", Value: orderID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: nextPay}, {Key: "order_status", Value: nextOrder}, {Key: "updated_at", Value: now}}}}); e != nil {
		rollback()
		return "", "", e
	}
	paymentWhy := "Konfirmasi pembayaran ditolak setelah order kedaluwarsa."
	if decision == "APPROVE" {
		paymentWhy = "Konfirmasi pembayaran disetujui."
	} else if retry {
		paymentWhy = "Konfirmasi pembayaran ditolak; order masih berlaku untuk konfirmasi ulang."
	}
	if note != nil {
		paymentWhy = *note
	}
	orderWhy := "Pembayaran ditolak admin setelah order kedaluwarsa."
	if decision == "APPROVE" {
		orderWhy = "Pembayaran disetujui admin."
	} else if retry {
		orderWhy = "Konfirmasi pembayaran ditolak admin; pelanggan dapat konfirmasi ulang."
	}
	if note != nil {
		orderWhy = *note
	}
	oldPay, oldOrder := payment.Status, order.OrderStatus
	if _, e = r.db.Collection("payment_status_history").InsertOne(c, model.PaymentStatusHistory{ID: paymentHistoryID, PaymentID: paymentID, OldStatus: &oldPay, NewStatus: nextPay, ChangedBy: &adminID, Reason: &paymentWhy, ChangedAt: now}); e != nil {
		rollback()
		return "", "", e
	}
	if _, e = r.db.Collection("order_status_history").InsertOne(c, model.OrderStatusHistory{ID: orderHistoryID, OrderID: orderID, OldStatus: &oldOrder, NewStatus: nextOrder, ChangedBy: &adminID, Reason: &orderWhy, ChangedAt: now}); e != nil {
		rollback()
		return "", "", e
	}
	if decision == "APPROVE" {
		var suffix [4]byte
		if _, e = rand.Read(suffix[:]); e != nil {
			rollback()
			return "", "", e
		}
		transactionID, transactionHistoryID = bson.NewObjectID(), bson.NewObjectID()
		paymentRef := payment.ID
		tx := model.FinancialTransaction{ID: transactionID, TransactionNumber: "JHT-" + now.Format("20060102") + "-" + strings.ToUpper(hex.EncodeToString(suffix[:])), OrderID: orderID, OrderPaymentID: &paymentRef, Type: "PAYMENT", Method: payment.Method, Amount: payment.Amount, Currency: order.Currency, Status: "SUCCESS", CustomerNameSnapshot: &order.CustomerName, CustomerContactSnapshot: &order.CustomerWhatsApp, CreatedAt: now, UpdatedAt: now, SettledAt: &now}
		if _, e = r.db.Collection("financial_transaction").InsertOne(c, tx); e != nil {
			rollback()
			return "", "", e
		}
		reason := "Pembayaran manual disetujui admin."
		if _, e = r.db.Collection("transaction_status_history").InsertOne(c, model.TransactionStatusHistory{ID: transactionHistoryID, TransactionID: transactionID, NewStatus: "SUCCESS", ChangedBy: &adminID, Reason: &reason, ChangedAt: now}); e != nil {
			rollback()
			return "", "", e
		}
	}
	return nextPay, nextOrder, nil
}
func (r *AdminOrderRepository) CompleteOrder(c context.Context, id, actor bson.ObjectID, now time.Time) error {
	var o model.Order
	if e := r.db.Collection("customer_order").FindOne(c, bson.D{{Key: "_id", Value: id}}).Decode(&o); e != nil {
		if e == mongo.ErrNoDocuments {
			return model.ErrAdminOrderNotFound
		}
		return e
	}
	if o.OrderStatus != "PROCESSING" || o.PaymentStatus != "PAID" {
		return model.ErrAdminOrderNotCompletable
	}
	res, e := r.db.Collection("customer_order").UpdateOne(c, bson.D{{Key: "_id", Value: id}, {Key: "order_status", Value: "PROCESSING"}, {Key: "payment_status", Value: "PAID"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "order_status", Value: "COMPLETED"}, {Key: "updated_at", Value: now}}}})
	if e != nil {
		return e
	}
	if res.ModifiedCount != 1 {
		return model.ErrAdminOrderCompletionConflict
	}
	old := "PROCESSING"
	reason := "Layanan pesanan diselesaikan admin."
	historyID := bson.NewObjectID()
	_, e = r.db.Collection("order_status_history").InsertOne(c, model.OrderStatusHistory{ID: historyID, OrderID: id, OldStatus: &old, NewStatus: "COMPLETED", ChangedBy: &actor, Reason: &reason, ChangedAt: now})
	if e != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = r.db.Collection("order_status_history").DeleteOne(cleanupCtx, bson.D{{Key: "_id", Value: historyID}})
		_, _ = r.db.Collection("customer_order").UpdateOne(cleanupCtx, bson.D{{Key: "_id", Value: id}, {Key: "order_status", Value: "COMPLETED"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "order_status", Value: "PROCESSING"}, {Key: "updated_at", Value: time.Now().UTC()}}}})
	}
	return e
}

var _ model.AdminOrderRepository = (*AdminOrderRepository)(nil)
