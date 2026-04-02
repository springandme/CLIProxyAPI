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
		"label":"Team Owner",
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
	if auth.Label != "Team Owner" {
		t.Fatalf("expected label Team Owner, got %q", auth.Label)
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

func TestExportAuthFilesBatch_IncludesPreferredEditableColumnsWhenMissing(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	fileBody := `{
		"type":"codex",
		"email":"codex@example.com"
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

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/batch-export", bytes.NewBufferString(`{"names":["codex-user.json"]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ExportAuthFilesBatch(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected export status %d, got %d with body %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var payload authFileBatchExportResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode export payload: %v", err)
	}
	for _, column := range []string{"disabled", "label", "prefix", "proxy_url", "note", "deno_proxy_host", "websockets"} {
		if !containsString(payload.EditableColumns, column) {
			t.Fatalf("expected editable column %q in payload: %#v", column, payload.EditableColumns)
		}
	}
	if len(payload.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(payload.Rows))
	}
	row := payload.Rows[0]
	if _, ok := row["disabled"]; !ok {
		t.Fatalf("expected disabled column to exist in row: %#v", row)
	}
	if _, ok := row["websockets"]; !ok {
		t.Fatalf("expected websockets column to exist in row: %#v", row)
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

func TestCreateAuthFilesBatchImportTask_CompletesAndTracksProgress(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	fileName := "codex-user.json"
	original := `{
		"type":"codex",
		"email":"codex@example.com",
		"note":"old-note"
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
					"note":{"op":"set","value":"task-note"},
					"disabled":{"op":"set","value":true}
				}
			}
		]
	}`
	createRec := httptest.NewRecorder()
	createCtx, _ := gin.CreateTestContext(createRec)
	createCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files/batch-import-tasks", bytes.NewBufferString(reqBody))
	createCtx.Request.Header.Set("Content-Type", "application/json")

	h.CreateAuthFilesBatchImportTask(createCtx)

	if createRec.Code != http.StatusAccepted {
		t.Fatalf("expected create task status %d, got %d with body %s", http.StatusAccepted, createRec.Code, createRec.Body.String())
	}

	var createPayload authFileBatchImportTaskCreateResponse
	if err = json.Unmarshal(createRec.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("failed to decode create task payload: %v", err)
	}
	if createPayload.TaskID == "" {
		t.Fatalf("expected non-empty task id")
	}

	var taskPayload authFileBatchImportTaskResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		getRec := httptest.NewRecorder()
		getCtx, _ := gin.CreateTestContext(getRec)
		getCtx.Params = gin.Params{{Key: "taskID", Value: createPayload.TaskID}}
		getCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files/batch-import-tasks/"+createPayload.TaskID, nil)

		h.GetAuthFilesBatchImportTask(getCtx)
		if getRec.Code != http.StatusOK {
			t.Fatalf("expected get task status %d, got %d with body %s", http.StatusOK, getRec.Code, getRec.Body.String())
		}
		if err = json.Unmarshal(getRec.Body.Bytes(), &taskPayload); err != nil {
			t.Fatalf("failed to decode task payload: %v", err)
		}
		if taskPayload.Status == authFileBatchImportTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if taskPayload.Status != authFileBatchImportTaskStatusCompleted {
		t.Fatalf("expected task to complete, got %#v", taskPayload)
	}
	if taskPayload.TotalRows != 1 || taskPayload.ProcessedRows != 1 {
		t.Fatalf("expected processed rows 1/1, got %#v", taskPayload)
	}
	if taskPayload.Updated != 1 || taskPayload.Skipped != 0 || taskPayload.Failed != 0 {
		t.Fatalf("unexpected task counters: %#v", taskPayload)
	}
	if taskPayload.CurrentFile != "" {
		t.Fatalf("expected current file to be cleared after completion, got %q", taskPayload.CurrentFile)
	}
	if len(taskPayload.Files) != 1 || taskPayload.Files[0] != fileName {
		t.Fatalf("unexpected updated files: %#v", taskPayload.Files)
	}

	raw, err := os.ReadFile(filepath.Join(authDir, fileName))
	if err != nil {
		t.Fatalf("failed to read updated auth file: %v", err)
	}
	var stored map[string]any
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("failed to decode updated auth file: %v", err)
	}
	if got := stored["note"]; got != "task-note" {
		t.Fatalf("expected note to be updated, got %#v", got)
	}
	if got := stored["disabled"]; got != true {
		t.Fatalf("expected disabled to be updated, got %#v", got)
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

func TestImportAuthFilesBatch_CreateMissingRestoresDeletedAuthFile(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	reqBody := `{
		"create_missing": true,
		"rows":[
			{
				"name":"restored-codex.json",
				"expected_provider":"codex",
				"expected_type":"codex",
				"fields":{
					"email":{"op":"set","value":"restored@example.com"},
					"access_token":{"op":"set","value":"access-token"},
					"refresh_token":{"op":"set","value":"refresh-token"},
					"deno_proxy_host":{"op":"set","value":"https://relay.example.com"},
					"websockets":{"op":"set","value":true}
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

	var payload authFileBatchImportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode import payload: %v", err)
	}
	if payload.Created != 1 || payload.Updated != 0 || payload.Skipped != 0 || len(payload.Failed) != 0 {
		t.Fatalf("unexpected import payload: %#v", payload)
	}

	raw, err := os.ReadFile(filepath.Join(authDir, "restored-codex.json"))
	if err != nil {
		t.Fatalf("failed to read restored auth file: %v", err)
	}
	var stored map[string]any
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("failed to decode restored auth file: %v", err)
	}
	if got := stored["type"]; got != "codex" {
		t.Fatalf("expected restored type codex, got %#v", got)
	}
	if got := stored["deno_proxy_host"]; got != "https://relay.example.com" {
		t.Fatalf("expected deno relay host to be restored, got %#v", got)
	}
	if got := stored["websockets"]; got != true {
		t.Fatalf("expected websockets true, got %#v", got)
	}

	auth, ok := manager.GetByID("restored-codex.json")
	if !ok {
		t.Fatalf("expected restored auth to be registered")
	}
	if auth.Provider != "codex" {
		t.Fatalf("expected runtime auth provider codex, got %q", auth.Provider)
	}
	if auth.Attributes["deno_proxy_host"] != "https://relay.example.com" {
		t.Fatalf("expected runtime auth deno relay host, got %q", auth.Attributes["deno_proxy_host"])
	}
}

func TestImportAuthFilesBatch_CreateMissingRequiresExpectedType(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	reqBody := `{
		"create_missing": true,
		"rows":[
			{
				"name":"restored-codex.json",
				"expected_provider":"codex",
				"fields":{
					"access_token":{"op":"set","value":"access-token"}
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
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode import payload: %v", err)
	}
	if payload.Created != 0 || payload.Updated != 0 {
		t.Fatalf("expected no created or updated files, got %#v", payload)
	}
	if len(payload.Failed) != 1 || !strings.Contains(payload.Failed[0].Error, "expected_type is required") {
		t.Fatalf("unexpected failures: %#v", payload.Failed)
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
