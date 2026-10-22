package controllers

import (
	"encoding/json"
	"testing"
)

func TestValidSettingsPayload(t *testing.T) {
	valid := map[string]json.RawMessage{
		"siteName": json.RawMessage(`"JokiHub"`), "siteDescription": json.RawMessage(`null`),
		"contactEmail": json.RawMessage(`""`), "contactWhatsapp": json.RawMessage(`""`),
		"socialLinks": json.RawMessage(`[]`), "businessHours": json.RawMessage(`null`),
		"maintenanceMode": json.RawMessage(`false`), "allowRegistration": json.RawMessage(`true`),
		"defaultCurrency": json.RawMessage(`"IDR"`), "timezone": json.RawMessage(`"Asia/Jakarta"`),
	}
	if !validSettingsPayload(valid) {
		t.Fatal("valid source-compatible settings payload rejected")
	}

	missing := map[string]json.RawMessage{}
	for key, value := range valid {
		missing[key] = value
	}
	delete(missing, "timezone")
	if validSettingsPayload(missing) {
		t.Fatal("settings payload without required timezone accepted")
	}

	wrongType := map[string]json.RawMessage{}
	for key, value := range valid {
		wrongType[key] = value
	}
	wrongType["maintenanceMode"] = json.RawMessage(`"false"`)
	if validSettingsPayload(wrongType) {
		t.Fatal("settings payload with string maintenanceMode accepted")
	}
}

func TestValidISODate(t *testing.T) {
	for _, value := range []string{"", "2026-02-28", "2000-02-29"} {
		if !validISODate(value) {
			t.Errorf("validISODate(%q) = false", value)
		}
	}
	for _, value := range []string{"2026-02-31", "2026-2-01", "not-a-date"} {
		if validISODate(value) {
			t.Errorf("validISODate(%q) = true", value)
		}
	}
}
