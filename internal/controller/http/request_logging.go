package controllers

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type responseRecorder struct {
	http.ResponseWriter
	status       int
	responseSize int
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.responseSize += n
	return n, err
}

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// WithRequestLogging logs one structured access record for every handled request.
// It deliberately excludes query strings, headers, and request bodies.
func WithRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}

		defer func() {
			route := requestRoute(r.Pattern)
			if recovered := recover(); recovered != nil {
				status := recorder.status
				if status == 0 {
					status = http.StatusInternalServerError
				}
				logRequest(r, route, status, recorder.responseSize, time.Since(started), true)
				panic(recovered)
			}

			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logRequest(r, route, status, recorder.responseSize, time.Since(started), false)
		}()

		next.ServeHTTP(recorder, r)
	})
}

func requestRoute(pattern string) string {
	route := strings.TrimSpace(pattern)
	if _, path, found := strings.Cut(route, " "); found {
		route = path
	}
	if route == "" {
		return "unmatched"
	}
	return route
}

func serviceForRoute(route string) string {
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "admin" {
		return "admin." + parts[2]
	}
	if len(parts) >= 2 && parts[0] == "api" {
		return parts[1]
	}
	return "http"
}

func logRequest(r *http.Request, route string, status, responseSize int, duration time.Duration, panicked bool) {
	attrs := []slog.Attr{
		slog.String("service", serviceForRoute(route)),
		slog.String("route", route),
		slog.String("method", r.Method),
		slog.Int("status", status),
		slog.Int("response_bytes", responseSize),
		slog.Duration("duration", duration),
	}
	if panicked {
		attrs = append(attrs, slog.Bool("panic", true))
	}

	switch {
	case panicked || status >= http.StatusInternalServerError:
		slog.LogAttrs(r.Context(), slog.LevelError, "http request", attrs...)
	case status >= http.StatusBadRequest:
		slog.LogAttrs(r.Context(), slog.LevelWarn, "http request", attrs...)
	default:
		slog.LogAttrs(r.Context(), slog.LevelInfo, "http request", attrs...)
	}
}
