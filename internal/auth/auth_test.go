package auth

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAPIKeyAuthenticationAndOneTimePairing(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(true, []APIKey{{Key: "secret", Tenant: "team"}}, []PairingCode{{Code: "pair-me", Tenant: "team"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if tenant, ok := manager.Authenticate("Bearer secret"); !ok || tenant != "team" {
		t.Fatalf("expected team authentication, got %q %v", tenant, ok)
	}
	key, tenant, err := manager.Exchange(context.Background(), "pair-me")
	if err != nil || tenant != "team" {
		t.Fatalf("exchange failed: %q %s %v", key, tenant, err)
	}
	if _, ok := manager.Authenticate(key); !ok {
		t.Fatal("exchanged key did not authenticate")
	}
	if _, _, err := manager.Exchange(context.Background(), "pair-me"); err != ErrPairingCodeUsed {
		t.Fatalf("expected one-time pairing error, got %v", err)
	}
}

func TestFileStorePersistsExchangedKeysAndConsumedCodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	manager, err := New(true, nil, []PairingCode{{Code: "once", Tenant: "tenant"}}, NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := manager.Exchange(context.Background(), "once")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(true, nil, []PairingCode{{Code: "once", Tenant: "tenant"}}, NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Authenticate(key); !ok {
		t.Fatal("persisted key did not authenticate")
	}
	if _, _, err := reloaded.Exchange(context.Background(), "once"); err != ErrPairingCodeUsed {
		t.Fatalf("expected consumed code after reload, got %v", err)
	}
}
