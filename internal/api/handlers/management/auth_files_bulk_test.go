package management

import (
	"bytes"
	"context"
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

func TestBuildAuthFromFileData_MapsEditableFields(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	auth, err := h.buildAuthFromFileData(filepath.Join(authDir, "codex.json"), []byte(`{
		"type":"codex",
		"email":"user@example.com",
		"disabled":true,
		"prefix":"team-a",
		"proxy_url":"socks5://127.0.0.1:1080",
		"priority":12,
		"note":"owner-a",
		"deno_proxy_host":"https://relay.example.com"
	}`))
	if err != nil {
		t.Fatalf("buildAuthFromFileData returned error: %v", err)
	}

	if auth.Provider != "codex" {
		t.Fatalf("expected provider codex, got %q", auth.Provider)
	}
	if auth.Prefix != "team-a" {
		t.Fatalf("expected prefix team-a, got %q", auth.Prefix)
	}
	if auth.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("expected proxy_url to be mapped, got %q", auth.ProxyURL)
	}
	if !auth.Disabled || auth.Status != coreauth.StatusDisabled {
		t.Fatalf("expected auth to be disabled, got disabled=%v status=%v", auth.Disabled, auth.Status)
	}
	if got := auth.Attributes["priority"]; got != "12" {
		t.Fatalf("expected priority attribute to be 12, got %q", got)
	}
	if got := auth.Attributes["note"]; got != "owner-a" {
		t.Fatalf("expected note attribute to be owner-a, got %q", got)
	}
	if got := auth.Attributes["deno_proxy_host"]; got != "https://relay.example.com" {
		t.Fatalf("expected deno_proxy_host attribute to be set, got %q", got)
	}
}

