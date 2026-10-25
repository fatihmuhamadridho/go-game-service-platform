package auth

import (
	"testing"
	"time"

	"github.com/fatihmuhamadridho/go-game-service-platform/internal/utils"
)

func TestArgon2idHashAndVerify(t *testing.T) {
	passwords := Argon2id{}
	hash, err := passwords.Hash("a strong password")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := passwords.Verify(hash, "a strong password"); err != nil || !ok {
		t.Fatalf("valid password rejected: ok=%v err=%v", ok, err)
	}
	if ok, err := passwords.Verify(hash, "other password"); err != nil || ok {
		t.Fatalf("invalid password accepted: ok=%v err=%v", ok, err)
	}
}

func TestArgon2idRejectsCostlyOrMalformedHash(t *testing.T) {
	if _, err := (Argon2id{}).Verify("$argon2id$v=19$m=999999999,t=3,p=2$c2FsdA$ZGlnZXN0", "x"); err == nil {
		t.Fatal("expected malformed hash rejection")
	}
}

func TestAccessTokenValidatesIssuerAudienceSignatureAndExpiry(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	manager := TokenManager{Secret: []byte("01234567890123456789012345678901"), Issuer: "issuer", Audience: "audience"}
	issued, err := manager.IssueAccess("admin_1", "family_1", now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ParseAccess(issued, now)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "admin_1" || claims.FamilyID != "family_1" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if _, err := manager.ParseAccess(issued, now.Add(16*time.Minute)); err == nil {
		t.Fatal("expired access token accepted")
	}
	wrong := manager
	wrong.Audience = "other"
	if _, err := wrong.ParseAccess(issued, now); err == nil {
		t.Fatal("wrong audience accepted")
	}
}

func TestOpaqueTokenHasAtLeast32RandomBytesAndDigestIsOneWay(t *testing.T) {
	first, err := (TokenManager{}).NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 43 {
		t.Fatalf("opaque token too short: %d", len(first))
	}
	if utils.DigestToken(first) == first || utils.DigestToken(first) == utils.DigestToken("another") {
		t.Fatal("digest function did not isolate token")
	}
}
