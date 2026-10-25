package config

import "testing"

func TestSplitOriginsRequiresExactOriginsAndDeduplicates(t *testing.T) {
	origins := splitOrigins("https://admin.example.test, https://admin.example.test, https://admin.example.test/path, *, https://*.example.test, https://user@admin.example.test, https://other.example.test")
	if len(origins) != 2 || origins[0] != "https://admin.example.test" || origins[1] != "https://other.example.test" {
		t.Fatalf("unexpected parsed origins: %#v", origins)
	}
}

func TestSplitCIDRsValidatesTrustedProxyRanges(t *testing.T) {
	proxies, err := splitCIDRs("10.0.0.0/8, 192.0.2.0/24")
	if err != nil || len(proxies) != 2 {
		t.Fatalf("valid proxy ranges rejected: %#v, %v", proxies, err)
	}
	if _, err := splitCIDRs("not-a-cidr"); err == nil {
		t.Fatal("invalid proxy range accepted")
	}
}

func TestLoadAllowsLocalHTTPOnlyForLocalMongo(t *testing.T) {
	t.Setenv("MONGODB_URL", "mongodb://localhost:27017/game_service_platform")
	t.Setenv("MONGODB_DATABASE", "game_service_platform")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("AUTH_ALLOW_INSECURE_HTTP", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("local config rejected: %v", err)
	}
	if !cfg.AllowInsecureHTTP || len(cfg.Origins) != 1 || cfg.Origins[0] != "http://localhost:3000" {
		t.Fatalf("unexpected local config: %#v", cfg)
	}
}

func TestLoadAllowsLocalHTTPModeWithRemoteMongo(t *testing.T) {
	t.Setenv("MONGODB_URL", "mongodb+srv://cluster.example.test/game_service_platform")
	t.Setenv("MONGODB_DATABASE", "game_service_platform")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("AUTH_ALLOW_INSECURE_HTTP", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("local HTTP config rejected with remote MongoDB: %v", err)
	}
	if !cfg.AllowInsecureHTTP {
		t.Fatal("local HTTP option was not enabled")
	}
}

func TestLoadFailsClearlyWithoutOriginAllowlist(t *testing.T) {
	t.Setenv("MONGODB_URL", "mongodb://localhost:27017/game_service_platform")
	t.Setenv("MONGODB_DATABASE", "game_service_platform")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("AUTH_ALLOW_INSECURE_HTTP", "false")
	_, err := Load()
	if err == nil || err.Error() != "CORS_ALLOWED_ORIGINS must list exact dashboard origins" {
		t.Fatalf("unexpected missing-origin error: %v", err)
	}
}
