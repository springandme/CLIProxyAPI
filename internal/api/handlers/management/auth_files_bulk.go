package management

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const authFileBatchInfoPrefix = "info_"

var authFileBatchPreferredColumns = []string{
	"disabled",
	"label",
	"email",
	"prefix",
	"proxy_url",
	"priority",
	"note",
	"excluded_models",
	"disable_cooling",
	"deno_proxy_host",
	"websockets",
}

var authFileBatchReadonlyColumns = []string{
	authFileBatchInfoPrefix + "provider",
	authFileBatchInfoPrefix + "type",
	authFileBatchInfoPrefix + "email",
	authFileBatchInfoPrefix + "status",
	authFileBatchInfoPrefix + "status_message",
	authFileBatchInfoPrefix + "disabled",
	authFileBatchInfoPrefix + "runtime_only",
	authFileBatchInfoPrefix + "unavailable",
	authFileBatchInfoPrefix + "size",
	authFileBatchInfoPrefix + "modified",
	authFileBatchInfoPrefix + "last_refresh",
	authFileBatchInfoPrefix + "account_type",
	authFileBatchInfoPrefix + "account",
	authFileBatchInfoPrefix + "plan_type",
	authFileBatchInfoPrefix + "chatgpt_account_id",
	authFileBatchInfoPrefix + "json_keys",
}

type authFileBatchExportRequest struct {
	Names []string `json:"names"`
}

type authFileBatchExportResponse struct {
	Status          string                 `json:"status"`
	EditableColumns []string               `json:"editable_columns"`
	ReadonlyColumns []string               `json:"readonly_columns"`
	Rows            []map[string]any       `json:"rows"`
	Failed          []authFileBatchFailure `json:"failed,omitempty"`
}

type authFileBatchImportRequest struct {
	Rows []authFileBatchImportRow `json:"rows"`
}

type authFileBatchImportRow struct {
	Name             string                              `json:"name"`
	ExpectedProvider string                              `json:"expected_provider,omitempty"`
	ExpectedType     string                              `json:"expected_type,omitempty"`
	Fields           map[string]authFileBatchFieldAction `json:"fields"`
}

type authFileBatchFieldAction struct {
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

type authFileBatchFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type authFileBatchImportResponse struct {
	Status  string                 `json:"status"`
	Updated int                    `json:"updated"`
	Skipped int                    `json:"skipped"`
	Files   []string               `json:"files,omitempty"`
	Failed  []authFileBatchFailure `json:"failed,omitempty"`
}

func (h *Handler) ExportAuthFilesBatch(c *gin.Context) {
	var req authFileBatchExportRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	names := uniqueAuthFileNames(req.Names)
	if len(names) == 0 {
		names = h.exportableAuthFileNames()
	}
	if len(names) == 0 {
		c.JSON(http.StatusOK, authFileBatchExportResponse{
			Status:          "ok",
			EditableColumns: nil,
			ReadonlyColumns: authFileBatchReadonlyColumns,
			Rows:            []map[string]any{},
		})
		return
	}

	rows := make([]map[string]any, 0, len(names))
	editableColumnSet := make(map[string]struct{})
	failed := make([]authFileBatchFailure, 0)

	for _, name := range names {
		row, editableColumns, err := h.buildAuthFileBatchExportRow(name)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			continue
		}
		rows = append(rows, row)
		for _, column := range editableColumns {
			editableColumnSet[column] = struct{}{}
		}
	}

	editableColumns := sortAuthFileBatchEditableColumns(editableColumnSet)
	status := "ok"
	httpStatus := http.StatusOK
	if len(failed) > 0 {
		status = "partial"
		httpStatus = http.StatusMultiStatus
	}
	c.JSON(httpStatus, authFileBatchExportResponse{
		Status:          status,
		EditableColumns: editableColumns,
		ReadonlyColumns: authFileBatchReadonlyColumns,
		Rows:            rows,
		Failed:          failed,
	})
}

