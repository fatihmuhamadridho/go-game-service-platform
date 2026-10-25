package handler

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/application"
	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

var (
	initialize sync.Once
	app        *application.Application
	initErr    error
)

// Handler is the Vercel Go Function entry point. Vercel routes all paths here;
// the shared application router dispatches each request to its API endpoint.
func Handler(writer http.ResponseWriter, request *http.Request) {
	initialize.Do(func() {
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
		slog.SetDefault(logger)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		app, initErr = application.New(ctx, logger)
	})
	if initErr != nil {
		slog.Error("API function initialization failed", "error", initErr.Error())
		utils.WriteJSON(writer, http.StatusServiceUnavailable, utils.ResponseEnvelope{
			Code: "ADMT03001", Message: "Layanan API sedang tidak tersedia.", Data: nil,
		})
		return
	}

	// The Vercel rewrite targets /api/index while carrying the public path here.
	query := request.URL.Query()
	if paths := query["__vercel_path"]; len(paths) > 0 && paths[0] != "" {
		request = request.Clone(request.Context())
		request.URL.Path = paths[0]
		request.URL.RawPath = ""
		query.Del("__vercel_path")
		request.URL.RawQuery = query.Encode()
	}
	app.Handler.ServeHTTP(writer, request)
}
