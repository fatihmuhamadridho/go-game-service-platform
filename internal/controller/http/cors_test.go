package controllers

import "testing"

func TestIsTrustedRequestOrigin(t *testing.T) {
	allowedOrigins := parseCORSOrigins("https://shop.example")
	tests := []struct {
		name         string
		origin       string
		requestOrigin string
		trusted      bool
	}{
		{name: "same origin", origin: "http://localhost:8080", requestOrigin: "http://localhost:8080", trusted: true},
		{name: "allowed local frontend origin", origin: "http://localhost:3001", requestOrigin: "http://localhost:8080", trusted: true},
		{name: "configured production origin", origin: "https://shop.example", requestOrigin: "https://api.example", trusted: true},
		{name: "untrusted origin", origin: "https://evil.example", requestOrigin: "https://api.example", trusted: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isTrustedRequestOrigin(test.origin, test.requestOrigin, allowedOrigins); got != test.trusted {
				t.Fatalf("isTrustedRequestOrigin(%q, %q) = %v, want %v", test.origin, test.requestOrigin, got, test.trusted)
			}
		})
	}
}