func (h *Handler) ImportAuthFilesBatch(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req authFileBatchImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if len(req.Rows) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rows are required"})
		return
	}

	ctx := c.Request.Context()
	seenNames := make(map[string]struct{}, len(req.Rows))
	updatedFiles := make([]string, 0, len(req.Rows))
	failed := make([]authFileBatchFailure, 0)
	skipped := 0

	for _, row := range req.Rows {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			failed = append(failed, authFileBatchFailure{Name: "", Error: "name is required"})
			continue
		}
		if _, ok := seenNames[name]; ok {
			failed = append(failed, authFileBatchFailure{Name: name, Error: "duplicate row for auth file"})
			continue
		}
		seenNames[name] = struct{}{}

		if len(row.Fields) == 0 {
			skipped++
			continue
		}

		path, metadata, err := h.readManagedAuthFileJSON(name)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			continue
		}

		currentType := strings.TrimSpace(fmt.Sprint(metadata["type"]))
		currentProvider := normalizeBatchAuthProvider(currentType)
		if expectedProvider := normalizeBatchAuthProvider(row.ExpectedProvider); expectedProvider != "" && expectedProvider != currentProvider {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("provider mismatch: expected %s, got %s", expectedProvider, currentProvider)})
			continue
		}
		if expectedType := normalizeBatchAuthType(row.ExpectedType); expectedType != "" && expectedType != normalizeBatchAuthType(currentType) {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("type mismatch: expected %s, got %s", expectedType, normalizeBatchAuthType(currentType))})
			continue
		}

		nextMetadata, changed, err := applyAuthFileBatchFieldActions(metadata, row.Fields)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			continue
		}
		if !changed {
			skipped++
			continue
		}

		raw, err := json.Marshal(nextMetadata)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("failed to encode auth file: %v", err)})
			continue
		}
		if err = h.writeAuthFileAtPath(ctx, path, raw); err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			continue
		}
		updatedFiles = append(updatedFiles, name)
	}

	status := "ok"
	httpStatus := http.StatusOK
	if len(failed) > 0 {
		status = "partial"
		httpStatus = http.StatusMultiStatus
	}
	c.JSON(httpStatus, authFileBatchImportResponse{
		Status:  status,
		Updated: len(updatedFiles),
		Skipped: skipped,
		Files:   updatedFiles,
		Failed:  failed,
	})
}

func (h *Handler) exportableAuthFileNames() []string {
	if h == nil || h.cfg == nil {
		return nil
	}
	entries, err := os.ReadDir(h.cfg.AuthDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (h *Handler) buildAuthFileBatchExportRow(name string) (map[string]any, []string, error) {
	path, metadata, err := h.readManagedAuthFileJSON(name)
	if err != nil {
		return nil, nil, err
	}

	row := map[string]any{
		"name": filepath.Base(name),
	}
	editableColumns := make([]string, 0, len(metadata))
	jsonKeys := make([]string, 0, len(metadata))
	for key, value := range metadata {
		if !isAuthFileBatchEditableKey(key) {
			continue
		}
		row[key] = value
		editableColumns = append(editableColumns, key)
		jsonKeys = append(jsonKeys, key)
	}
	sort.Strings(jsonKeys)
	entry := h.authFileEntryForBatch(name, path, metadata)
	populateAuthFileBatchInfoColumns(row, entry, jsonKeys)
	return row, editableColumns, nil
}

func (h *Handler) readManagedAuthFileJSON(name string) (string, map[string]any, error) {
	path, err := h.resolveManagedAuthFilePath(name)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, errAuthFileNotFound
		}
		return "", nil, fmt.Errorf("failed to read file: %w", err)
	}
	var metadata map[string]any
	if err = json.Unmarshal(data, &metadata); err != nil {
		return "", nil, fmt.Errorf("invalid auth file: %w", err)
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	return path, metadata, nil
}

func (h *Handler) resolveManagedAuthFilePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if isUnsafeAuthFileName(name) {
		return "", fmt.Errorf("invalid name")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		return "", fmt.Errorf("name must end with .json")
	}

	targetPath := filepath.Join(h.cfg.AuthDir, filepath.Base(name))
	if auth := h.findAuthForDelete(name); auth != nil {
		if path := strings.TrimSpace(authAttribute(auth, "path")); path != "" {
			targetPath = path
		}
	}
	if !filepath.IsAbs(targetPath) {
		if abs, errAbs := filepath.Abs(targetPath); errAbs == nil {
			targetPath = abs
		}
	}
	return targetPath, nil
}

func (h *Handler) authFileEntryForBatch(name, path string, metadata map[string]any) gin.H {
	if auth := h.findAuthForDelete(name); auth != nil {
		if entry := h.buildAuthFileEntry(auth); entry != nil {
			return entry
		}
	}

	entry := gin.H{
		"name":         filepath.Base(name),
		"path":         path,
		"source":       "file",
		"type":         strings.TrimSpace(fmt.Sprint(metadata["type"])),
		"provider":     normalizeBatchAuthProvider(fmt.Sprint(metadata["type"])),
		"status":       "active",
		"disabled":     false,
		"runtime_only": false,
		"unavailable":  false,
	}
	if info, err := os.Stat(path); err == nil {
		entry["size"] = info.Size()
		entry["modtime"] = info.ModTime()
	}
	if email, ok := metadata["email"].(string); ok && strings.TrimSpace(email) != "" {
		entry["email"] = strings.TrimSpace(email)
	}
	if disabled, ok := metadata["disabled"].(bool); ok {
		entry["disabled"] = disabled
		if disabled {
			entry["status"] = string(coreauth.StatusDisabled)
		}
	}
	if lastRefresh, ok := extractLastRefreshTimestamp(metadata); ok {
		entry["last_refresh"] = lastRefresh
	}
	provider := strings.ToLower(strings.TrimSpace(fmt.Sprint(metadata["type"])))
	if provider == "codex" {
		claims := extractCodexIDTokenClaims(&coreauth.Auth{
			Provider: provider,
			Metadata: metadata,
		})
		if claims != nil {
			entry["id_token"] = claims
		}
	}
	return entry
}