func TestExportAuthFilesBatch_ReturnsDynamicColumnsAndInfo(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	fileBody := `{
		"type":"codex",
		"email":"codex@example.com",
		"prefix":"team-a",
		"websockets":true,
		"token":{"access_token":"abc"},
		"excluded_models":["gpt-5-*"]
	}`
	if err := os.WriteFile(filepath.Join(authDir, fileName), []byte(fileBody), 0o600); err != nil {
		t.Fatalf("failed to seed auth file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := hBuildAndRegisterAuth(t, authDir, manager, fileName, []byte(fileBody))
	if err != nil {
		t.Fatalf("failed to register auth: %v", err)
	}
	if _, err = manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("failed to register auth in manager: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	body := bytes.NewBufferString(`{"names":["codex-user.json"]}`)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/batch-export", body)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ExportAuthFilesBatch(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected export status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload authFileBatchExportResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode export payload: %v", err)
	}
	if len(payload.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(payload.Rows))
	}
	row := payload.Rows[0]
	if _, ok := row["type"]; ok {
		t.Fatalf("expected type to be exported as readonly info column, row=%#v", row)
	}
	if got := row["prefix"]; got != "team-a" {
		t.Fatalf("expected editable prefix column, got %#v", got)
	}
	if got := row[authFileBatchInfoPrefix+"type"]; got != "codex" {
		t.Fatalf("expected info_type column, got %#v", got)
	}
	if got := row[authFileBatchInfoPrefix+"status"]; got == nil {
		t.Fatalf("expected info_status column to be present")
	}
	if _, ok := row["token"].(map[string]any); !ok {
		t.Fatalf("expected token column to remain structured JSON, got %#v", row["token"])
	}
	if !containsString(payload.EditableColumns, "token") {
		t.Fatalf("expected token in editable columns: %#v", payload.EditableColumns)
	}
	if !containsString(payload.ReadonlyColumns, authFileBatchInfoPrefix+"type") {
		t.Fatalf("expected info_type in readonly columns: %#v", payload.ReadonlyColumns)
	}
}

func TestImportAuthFilesBatch_AppliesSetAndClearOperations(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	original := `{
		"type":"codex",
		"email":"codex@example.com",
		"prefix":"old-prefix",
		"proxy_url":"socks5://127.0.0.1:1080",
		"priority":5,
		"note":"old-note",
		"websockets":true,
		"token":{"access_token":"old-token"}
	}`
	if err := os.WriteFile(filepath.Join(authDir, fileName), []byte(original), 0o600); err != nil {
		t.Fatalf("failed to seed auth file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := hBuildAndRegisterAuth(t, authDir, manager, fileName, []byte(original))
	if err != nil {
		t.Fatalf("failed to build auth: %v", err)
	}
	if _, err = manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("failed to register auth: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	reqBody := `{
		"rows":[
			{
				"name":"codex-user.json",
				"expected_provider":"codex",
				"expected_type":"codex",
				"fields":{
					"prefix":{"op":"clear"},
					"proxy_url":{"op":"set","value":"https://proxy.example.com"},
					"note":{"op":"set","value":"new-note"},
					"token":{"op":"set","value":{"access_token":"new-token","refresh_token":"refresh-token"}},
					"websockets":{"op":"set","value":false}
				}
			}
		]
	}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/batch-import", bytes.NewBufferString(reqBody))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ImportAuthFilesBatch(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected import status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var stored map[string]any
	raw, err := os.ReadFile(filepath.Join(authDir, fileName))
	if err != nil {
		t.Fatalf("failed to read updated auth file: %v", err)
	}
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("failed to decode updated auth file: %v", err)
	}
	if _, ok := stored["prefix"]; ok {
		t.Fatalf("expected prefix to be cleared, got %#v", stored["prefix"])
	}
	if got := stored["proxy_url"]; got != "https://proxy.example.com" {
		t.Fatalf("expected proxy_url to be updated, got %#v", got)
	}
	if got := stored["note"]; got != "new-note" {
		t.Fatalf("expected note to be updated, got %#v", got)
	}
	if got := stored["websockets"]; got != false {
		t.Fatalf("expected websockets false, got %#v", got)
	}
	tokenMap, ok := stored["token"].(map[string]any)
	if !ok {
		t.Fatalf("expected token object to be updated, got %#v", stored["token"])
	}
	if got := tokenMap["access_token"]; got != "new-token" {
		t.Fatalf("expected token access_token to be updated, got %#v", got)
	}

	updatedAuth, ok := manager.GetByID(fileName)
	if !ok {
		t.Fatalf("expected updated auth to remain registered")
	}
	if updatedAuth.Prefix != "" {
		t.Fatalf("expected runtime auth prefix to be cleared, got %q", updatedAuth.Prefix)
	}
	if updatedAuth.ProxyURL != "https://proxy.example.com" {
		t.Fatalf("expected runtime auth proxy_url to be updated, got %q", updatedAuth.ProxyURL)
	}
	if got := updatedAuth.Attributes["note"]; got != "new-note" {
		t.Fatalf("expected runtime auth note attribute to be updated, got %q", got)
	}
}

func TestImportAuthFilesBatch_RejectsProviderMismatch(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	if err := os.WriteFile(filepath.Join(authDir, fileName), []byte(`{"type":"codex","email":"codex@example.com"}`), 0o600); err != nil {
		t.Fatalf("failed to seed auth file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := hBuildAndRegisterAuth(t, authDir, manager, fileName, []byte(`{"type":"codex","email":"codex@example.com"}`))
	if err != nil {
		t.Fatalf("failed to build auth: %v", err)
	}
	if _, err = manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("failed to register auth: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	reqBody := `{
		"rows":[
			{
				"name":"codex-user.json",
				"expected_provider":"claude",
				"fields":{
					"note":{"op":"set","value":"should-fail"}
				}
			}
		]
	}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/batch-import", bytes.NewBufferString(reqBody))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ImportAuthFilesBatch(ctx)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected import status %d, got %d with body %s", http.StatusMultiStatus, rec.Code, rec.Body.String())
	}

	var payload authFileBatchImportResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode import payload: %v", err)
	}
	if payload.Updated != 0 {
		t.Fatalf("expected no updated files, got %d", payload.Updated)
	}
	if len(payload.Failed) != 1 {
		t.Fatalf("expected one failed row, got %#v", payload.Failed)
	}
}

func hBuildAndRegisterAuth(t *testing.T, authDir string, manager *coreauth.Manager, name string, data []byte) (*coreauth.Auth, error) {
	t.Helper()
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	return h.buildAuthFromFileData(filepath.Join(authDir, name), data)
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
