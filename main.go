package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"example.com/learn-golang/config"
	"example.com/learn-golang/internal/controller"
	controllers "example.com/learn-golang/internal/controller/http"
	"example.com/learn-golang/internal/infrastructure/blob"
	"example.com/learn-golang/internal/infrastructure/email"
	"example.com/learn-golang/internal/infrastructure/mongodb"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Error("failed to load .env", "error", err)
		os.Exit(1)
	}

	client, database, err := config.ConnectMongo()
	if err != nil {
		slog.Error("failed to connect to MongoDB", "error", err)
		os.Exit(1)
	}
	slog.Info("connected to MongoDB", "database", database.Name())

	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			slog.Error("failed to disconnect from MongoDB", "error", err)
		}
	}()

	mux := http.NewServeMux()
	controllers.RegisterSystemRoutes(mux)
	catalog := controller.NewCatalogPresenter(mongodb.NewCatalogRepository(database))
	auth := controller.NewAuthPresenter(mongodb.NewAuthRepository(database))
	controllers.ConfigureAuthRateLimit(mongodb.NewRateLimitRepository(database))
	games := controller.NewAdminGamePresenter(mongodb.NewCatalogRepository(database))
	adminCatalog := controller.NewAdminCatalogController(mongodb.NewCatalogRepository(database), mongodb.NewCatalogRepository(database))
	adminMembers := controller.NewAdminMemberController(mongodb.NewAdminMemberRepository(database))
	adminAccessRepository := mongodb.NewAdminAccessRepository(database)
	adminAccess := controller.NewAdminAccessController(adminAccessRepository, adminAccessRepository)
	adminRead := controller.NewAdminReadController(mongodb.NewAdminReadRepository(database))
	adminOrders := controller.NewAdminOrderController(mongodb.NewAdminOrderRepository(database))
	orders := controller.NewOrderController(mongodb.NewOrderRepository(database))
	orders.ConfigureProofs(mongodb.NewPaymentProofRepository(database), blob.NewStorage())
	controllers.RegisterCatalogRoutes(mux, catalog)
	controllers.RegisterAuthRoutes(mux, auth, email.NewSMTPSender())
	controllers.RegisterAdminGameRoutes(mux, auth, games)
	controllers.RegisterAdminCatalogRoutes(mux, auth, adminCatalog)
	controllers.RegisterAdminMemberRoutes(mux, auth, adminMembers)
	controllers.RegisterAdminAccessRoutes(mux, auth, adminAccess)
	controllers.RegisterAdminReadRoutes(mux, auth, adminRead)
	controllers.RegisterAdminOrderRoutes(mux, auth, adminOrders)
	controllers.RegisterOrderRoutes(mux, orders)
	controllers.RegisterPaymentProofRoutes(mux, auth, orders)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	slog.Info("starting HTTP server", "address", addr)
	handler := controllers.WithCORS(controllers.WithRequestLogging(mux))
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("HTTP server stopped", "error", err)
		os.Exit(1)
	}
}
