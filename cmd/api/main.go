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

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/application"
	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Error("environment load failed")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app, err := application.New(ctx, logger)
	if err != nil {
		logger.Error("application initialization failed", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		disconnectCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := app.Close(disconnectCtx); err != nil {
			logger.Error("MongoDB disconnect failed")
		}
	}()

	server := &http.Server{
		Addr:              ":" + app.Port,
		Handler:           app.Handler,
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
			logger.Error("HTTP shutdown failed")
		}
	}()

	logger.Info("admin api service starting", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("HTTP server stopped", "error", err.Error())
		os.Exit(1)
	}
}
