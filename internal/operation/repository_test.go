package operation

import "testing"

func TestAboutBrandMigrationOnlyRewritesKnownCopyFields(t *testing.T) {
	updates := aboutBrandMigrationUpdates(map[string]any{
		"title":   "Tentang JokiHub",
		"summary": "jokihub melayani pemain",
		"content": "JOKIHUB",
		"slug":    "jokihub-page",
		"custom":  42,
	})
	if len(updates) != 3 {
		t.Fatalf("expected 3 copy updates, got %#v", updates)
	}
	want := map[string]string{
		"data.title":   "Tentang Bongastore",
		"data.summary": "bongastore melayani pemain",
		"data.content": "BONGASTORE",
	}
	for _, update := range updates {
		value, ok := update.Value.(string)
		if !ok || want[update.Key] != value {
			t.Fatalf("unexpected migration update: %#v", update)
		}
		delete(want, update.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing migration fields: %#v", want)
	}
}

func TestAboutBrandMigrationPreservesNonLegacyText(t *testing.T) {
	updates := aboutBrandMigrationUpdates(map[string]any{
		"title":   "Tentang Bongastore",
		"summary": "User configured copy",
		"content": "Contact support",
		"slug":    "jokihub-page",
	})
	if len(updates) != 0 {
		t.Fatalf("migration changed custom copy or slug: %#v", updates)
	}
}
