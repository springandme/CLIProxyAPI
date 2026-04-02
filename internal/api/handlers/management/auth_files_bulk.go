package management

import (
	"context"
	"crypto/rand"
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
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const authFileBatchInfoPrefix = "info_"

type authFileBatchImportTaskStatus string

const (
	authFileBatchImportTaskStatusPending   authFileBatchImportTaskStatus = "pending"
	authFileBatchImportTaskStatusRunning   authFileBatchImportTaskStatus = "running"
	authFileBatchImportTaskStatusCompleted authFileBatchImportTaskStatus = "completed"
	authFileBatchImportTaskStatusFailed    authFileBatchImportTaskStatus = "failed"
)

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

var authFileBatchAlwaysEditableColumns = []string{
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
	Rows          []authFileBatchImportRow `json:"rows"`
	CreateMissing bool                     `json:"create_missing,omitempty"`
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
	Created int                    `json:"created"`
	Updated int                    `json:"updated"`
	Skipped int                    `json:"skipped"`
	Files   []string               `json:"files,omitempty"`
	Failed  []authFileBatchFailure `json:"failed,omitempty"`
}

type authFileBatchImportTaskCreateResponse struct {
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

type authFileBatchImportTaskResponse struct {
	TaskID        string                        `json:"task_id"`
	Status        authFileBatchImportTaskStatus `json:"status"`
	TotalRows     int                           `json:"total_rows"`
	ProcessedRows int                           `json:"processed_rows"`
	Created       int                           `json:"created"`
	Updated       int                           `json:"updated"`
	Skipped       int                           `json:"skipped"`
	Failed        int                           `json:"failed"`
	CurrentFile   string                        `json:"current_file,omitempty"`
	Files         []string                      `json:"files,omitempty"`
	Failures      []authFileBatchFailure        `json:"failures,omitempty"`
	Error         string                        `json:"error,omitempty"`
	CreatedAt     time.Time                     `json:"created_at"`
	StartedAt     *time.Time                    `json:"started_at,omitempty"`
	CompletedAt   *time.Time                    `json:"completed_at,omitempty"`
}

type authFileBatchImportTask struct {
	ID            string
	Status        authFileBatchImportTaskStatus
	TotalRows     int
	ProcessedRows int
	Created       int
	Updated       int
	Skipped       int
	Failed        int
	CurrentFile   string
	Files         []string
	Failures      []authFileBatchFailure
	Error         string
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
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

	result := h.importAuthFilesBatch(c.Request.Context(), req.Rows, req.CreateMissing, nil)
	writeAuthFileBatchImportResponse(c, result)
}

func (h *Handler) CreateAuthFilesBatchImportTask(c *gin.Context) {
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

	task := h.createAuthFileBatchImportTask(len(req.Rows))
	go h.runAuthFilesBatchImportTask(task.ID, req.Rows, req.CreateMissing)

	c.JSON(http.StatusAccepted, authFileBatchImportTaskCreateResponse{
		Status: "accepted",
		TaskID: task.ID,
	})
}

func (h *Handler) GetAuthFilesBatchImportTask(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("taskID"))
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id is required"})
		return
	}

	task, ok := h.getAuthFileBatchImportTask(taskID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, task)
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
	for _, key := range authFileBatchAlwaysEditableColumns {
		if !isAuthFileBatchEditableKey(key) {
			continue
		}
		if _, ok := row[key]; ok {
			continue
		}
		row[key] = nil
		editableColumns = append(editableColumns, key)
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

func buildAuthFileBatchMetadataForCreate(row authFileBatchImportRow) (map[string]any, error) {
	expectedType := normalizeBatchAuthType(row.ExpectedType)
	if expectedType == "" {
		return nil, fmt.Errorf("expected_type is required to create missing auth file")
	}

	expectedProvider := normalizeBatchAuthProvider(row.ExpectedProvider)
	currentProvider := normalizeBatchAuthProvider(expectedType)
	if expectedProvider != "" && expectedProvider != currentProvider {
		return nil, fmt.Errorf("provider mismatch: expected %s, got %s", expectedProvider, currentProvider)
	}

	metadata := map[string]any{
		"type": expectedType,
	}
	next, changed, err := applyAuthFileBatchFieldActions(metadata, row.Fields)
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, fmt.Errorf("cannot create missing auth file without any set fields")
	}
	return next, nil
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

func (h *Handler) importAuthFilesBatch(ctx context.Context, rows []authFileBatchImportRow, createMissing bool, progress func(*authFileBatchImportTask)) authFileBatchImportResponse {
	seenNames := make(map[string]struct{}, len(rows))
	changedFiles := make([]string, 0, len(rows))
	failed := make([]authFileBatchFailure, 0)
	created := 0
	updated := 0
	skipped := 0

	task := &authFileBatchImportTask{TotalRows: len(rows)}
	for idx, row := range rows {
		if ctx != nil {
			select {
			case <-ctx.Done():
				failed = append(failed, authFileBatchFailure{Name: "", Error: ctx.Err().Error()})
				task.Error = ctx.Err().Error()
				task.ProcessedRows = idx
				task.Created = created
				task.Updated = updated
				task.Skipped = skipped
				task.Failed = len(failed)
				task.Files = append([]string(nil), changedFiles...)
				task.Failures = append([]authFileBatchFailure(nil), failed...)
				if progress != nil {
					progress(task)
				}
				return authFileBatchImportResponse{
					Status:  "partial",
					Created: created,
					Updated: updated,
					Skipped: skipped,
					Files:   changedFiles,
					Failed:  failed,
				}
			default:
			}
		}

		name := strings.TrimSpace(row.Name)
		task.CurrentFile = name
		if name == "" {
			failed = append(failed, authFileBatchFailure{Name: "", Error: "name is required"})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}
		if _, ok := seenNames[name]; ok {
			failed = append(failed, authFileBatchFailure{Name: name, Error: "duplicate row for auth file"})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}
		seenNames[name] = struct{}{}

		if len(row.Fields) == 0 {
			skipped++
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}

		path, metadata, err := h.readManagedAuthFileJSON(name)
		if err != nil {
			if createMissing && errors.Is(err, errAuthFileNotFound) {
				path, err = h.resolveManagedAuthFilePath(name)
				if err == nil {
					metadata, err = buildAuthFileBatchMetadataForCreate(row)
				}
				if err != nil {
					failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
					task.ProcessedRows = idx + 1
					task.Created = created
					task.Updated = updated
					task.Skipped = skipped
					task.Failed = len(failed)
					task.Files = append([]string(nil), changedFiles...)
					task.Failures = append([]authFileBatchFailure(nil), failed...)
					if progress != nil {
						progress(task)
					}
					continue
				}

				raw, errMarshal := json.Marshal(metadata)
				if errMarshal != nil {
					failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("failed to encode auth file: %v", errMarshal)})
					task.ProcessedRows = idx + 1
					task.Created = created
					task.Updated = updated
					task.Skipped = skipped
					task.Failed = len(failed)
					task.Files = append([]string(nil), changedFiles...)
					task.Failures = append([]authFileBatchFailure(nil), failed...)
					if progress != nil {
						progress(task)
					}
					continue
				}
				if err = h.writeAuthFileAtPath(ctx, path, raw); err != nil {
					failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
					task.ProcessedRows = idx + 1
					task.Created = created
					task.Updated = updated
					task.Skipped = skipped
					task.Failed = len(failed)
					task.Files = append([]string(nil), changedFiles...)
					task.Failures = append([]authFileBatchFailure(nil), failed...)
					if progress != nil {
						progress(task)
					}
					continue
				}

				created++
				changedFiles = append(changedFiles, name)
				task.ProcessedRows = idx + 1
				task.Created = created
				task.Updated = updated
				task.Skipped = skipped
				task.Failed = len(failed)
				task.Files = append([]string(nil), changedFiles...)
				task.Failures = append([]authFileBatchFailure(nil), failed...)
				if progress != nil {
					progress(task)
				}
				continue
			}
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}

		currentType := strings.TrimSpace(fmt.Sprint(metadata["type"]))
		currentProvider := normalizeBatchAuthProvider(currentType)
		if expectedProvider := normalizeBatchAuthProvider(row.ExpectedProvider); expectedProvider != "" && expectedProvider != currentProvider {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("provider mismatch: expected %s, got %s", expectedProvider, currentProvider)})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}
		if expectedType := normalizeBatchAuthType(row.ExpectedType); expectedType != "" && expectedType != normalizeBatchAuthType(currentType) {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("type mismatch: expected %s, got %s", expectedType, normalizeBatchAuthType(currentType))})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}

		nextMetadata, changed, err := applyAuthFileBatchFieldActions(metadata, row.Fields)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}
		if !changed {
			skipped++
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}

		raw, err := json.Marshal(nextMetadata)
		if err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: fmt.Sprintf("failed to encode auth file: %v", err)})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}
		if err = h.writeAuthFileAtPath(ctx, path, raw); err != nil {
			failed = append(failed, authFileBatchFailure{Name: name, Error: err.Error()})
			task.ProcessedRows = idx + 1
			task.Created = created
			task.Updated = updated
			task.Skipped = skipped
			task.Failed = len(failed)
			task.Files = append([]string(nil), changedFiles...)
			task.Failures = append([]authFileBatchFailure(nil), failed...)
			if progress != nil {
				progress(task)
			}
			continue
		}

		updated++
		changedFiles = append(changedFiles, name)
		task.ProcessedRows = idx + 1
		task.Created = created
		task.Updated = updated
		task.Skipped = skipped
		task.Failed = len(failed)
		task.Files = append([]string(nil), changedFiles...)
		task.Failures = append([]authFileBatchFailure(nil), failed...)
		if progress != nil {
			progress(task)
		}
	}

	status := "ok"
	if len(failed) > 0 {
		status = "partial"
	}
	return authFileBatchImportResponse{
		Status:  status,
		Created: created,
		Updated: updated,
		Skipped: skipped,
		Files:   changedFiles,
		Failed:  failed,
	}
}

