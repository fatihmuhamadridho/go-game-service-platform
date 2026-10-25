package game

import "testing"

func TestValidateGameIDRequiresCanonicalCrockfordULID(t *testing.T) {
	valid := "game_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err := ValidateGameID(valid); err != nil {
		t.Fatalf("canonical game ID rejected: %v", err)
	}
	for _, invalid := range []string{
		"game_81ARZ3NDEKTSV4RRFFQ69G5FAV", // ULID overflow bits
		"game_01arz3ndektsv4rrffq69g5fav", // non-canonical lowercase
		"game_01ARZ3NDEKTSV4RRFFQ69G5FAI", // excluded Crockford character
		"game_short",
	} {
		if err := ValidateGameID(invalid); err == nil {
			t.Errorf("invalid game ID accepted: %q", invalid)
		}
	}
}

func TestGenerateGameIDUsesCanonicalPrefixAndULID(t *testing.T) {
	id, err := NewGameID()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGameID(id); err != nil {
		t.Fatalf("generated ID %q is invalid: %v", id, err)
	}
}

func TestGameValidationNormalizesNameAndEnforcesHTTPSImage(t *testing.T) {
	game := &Game{Name: "  Mobile Legends ", Slug: "mobile-legends", ImageURL: "https://cdn.example.test/game.webp", SortOrder: 0}
	if err := ValidateGame(game); err != nil {
		t.Fatalf("valid game rejected: %v", err)
	}
	if game.Name != "Mobile Legends" {
		t.Fatalf("name was not trimmed: %q", game.Name)
	}
	for _, imageURL := range []string{"http://cdn.example.test/game.webp", "//cdn.example.test/game.webp", "javascript:alert(1)"} {
		candidate := *game
		candidate.ImageURL = imageURL
		if err := ValidateGame(&candidate); err == nil {
			t.Errorf("invalid image URL accepted: %q", imageURL)
		}
	}
}
