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

func TestNamespaceOwnershipIsExclusiveAndPersistent(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(true, []APIKey{
		{Key: "first", Tenant: "team", Scope: "daemon", Identity: "daemon-a"},
		{Key: "second", Tenant: "team", Scope: "daemon", Identity: "daemon-b"},
	}, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ClaimNamespace(context.Background(), "team", "daemon-a"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ClaimNamespace(context.Background(), "team", "daemon-b"); err != ErrNamespaceOwned {
		t.Fatalf("expected namespace conflict, got %v", err)
	}
	reloaded, err := New(true, nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.NamespaceOwner("TEAM"); got != "daemon-a" {
		t.Fatalf("expected persisted owner daemon-a, got %q", got)
	}
	if _, _, err := reloaded.RotateDaemonKey(context.Background(), "second", "daemon-b"); err != ErrNamespaceOwned {
		t.Fatalf("expected impostor rotation rejection, got %v", err)
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

func TestRotateDaemonKeyRequiresIdentityAndRevokesOldKey(t *testing.T) {
	manager, err := New(true, []APIKey{{Key: "daemon-old", Tenant: "team", Scope: "daemon", Identity: "daemon-1"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.RotateDaemonKey(context.Background(), "daemon-old", "other-daemon"); err != ErrDaemonIdentity {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
	replacement, tenant, err := manager.RotateDaemonKey(context.Background(), "Bearer daemon-old", "daemon-1")
	if err != nil || tenant != "team" {
		t.Fatalf("rotation failed: %q %q %v", replacement, tenant, err)
	}
	if _, ok := manager.Authenticate("daemon-old"); ok {
		t.Fatal("old daemon key still authenticates")
	}
	if _, ok := manager.Authenticate(replacement); !ok {
		t.Fatal("replacement daemon key does not authenticate")
	}
	if _, _, err := manager.RotateDaemonKey(context.Background(), "daemon-old", "daemon-1"); err != ErrInvalidCredentials {
		t.Fatalf("expected revoked key error, got %v", err)
	}
}

func TestRotateDaemonKeyRejectsTenantKey(t *testing.T) {
	manager, err := New(true, []APIKey{{Key: "tenant-key", Tenant: "team"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.RotateDaemonKey(context.Background(), "tenant-key", "daemon-1"); err != ErrDaemonCredential {
		t.Fatalf("expected daemon credential error, got %v", err)
	}
}

func TestDaemonPairingIssuesPersistedScopedCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	manager, err := New(true, nil, []PairingCode{{
		Code: "daemon-pair", Tenant: "team", Scope: "daemon", Identity: "daemon-1",
	}}, NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := manager.Exchange(context.Background(), "daemon-pair")
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := manager.AuthenticateCredential(key)
	if !ok || credential.Scope != "daemon" || credential.Identity != "daemon-1" {
		t.Fatalf("unexpected issued credential: %#v %v", credential, ok)
	}
	reloaded, err := New(true, nil, nil, NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok = reloaded.AuthenticateCredential(key)
	if !ok || credential.Scope != "daemon" || credential.Identity != "daemon-1" {
		t.Fatalf("scoped credential did not survive reload: %#v %v", credential, ok)
	}
}

func TestPrivateNamespaceRequiresOwnerOrNamespaceScopedCredential(t *testing.T) {
	store := NewMemoryStore()
	manager, err := New(true, []APIKey{
		{Key: "owner", Tenant: "private", Scope: "daemon", Identity: "daemon-1"},
		{Key: "tenant", Tenant: "private"},
	}, []PairingCode{{Code: "pair", Tenant: "private", Scope: "namespace"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureNamespace(context.Background(), "private", true, "daemon-1"); err != nil {
		t.Fatal(err)
	}
	if !manager.AuthorizeNamespace("owner", "private", "daemon-1") {
		t.Fatal("namespace owner was rejected")
	}
	if manager.AuthorizeNamespace("tenant", "private", "") {
		t.Fatal("tenant key discovered private namespace")
	}
	key, _, err := manager.Exchange(context.Background(), "pair")
	if err != nil {
		t.Fatal(err)
	}
	if !manager.AuthorizeNamespace(key, "private", "") {
		t.Fatal("paired namespace credential was rejected")
	}
	reloaded, err := New(true, nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.AuthorizeNamespace("tenant", "private", "") || !reloaded.AuthorizeNamespace(key, "private", "") {
		t.Fatal("private namespace policy did not persist")
	}
}

func TestRotateDaemonKeyPersistsReplacement(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	manager, err := New(true, []APIKey{{Key: "daemon-old", Tenant: "team", Scope: "daemon", Identity: "daemon-1"}}, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	replacement, _, err := manager.RotateDaemonKey(context.Background(), "daemon-old", "daemon-1")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(true, nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Authenticate("daemon-old"); ok {
		t.Fatal("old key authenticated after reload")
	}
	if _, ok := reloaded.Authenticate(replacement); !ok {
		t.Fatal("replacement key did not persist")
	}
}
