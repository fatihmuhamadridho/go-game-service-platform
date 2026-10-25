package config

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	MongoURL          string
	MongoDatabase     string
	JWTSecret         []byte
	JWTIssuer         string
	JWTAudience       string
	Origins           []string
	TrustedProxyCIDRs []*net.IPNet
	Port              string
	AllowInsecureHTTP bool
}

func Load() (Config, error) {
	mongoURL := strings.TrimSpace(os.Getenv("MONGODB_URL"))
	if mongoURL == "" {
		mongoURL = strings.TrimSpace(os.Getenv("MONGODB_URI"))
	}
	if mongoURL == "" {
		return Config{}, errors.New("MONGODB_URL is required")
	}
	database := strings.TrimSpace(os.Getenv("MONGODB_DATABASE"))
	if database == "" {
		parsed, err := url.Parse(mongoURL)
		if err != nil {
			return Config{}, errors.New("MONGODB_URL is invalid")
		}
		database = strings.Trim(parsed.Path, "/")
	}
	if database == "" {
		return Config{}, errors.New("MONGODB_DATABASE is required when MONGODB_URL has no database path")
	}
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return Config{}, errors.New("JWT_SECRET must contain at least 32 characters")
	}
	issuer := envOr("JWT_ISSUER", "jokihub-admin-api")
	audience := envOr("JWT_AUDIENCE", "jokihub-admin-dashboard")
	origins := splitOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if len(origins) == 0 {
		return Config{}, errors.New("CORS_ALLOWED_ORIGINS must list exact dashboard origins")
	}
	trustedProxies, err := splitCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	port := envOr("PORT", "8080")
	allowInsecureHTTP := strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_ALLOW_INSECURE_HTTP")), "true")
	return Config{MongoURL: mongoURL, MongoDatabase: database, JWTSecret: []byte(secret), JWTIssuer: issuer, JWTAudience: audience, Origins: origins, TrustedProxyCIDRs: trustedProxies, Port: port, AllowInsecureHTTP: allowInsecureHTTP}, nil
}

func splitOrigins(value string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, item := range strings.Split(value, ",") {
		origin := strings.TrimSpace(item)
		parsed, err := url.Parse(origin)
		if origin == "" || err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(origin, "*") {
			continue
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		result = append(result, origin)
	}
	return result
}

func splitCIDRs(value string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	for _, item := range strings.Split(value, ",") {
		cidr := strings.TrimSpace(item)
		if cidr == "" {
			continue
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, errors.New("TRUSTED_PROXY_CIDRS contains an invalid CIDR")
		}
		result = append(result, network)
	}
	return result, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
