package controllers

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type corsResponseWriter struct {
	http.ResponseWriter
	origin    string
	committed bool
}

func (w *corsResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.applyCORSHeaders()
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *corsResponseWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *corsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *corsResponseWriter) applyCORSHeaders() {
	w.Header().Set("Access-Control-Allow-Origin", w.origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
}

func WithCORS(next http.Handler) http.Handler {
	allowedOrigins := parseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	isProduction := isProductionEnvironment()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Add("Vary", "Origin")
		if !isAllowedCORSOrigin(origin, allowedOrigins, isProduction) {
			if r.Method == http.MethodOptions {
				http.Error(w, "Origin not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		corsWriter := &corsResponseWriter{ResponseWriter: w, origin: origin}
		corsWriter.applyCORSHeaders()

		if r.Method == http.MethodOptions {
			corsWriter.Header().Add("Vary", "Access-Control-Request-Method")
			corsWriter.Header().Add("Vary", "Access-Control-Request-Headers")
			requestedMethod := strings.TrimSpace(r.Header.Get("Access-Control-Request-Method"))
			if !isHTTPToken(requestedMethod) {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			requestedHeaders, ok := validateCORSHeaders(r.Header.Get("Access-Control-Request-Headers"))
			if !ok {
				http.Error(w, "Header not allowed", http.StatusForbidden)
				return
			}

			corsWriter.Header().Set("Access-Control-Allow-Methods", requestedMethod+", OPTIONS")
			if requestedHeaders != "" {
				corsWriter.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
			}
			corsWriter.Header().Set("Access-Control-Max-Age", "600")
			corsWriter.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(corsWriter, r)
	})
}

func isProductionEnvironment() bool {
	return strings.EqualFold(os.Getenv("VERCEL_ENV"), "production") || strings.EqualFold(os.Getenv("NODE_ENV"), "production")
}

func parseCORSOrigins(value string) map[string]struct{} {
	origins := make(map[string]struct{})
	for _, origin := range strings.Split(value, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins[origin] = struct{}{}
		}
	}
	return origins
}

func isAllowedCORSOrigin(origin string, configured map[string]struct{}, isProduction bool) bool {
	if len(configured) > 0 {
		_, allowed := configured[origin]
		return allowed
	}
	return !isProduction && isLocalhostOrigin(origin)
}

func isTrustedRequestOrigin(origin, sameOrigin string, configured map[string]struct{}) bool {
	if origin == "" || origin == sameOrigin {
		return true
	}
	return isAllowedCORSOrigin(origin, configured, isProductionEnvironment())
}

func isLocalhostOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateCORSHeaders(value string) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", true
	}

	headers := strings.Split(value, ",")
	for i, header := range headers {
		header = strings.TrimSpace(header)
		if !isHTTPToken(header) {
			return "", false
		}
		headers[i] = http.CanonicalHeaderKey(header)
	}
	return strings.Join(headers, ", "), true
}

func isHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", char)) {
			return false
		}
	}
	return true
}
