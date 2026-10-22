package email

import "testing"

func setSMTPConfig(t *testing.T, host, port, secure, from, username, password string) {
	t.Helper()
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_SECURE", secure)
	t.Setenv("SMTP_FROM", from)
	t.Setenv("SMTP_USER", username)
	t.Setenv("SMTP_PASSWORD", password)
}

func TestReadConfigUsesSourceDefaults(t *testing.T) {
	setSMTPConfig(t, "smtp.example.test", "", "", "JokiHub <noreply@example.test>", "", "")
	config, err := ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Port != 587 || config.Secure {
		t.Fatalf("default SMTP transport = port %d secure %t, want 587 false", config.Port, config.Secure)
	}
}

func TestReadConfigInfersSecureForSMTPSPort(t *testing.T) {
	setSMTPConfig(t, "smtp.example.test", "465", "", "noreply@example.test", "", "")
	config, err := ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Secure {
		t.Fatal("port 465 should enable implicit TLS by default")
	}
}

func TestReadConfigRejectsPartialCredentials(t *testing.T) {
	setSMTPConfig(t, "smtp.example.test", "587", "false", "noreply@example.test", "user", "")
	if _, err := ReadConfig(); err == nil {
		t.Fatal("SMTP config with username but no password should fail")
	}
}
