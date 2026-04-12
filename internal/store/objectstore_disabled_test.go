package store

import (
	"os"
	"path/filepath"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestObjectTokenStoreReadAuthFileRestoresDisabledState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	authDir := filepath.Join(root, "auths")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	path := filepath.Join(authDir, "disabled.json")
	payload := []byte(`{"type":"codex","email":"disabled@example.com","disabled":true}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := &ObjectTokenStore{}
	auth, err := store.readAuthFile(path, authDir)
	if err != nil {
		t.Fatalf("readAuthFile: %v", err)
	}
	if auth == nil {
		t.Fatal("readAuthFile returned nil auth")
	}
	if !auth.Disabled {
		t.Fatal("expected disabled auth to be restored as disabled")
	}
	if auth.Status != cliproxyauth.StatusDisabled {
		t.Fatalf("auth.Status = %q, want %q", auth.Status, cliproxyauth.StatusDisabled)
	}
}
