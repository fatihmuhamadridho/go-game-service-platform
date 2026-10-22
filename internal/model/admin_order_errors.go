package model

import "errors"

var (
	ErrAdminOrderNotFound           = errors.New("admin order not found")
	ErrAdminOrderPaymentNotFound    = errors.New("admin order payment not found")
	ErrAdminPaymentNotReviewable    = errors.New("admin order payment is not reviewable")
	ErrAdminPaymentReviewConflict   = errors.New("admin order payment review conflict")
	ErrAdminOrderNotCompletable     = errors.New("admin order is not completable")
	ErrAdminOrderCompletionConflict = errors.New("admin order completion conflict")
	ErrPaymentProofConflict         = errors.New("payment proof submission conflict")
)