func populateAuthFileBatchInfoColumns(row map[string]any, entry gin.H, jsonKeys []string) {
	if row == nil {
		return
	}
	set := func(column string, value any) {
		if value == nil {
			return
		}
		row[column] = value
	}

	set(authFileBatchInfoPrefix+"provider", firstNonEmpty(entry["provider"], entry["type"]))
	set(authFileBatchInfoPrefix+"type", entry["type"])
	set(authFileBatchInfoPrefix+"email", entry["email"])
	set(authFileBatchInfoPrefix+"status", entry["status"])
	set(authFileBatchInfoPrefix+"status_message", firstNonEmpty(entry["status_message"], entry["statusMessage"]))
	set(authFileBatchInfoPrefix+"disabled", entry["disabled"])
	set(authFileBatchInfoPrefix+"runtime_only", firstNonEmpty(entry["runtime_only"], entry["runtimeOnly"]))
	set(authFileBatchInfoPrefix+"unavailable", entry["unavailable"])
	set(authFileBatchInfoPrefix+"size", entry["size"])
	set(authFileBatchInfoPrefix+"modified", firstNonEmpty(entry["modtime"], entry["modified"], entry["updated_at"]))
	set(authFileBatchInfoPrefix+"last_refresh", firstNonEmpty(entry["last_refresh"], entry["lastRefresh"]))
	set(authFileBatchInfoPrefix+"account_type", entry["account_type"])
	set(authFileBatchInfoPrefix+"account", entry["account"])
	if claims, ok := entry["id_token"].(gin.H); ok {
		set(authFileBatchInfoPrefix+"plan_type", claims["plan_type"])
		set(authFileBatchInfoPrefix+"chatgpt_account_id", claims["chatgpt_account_id"])
	}
	if len(jsonKeys) > 0 {
		set(authFileBatchInfoPrefix+"json_keys", strings.Join(jsonKeys, ", "))
	}
}

func normalizeBatchAuthProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "gemini" {
		return "gemini-cli"
	}
	return value
}

func normalizeBatchAuthType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isAuthFileBatchEditableKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, authFileBatchInfoPrefix) {
		return false
	}
	switch lower {
	case "name", "type", "provider":
		return false
	default:
		return true
	}
}

func sortAuthFileBatchEditableColumns(columns map[string]struct{}) []string {
	if len(columns) == 0 {
		return nil
	}
	ordered := make([]string, 0, len(columns))
	used := make(map[string]struct{}, len(columns))
	for _, preferred := range authFileBatchPreferredColumns {
		if _, ok := columns[preferred]; ok {
			ordered = append(ordered, preferred)
			used[preferred] = struct{}{}
		}
	}
	rest := make([]string, 0, len(columns))
	for column := range columns {
		if _, ok := used[column]; ok {
			continue
		}
		rest = append(rest, column)
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

func cloneAuthFileMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func applyAuthFileBatchFieldActions(metadata map[string]any, fields map[string]authFileBatchFieldAction) (map[string]any, bool, error) {
	if len(fields) == 0 {
		return cloneAuthFileMetadata(metadata), false, nil
	}
	next := cloneAuthFileMetadata(metadata)
	changed := false
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		key = strings.TrimSpace(key)
		if !isAuthFileBatchEditableKey(key) {
			return nil, false, fmt.Errorf("field %q is not editable", key)
		}
		action := fields[key]
		switch strings.ToLower(strings.TrimSpace(action.Op)) {
		case "clear":
			if _, ok := next[key]; ok {
				delete(next, key)
				changed = true
			}
		case "set":
			if current, ok := next[key]; ok && reflect.DeepEqual(current, action.Value) {
				continue
			}
			next[key] = action.Value
			changed = true
		default:
			return nil, false, fmt.Errorf("unsupported operation %q for field %q", action.Op, key)
		}
	}

	return next, changed, nil
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
			return strings.TrimSpace(typed)
		default:
			return value
		}
	}
	return nil
}
