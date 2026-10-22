package mongodb

import (
	"context"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type PaymentProofRepository struct{ db *mongo.Database }

func NewPaymentProofRepository(db *mongo.Database) *PaymentProofRepository {
	return &PaymentProofRepository{db: db}
}
func (r *PaymentProofRepository) FindPaymentProof(c context.Context, id bson.ObjectID) (*model.PaymentProof, error) {
	var x model.PaymentProof
	e := r.db.Collection("payment_proof").FindOne(c, bson.D{{Key: "_id", Value: id}}).Decode(&x)
	if e == mongo.ErrNoDocuments {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return &x, nil
}
func (r *PaymentProofRepository) ListPaymentProofs(c context.Context, id bson.ObjectID) ([]model.PaymentProof, error) {
	return findAll[model.PaymentProof](c, r.db.Collection("payment_proof"), bson.D{{Key: "payment_id", Value: id}})
}
func (r *PaymentProofRepository) FinalizeProof(c context.Context, paymentID bson.ObjectID, orderNumber, tempKey, objectKey string, size int64, now time.Time) (bson.ObjectID, error) {
	var payment model.OrderPayment
	if e := r.db.Collection("order_payment").FindOne(c, bson.D{{Key: "_id", Value: paymentID}}).Decode(&payment); e != nil {
		if e == mongo.ErrNoDocuments {
			return bson.NilObjectID, model.ErrPaymentProofConflict
		}
		return bson.NilObjectID, e
	}
	var order model.Order
	if e := r.db.Collection("customer_order").FindOne(c, bson.D{{Key: "_id", Value: payment.OrderID}}).Decode(&order); e != nil {
		if e == mongo.ErrNoDocuments {
			return bson.NilObjectID, model.ErrPaymentProofConflict
		}
		return bson.NilObjectID, e
	}
	if order.OrderNumber != orderNumber || order.ExpiresAt == nil || !order.ExpiresAt.After(now) {
		return bson.NilObjectID, model.ErrPaymentProofConflict
	}
	if payment.Status != "UNPAID" {
		return bson.NilObjectID, model.ErrPaymentProofConflict
	}
	res, e := r.db.Collection("order_payment").UpdateOne(c, bson.D{{Key: "_id", Value: paymentID}, {Key: "status", Value: "UNPAID"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "PENDING_REVIEW"}, {Key: "submitted_at", Value: now}, {Key: "updated_at", Value: now}}}})
	if e != nil {
		return bson.NilObjectID, e
	}
	if res.ModifiedCount != 1 {
		return bson.NilObjectID, model.ErrPaymentProofConflict
	}
	proof := model.PaymentProof{ID: bson.NewObjectID(), PaymentID: paymentID, ObjectKey: objectKey, MimeType: "image/png", FileSizeBytes: int32(size), UploadedAt: now}
	paymentHistoryID, orderHistoryID := bson.NewObjectID(), bson.NewObjectID()
	rollback := func(cause error) (bson.ObjectID, error) {
		_, _ = r.db.Collection("order_status_history").DeleteOne(c, bson.D{{Key: "_id", Value: orderHistoryID}})
		_, _ = r.db.Collection("payment_status_history").DeleteOne(c, bson.D{{Key: "_id", Value: paymentHistoryID}})
		_, _ = r.db.Collection("payment_proof").DeleteOne(c, bson.D{{Key: "_id", Value: proof.ID}})
		_, _ = r.db.Collection("customer_order").UpdateOne(c, bson.D{{Key: "_id", Value: order.ID}, {Key: "payment_status", Value: "PENDING_REVIEW"}, {Key: "order_status", Value: "PENDING_VALIDATION"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: order.PaymentStatus}, {Key: "order_status", Value: order.OrderStatus}, {Key: "updated_at", Value: time.Now().UTC()}}}})
		_, _ = r.db.Collection("order_payment").UpdateOne(c, bson.D{{Key: "_id", Value: paymentID}, {Key: "status", Value: "PENDING_REVIEW"}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "UNPAID"}, {Key: "submitted_at", Value: payment.SubmittedAt}, {Key: "reviewed_by", Value: payment.ReviewedBy}, {Key: "reviewed_at", Value: payment.ReviewedAt}, {Key: "review_note", Value: payment.ReviewNote}, {Key: "updated_at", Value: time.Now().UTC()}}}})
		return bson.NilObjectID, cause
	}
	if _, e = r.db.Collection("payment_proof").InsertOne(c, proof); e != nil {
		return rollback(e)
	}
	_, e = r.db.Collection("customer_order").UpdateOne(c, bson.D{{Key: "_id", Value: payment.OrderID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "payment_status", Value: "PENDING_REVIEW"}, {Key: "order_status", Value: "PENDING_VALIDATION"}, {Key: "updated_at", Value: now}}}})
	if e != nil {
		return rollback(e)
	}
	reason := "Customer mengirim bukti pembayaran."
	oldPay := "UNPAID"
	_, e = r.db.Collection("payment_status_history").InsertOne(c, model.PaymentStatusHistory{ID: paymentHistoryID, PaymentID: paymentID, OldStatus: &oldPay, NewStatus: "PENDING_REVIEW", Reason: &reason, ChangedAt: now})
	if e != nil {
		return rollback(e)
	}
	oldOrder := order.OrderStatus
	reason = "Pelanggan mengirim bukti pembayaran; menunggu validasi admin."
	_, e = r.db.Collection("order_status_history").InsertOne(c, model.OrderStatusHistory{ID: orderHistoryID, OrderID: order.ID, OldStatus: &oldOrder, NewStatus: "PENDING_VALIDATION", Reason: &reason, ChangedAt: now})
	if e != nil {
		return rollback(e)
	}
	return proof.ID, nil
}

var _ model.PaymentProofRepository = (*PaymentProofRepository)(nil)
