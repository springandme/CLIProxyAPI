package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const testCodexDefaultBaseURL = "https://chatgpt.com/backend-api/codex"

func writeConfigFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}
	return path
}

func TestPutCodexKeys_PersistsDenoProxyHost(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	cfgPath := writeConfigFixture(t, "codex-api-key: []\n")
	cfg := &config.Config{}
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandler(cfg, cfgPath, manager)

	body := []map[string]any{{
		"api-key":         "sk-test",
		"base-url":        testCodexDefaultBaseURL,
		"deno-proxy-host": "https://relay.example.com",
	}}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/codex-api-key", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PutCodexKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutCodexKeys() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(h.cfg.CodexKey) != 1 {
		t.Fatalf("expected 1 codex key, got %d", len(h.cfg.CodexKey))
	}
	if got := h.cfg.CodexKey[0].DenoProxyHost; got != "https://relay.example.com" {
		t.Fatalf("DenoProxyHost = %q", got)
	}
}

func TestPatchCodexKey_UpdatesDenoProxyHost(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	cfgPath := writeConfigFixture(t, "codex-api-key:\n  - api-key: sk-test\n    base-url: https://chatgpt.com/backend-api/codex\n")
	cfg := &config.Config{
		CodexKey: []config.CodexKey{{
			APIKey:  "sk-test",
			BaseURL: testCodexDefaultBaseURL,
		}},
	}
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandler(cfg, cfgPath, manager)

	body := map[string]any{
		"index": 0,
		"value": map[string]any{
			"deno-proxy-host": "https://relay.example.com",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/codex-api-key", bytes.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchCodexKey(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PatchCodexKey() status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := h.cfg.CodexKey[0].DenoProxyHost; got != "https://relay.example.com" {
		t.Fatalf("DenoProxyHost = %q", got)
	}
}