func writeAuthFileBatchImportResponse(c *gin.Context, result authFileBatchImportResponse) {
	httpStatus := http.StatusOK
	if len(result.Failed) > 0 {
		httpStatus = http.StatusMultiStatus
	}
	c.JSON(httpStatus, result)
}

func (h *Handler) createAuthFileBatchImportTask(totalRows int) *authFileBatchImportTask {
	task := &authFileBatchImportTask{
		ID:        newAuthFileBatchImportTaskID(),
		Status:    authFileBatchImportTaskStatusPending,
		TotalRows: totalRows,
		CreatedAt: time.Now().UTC(),
	}
	h.batchTasksMu.Lock()
	h.batchImportTasks[task.ID] = task
	h.batchTasksMu.Unlock()
	return task
}

func (h *Handler) getAuthFileBatchImportTask(taskID string) (authFileBatchImportTaskResponse, bool) {
	h.batchTasksMu.RLock()
	task, ok := h.batchImportTasks[taskID]
	if !ok || task == nil {
		h.batchTasksMu.RUnlock()
		return authFileBatchImportTaskResponse{}, false
	}
	out := authFileBatchImportTaskResponse{
		TaskID:        task.ID,
		Status:        task.Status,
		TotalRows:     task.TotalRows,
		ProcessedRows: task.ProcessedRows,
		Created:       task.Created,
		Updated:       task.Updated,
		Skipped:       task.Skipped,
		Failed:        task.Failed,
		CurrentFile:   task.CurrentFile,
		Files:         append([]string(nil), task.Files...),
		Failures:      append([]authFileBatchFailure(nil), task.Failures...),
		Error:         task.Error,
		CreatedAt:     task.CreatedAt,
		StartedAt:     cloneTaskTime(task.StartedAt),
		CompletedAt:   cloneTaskTime(task.CompletedAt),
	}
	h.batchTasksMu.RUnlock()
	return out, true
}

func cloneTaskTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (h *Handler) runAuthFilesBatchImportTask(taskID string, rows []authFileBatchImportRow, createMissing bool) {
	startedAt := time.Now().UTC()
	h.batchTasksMu.Lock()
	task := h.batchImportTasks[taskID]
	if task != nil {
		task.Status = authFileBatchImportTaskStatusRunning
		task.StartedAt = &startedAt
	}
	h.batchTasksMu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			completedAt := time.Now().UTC()
			h.batchTasksMu.Lock()
			if task := h.batchImportTasks[taskID]; task != nil {
				task.Status = authFileBatchImportTaskStatusFailed
				task.Error = fmt.Sprintf("panic: %v", recovered)
				task.CompletedAt = &completedAt
			}
			h.batchTasksMu.Unlock()
		}
	}()

	result := h.importAuthFilesBatch(context.Background(), rows, createMissing, func(update *authFileBatchImportTask) {
		h.batchTasksMu.Lock()
		task := h.batchImportTasks[taskID]
		if task != nil {
			task.ProcessedRows = update.ProcessedRows
			task.CurrentFile = update.CurrentFile
			task.Created = update.Created
			task.Updated = update.Updated
			task.Skipped = update.Skipped
			task.Failed = update.Failed
			task.Files = append([]string(nil), update.Files...)
			task.Failures = append([]authFileBatchFailure(nil), update.Failures...)
			task.Error = update.Error
		}
		h.batchTasksMu.Unlock()
	})

	completedAt := time.Now().UTC()
	h.batchTasksMu.Lock()
	task = h.batchImportTasks[taskID]
	if task != nil {
		task.Status = authFileBatchImportTaskStatusCompleted
		task.ProcessedRows = task.TotalRows
		task.CurrentFile = ""
		task.Created = result.Created
		task.Updated = result.Updated
		task.Skipped = result.Skipped
		task.Failed = len(result.Failed)
		task.Files = append([]string(nil), result.Files...)
		task.Failures = append([]authFileBatchFailure(nil), result.Failed...)
		task.CompletedAt = &completedAt
	}
	h.batchTasksMu.Unlock()
}

func newAuthFileBatchImportTaskID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("auth-import-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("auth-import-%x", buf[:])
}
