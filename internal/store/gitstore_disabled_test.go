package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestGitTokenStoreSaveAndListPreservesDisabledState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	remoteDir := setupGitRemoteRepository(t, root, "master",
		testBranchSpec{name: "master", contents: "remote default branch\n"},
	)

	baseDir := filepath.Join(root, "workspace", "auths")
	store := NewGitTokenStore(remoteDir, "", "", "")
	store.SetBaseDir(baseDir)

	auth := &cliproxyauth.Auth{
		ID:       "disabled.json",
		FileName: "disabled.json",
		Provider: "codex",
		Disabled: false,
		Status:   cliproxyauth.StatusActive,
		Metadata: map[string]any{
			"type":     "codex",
			"email":    "disabled@example.com",
			"disabled": false,
		},
	}

	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatalf("Save: %v", err)
	}

	auth.Disabled = true
	auth.Status = cliproxyauth.StatusDisabled
	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatalf("Save disabled update: %v", err)
	}

	path := filepath.Join(baseDir, "disabled.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(raw) == "" || !strings.Contains(string(raw), `"disabled":true`) {
		t.Fatalf("saved auth payload = %s, want disabled=true", string(raw))
	}

	auths, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("List returned %d auths, want 1", len(auths))
	}

	got := auths[0]
	if !got.Disabled {
		t.Fatal("expected disabled auth to remain disabled after reopen")
	}
	if got.Status != cliproxyauth.StatusDisabled {
		t.Fatalf("got.Status = %q, want %q", got.Status, cliproxyauth.StatusDisabled)
	}
	if rawDisabled, ok := got.Metadata["disabled"].(bool); !ok || !rawDisabled {
		t.Fatalf("got.Metadata[disabled] = %#v, want true", got.Metadata["disabled"])
	}
}
