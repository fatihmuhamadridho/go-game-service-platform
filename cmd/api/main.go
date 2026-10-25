package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/adminaccount"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/auth"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/config"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/game"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/platform/database"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/publicgame"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/role"
	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Error("environment load failed")
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration invalid", "error", err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := database.Connect(ctx, cfg.MongoURL)
	if err != nil {
		logger.Error("MongoDB client creation failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		disconnectCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Disconnect(disconnectCtx); err != nil {
			logger.Error("MongoDB disconnect failed", "error", err.Error())
		}
	}()
	database := client.Database(cfg.MongoDatabase)
	adminModel := auth.NewMongoAdminAuthRepository(database)
	gameModel := game.NewMongoGameRepository(database)
	publicGameModel := publicgame.NewMongoRepository(database)
	adminAccountModel := adminaccount.NewMongoRepository(database)
	roleModel := role.NewMongoRepository(database)
	indexCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	if err := adminModel.EnsureIndexes(indexCtx); err != nil {
		stop()
		logger.Error("MongoDB index setup failed", "error", err.Error())
		os.Exit(1)
	}
	if err := gameModel.EnsureIndexes(indexCtx); err != nil {
		stop()
		logger.Error("MongoDB game index setup failed", "error", err.Error())
		os.Exit(1)
	}
	if err := publicGameModel.EnsureIndexes(indexCtx); err != nil {
		stop()
		logger.Error("MongoDB public game index setup failed", "error", err.Error())
		os.Exit(1)
	}
	stop()

	tokenManager := auth.TokenManager{Secret: cfg.JWTSecret, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience}
	service := &auth.AuthService{Data: adminModel, Limiter: adminModel, Tokens: tokenManager, Passwords: auth.Argon2id{}}
	handler := auth.NewAdminAuthHandler(service, adminModel, tokenManager, cfg.Origins, cfg.TrustedProxyCIDRs, logger, cfg.AllowInsecureHTTP)
	mux := http.NewServeMux()
	handler.Register(mux)
	gameHandler := game.NewAdminGameHandler(&game.GameService{Data: gameModel}, handler, cfg.TrustedProxyCIDRs)
	gameHandler.Register(mux)
	roleHandler := role.NewAdminRoleHandler(&role.Service{Data: roleModel}, adminModel, handler, cfg.TrustedProxyCIDRs)
	roleHandler.Register(mux)
	adminAccountHandler := adminaccount.NewAdminAccountHandler(&adminaccount.Service{Data: adminAccountModel}, adminModel, handler, cfg.TrustedProxyCIDRs)
	adminAccountHandler.Register(mux)
	publicGameHandler := publicgame.NewHandler(&publicgame.Service{Data: publicGameModel}, cfg.TrustedProxyCIDRs)
	publicGameHandler.Register(mux)
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler.CORS(auth.RequestLogging(logger, mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP shutdown failed", "error", err.Error())
		}
	}()

	logger.Info("admin api service starting", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err.Error())
		os.Exit(1)
	}
}
