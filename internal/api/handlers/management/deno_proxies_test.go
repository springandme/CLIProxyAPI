package management

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPutDenoProxies_NormalizesAndPersists(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	cfgPath := writeTestConfigFile(t)
	h := &Handler{
		cfg:            &config.Config{},
		configFilePath: cfgPath,
	}

	body := []string{
		"https://Relay.Example.com/",
		"https://relay.example.com",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/v0/management/deno-proxies", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PutDenoProxies(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(h.cfg.DenoProxies) != 1 || h.cfg.DenoProxies[0] != "https://relay.example.com" {
		t.Fatalf("cfg.DenoProxies = %#v", h.cfg.DenoProxies)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "https://relay.example.com") {
		t.Fatalf("persisted config = %s", data)
	}
}

func TestPatchDenoProxies_AddRemove(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	h := &Handler{
		cfg: &config.Config{
			DenoProxies: []string{
				"https://a.example.com",
				"https://b.example.com",
			},
		},
		configFilePath: writeTestConfigFile(t),
	}

	body := map[string]any{
		"add":    []string{"https://c.example.com/", "https://b.example.com"},
		"remove": []string{"https://a.example.com/"},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/deno-proxies", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PatchDenoProxies(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := []string{"https://b.example.com", "https://c.example.com"}
	if len(h.cfg.DenoProxies) != len(want) {
		t.Fatalf("cfg.DenoProxies = %#v, want %#v", h.cfg.DenoProxies, want)
	}
	for i := range want {
		if h.cfg.DenoProxies[i] != want[i] {
			t.Fatalf("cfg.DenoProxies = %#v, want %#v", h.cfg.DenoProxies, want)
		}
	}
}

func TestGetDenoProxies_AggregatesUsageAndUnmanaged(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	writeAuthFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(authDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile(%s) error = %v", name, err)
		}
	}
	writeAuthFile("codex-user.json", `{
	  "type":"codex",
	  "email":"user@example.com",
	  "deno_proxy_host":"https://managed.example.com/"
	}`)
	writeAuthFile("legacy.json", `{
	  "type":"codex",
	  "email":"legacy@example.com",
	  "deno_proxy_host":"legacy.example.com/"
	}`)

	manager := coreauth.NewManager(nil, nil, nil)
	_, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-file-auth",
		Provider: "codex",
		FileName: "codex-user.json",
		Metadata: map[string]any{"email": "user@example.com", "deno_proxy_host": "https://managed.example.com"},
	})
	if err != nil {
		t.Fatalf("manager.Register(file auth) error = %v", err)
	}
	_, err = manager.Register(context.Background(), &coreauth.Auth{
		ID:         "runtime-only-codex",
		Provider:   "codex",
		Label:      "Runtime Only",
		Attributes: map[string]string{"runtime_only": "true", "deno_proxy_host": "https://runtime-only.example.com"},
	})
	if err != nil {
		t.Fatalf("manager.Register(runtime auth) error = %v", err)
	}
	_, err = manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-runtime-shadow",
		Provider: "codex",
		Label:    "user@example.com",
		Metadata: map[string]any{"email": "user@example.com", "deno_proxy_host": "https://managed.example.com"},
	})
	if err != nil {
		t.Fatalf("manager.Register(runtime shadow auth) error = %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			AuthDir: authDir,
			DenoProxies: []string{
				"https://managed.example.com",
				"https://unused.example.com",
			},
			CodexKey: []config.CodexKey{
				{
					APIKey:        "sk-test",
					BaseURL:       "https://chatgpt.com/backend-api/codex",
					DenoProxyHost: "https://managed.example.com",
				},
			},
		},
		authManager: manager,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/deno-proxies", nil)

	h.GetDenoProxies(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp denoProxyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("len(resp.Items) = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].Host != "https://managed.example.com" {
		t.Fatalf("managed host = %q", resp.Items[0].Host)
	}
	if resp.Items[0].UsageCount != 2 {
		t.Fatalf("managed usage_count = %d, want 2", resp.Items[0].UsageCount)
	}
	if !resp.Items[1].Unused || resp.Items[1].Host != "https://unused.example.com" {
		t.Fatalf("unused item = %#v", resp.Items[1])
	}
	if resp.Items[1].UsedBy == nil || len(resp.Items[1].UsedBy) != 0 {
		t.Fatalf("unused used_by = %#v, want empty slice", resp.Items[1].UsedBy)
	}
	if len(resp.UnmanagedInUse) != 2 {
		t.Fatalf("len(resp.UnmanagedInUse) = %d, want 2", len(resp.UnmanagedInUse))
	}
	if resp.UnmanagedInUse[0].Host != "https://legacy.example.com" {
		t.Fatalf("first unmanaged host = %q", resp.UnmanagedInUse[0].Host)
	}
	if resp.UnmanagedInUse[1].Host != "https://runtime-only.example.com" {
		t.Fatalf("second unmanaged host = %q", resp.UnmanagedInUse[1].Host)
	}
}

func TestProbeDenoProxy_Success(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Codex Deno proxy is running!"))
		case "/robots.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("User-agent: *"))
		case denoProxyProbeHTTPPath:
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("missing auth"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	body := map[string]string{"host": server.URL}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	h := &Handler{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/deno-proxies/probe", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ProbeDenoProxy(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp denoProxyProbeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !resp.Root.OK || !resp.Robots.OK || !resp.CodexHTTP.OK || !resp.CodexWebsocket.OK {
		t.Fatalf("probe response = %#v", resp)
	}
}

func TestProbeDenoProxy_WebsocketFailure(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.WriteHeader(http.StatusOK)
		case "/robots.txt":
			w.WriteHeader(http.StatusOK)
		case denoProxyProbeHTTPPath:
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resp := probeDenoProxyHost(context.Background(), server.URL)
	if !resp.CodexHTTP.OK {
		t.Fatalf("codex http should be reachable: %#v", resp.CodexHTTP)
	}
	if resp.CodexWebsocket.OK {
		t.Fatalf("codex websocket should fail: %#v", resp.CodexWebsocket)
	}
}

func TestProbeDenoProxy_InvalidHost(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	body := map[string]string{"host": "https://relay.example.com/codex"}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	h := &Handler{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/deno-proxies/probe", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ProbeDenoProxy(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestProbeDenoProxy_Timeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(denoProxyProbeTimeout + time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp := probeDenoProxyHost(context.Background(), server.URL)
	if resp.Root.Error == "" {
		t.Fatalf("expected timeout error, got %#v", resp.Root)
	}
}
