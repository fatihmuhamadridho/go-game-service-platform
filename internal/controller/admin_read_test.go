package controller

import (
	"context"
	"testing"
	"time"

	"example.com/learn-golang/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type dashboardTestRepository struct {
	model.AdminReadRepository
	orders       []model.Order
	transactions []model.FinancialTransaction
	items        []model.OrderItem
}

func (r dashboardTestRepository) ListAllOrders(context.Context) ([]model.Order, error) {
	return r.orders, nil
}

func (r dashboardTestRepository) ListTransactions(context.Context) ([]model.FinancialTransaction, error) {
	return r.transactions, nil
}

func (dashboardTestRepository) ListAllUsers(context.Context) ([]model.Account, error) {
	return nil, nil
}

func (dashboardTestRepository) ListAllGameServices(context.Context) ([]model.GameService, error) {
	return nil, nil
}

func (r dashboardTestRepository) ListAllOrderItems(context.Context) ([]model.OrderItem, error) {
	return r.items, nil
}

func (dashboardTestRepository) ListActivity(context.Context) ([]model.ActivityLog, error) {
	return nil, nil
}

func TestDashboardPreservesSourceEncounterOrderForTies(t *testing.T) {
	amount, err := bson.ParseDecimal128("10")
	if err != nil {
		t.Fatal(err)
	}
	firstOrderID, secondOrderID := bson.NewObjectID(), bson.NewObjectID()
	firstOrderTime := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	secondOrderTime := firstOrderTime.Add(time.Hour)
	repository := dashboardTestRepository{
		orders: []model.Order{
			{ID: firstOrderID, OrderStatus: "PENDING_PAYMENT", CreatedAt: firstOrderTime},
			{ID: secondOrderID, OrderStatus: "COMPLETED", CreatedAt: secondOrderTime},
		},
		transactions: []model.FinancialTransaction{
			{OrderID: firstOrderID, Type: "PAYMENT", Status: "SUCCESS", Method: "BCA_TRANSFER", Amount: amount, CreatedAt: firstOrderTime},
			{OrderID: secondOrderID, Type: "PAYMENT", Status: "SUCCESS", Method: "QRIS", Amount: amount, CreatedAt: secondOrderTime},
		},
		items: []model.OrderItem{
			{OrderID: firstOrderID, GameNameSnapshot: "Game B"},
			{OrderID: secondOrderID, GameNameSnapshot: "Game A"},
		},
	}

	controller := NewAdminReadController(repository)
	start := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(24*time.Hour - time.Millisecond)
	data, err := controller.Dashboard(context.Background(), start, end, "daily")
	if err != nil {
		t.Fatal(err)
	}

	statuses := data["statusDistribution"].([]map[string]any)
	if got := []string{statuses[0]["status"].(string), statuses[1]["status"].(string)}; got[0] != "Menunggu" || got[1] != "Selesai" {
		t.Fatalf("status distribution order = %v, want [Menunggu Selesai]", got)
	}
	games := data["popularGames"].([]map[string]any)
	if got := []string{games[0]["name"].(string), games[1]["name"].(string)}; got[0] != "Game B" || got[1] != "Game A" {
		t.Fatalf("popular game tie order = %v, want [Game B Game A]", got)
	}
	methods := data["paymentMethods"].([]map[string]any)
	if got := []string{methods[0]["method"].(string), methods[1]["method"].(string)}; got[0] != "Transfer Bank" || got[1] != "QRIS" {
		t.Fatalf("payment method order = %v, want [Transfer Bank QRIS]", got)
	}
}
