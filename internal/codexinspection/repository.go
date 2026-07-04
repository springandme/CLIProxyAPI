package codexinspection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Repository interface {
	CreateCodexInspectionRun(ctx context.Context, run CodexInspectionRun) (CodexInspectionRun, error)
	UpdateCodexInspectionRun(ctx context.Context, run CodexInspectionRun) error
	InsertCodexInspectionResult(ctx context.Context, result CodexInspectionResult) (CodexInspectionResult, error)
	InsertCodexInspectionLog(ctx context.Context, entry CodexInspectionLog) (CodexInspectionLog, error)
	ListCodexInspectionRuns(ctx context.Context, limit int) ([]CodexInspectionRun, error)
	GetCodexInspectionRun(ctx context.Context, id int64) (CodexInspectionRun, bool, error)
	GetLatestCodexInspectionRunByTrigger(ctx context.Context, triggerType, triggerKey string) (CodexInspectionRun, bool, error)
	ListCodexInspectionResults(ctx context.Context, runID int64) ([]CodexInspectionResult, error)
	ListCodexInspectionLogs(ctx context.Context, runID int64) ([]CodexInspectionLog, error)
}

type FileRepository struct {
	mu     sync.Mutex
	path   string
	state  repositoryState
	loaded bool
}

type repositoryState struct {
	NextRunID    int64                   `json:"nextRunId"`
	NextResultID int64                   `json:"nextResultId"`
	NextLogID    int64                   `json:"nextLogId"`
	Runs         []CodexInspectionRun    `json:"runs"`
	Results      []CodexInspectionResult `json:"results"`
	Logs         []CodexInspectionLog    `json:"logs"`
}

func NewFileRepository(dir string) *FileRepository {
	return &FileRepository{path: filepath.Join(strings.TrimSpace(dir), "state.json")}
}

func (r *FileRepository) CreateCodexInspectionRun(ctx context.Context, run CodexInspectionRun) (CodexInspectionRun, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return CodexInspectionRun{}, err
	}
	now := time.Now().UnixMilli()
	if run.StartedAtMS <= 0 {
		run.StartedAtMS = now
	}
	if run.CreatedAtMS <= 0 {
		run.CreatedAtMS = now
	}
	run.UpdatedAtMS = now
	if run.Status == "" {
		run.Status = CodexInspectionStatusRunning
	}
	if run.SettingsJSON == "" {
		run.SettingsJSON = MarshalCodexInspectionSettings(run.Settings)
	}
	run.ID = r.state.NextRunID
	r.state.NextRunID++
	r.state.Runs = append(r.state.Runs, run)
	if err := r.saveLocked(); err != nil {
		return CodexInspectionRun{}, err
	}
	return run, nil
}

func (r *FileRepository) UpdateCodexInspectionRun(ctx context.Context, run CodexInspectionRun) error {
	_ = ctx
	if run.ID <= 0 {
		return errors.New("codex inspection run id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return err
	}
	run.UpdatedAtMS = time.Now().UnixMilli()
	if run.SettingsJSON == "" {
		run.SettingsJSON = MarshalCodexInspectionSettings(run.Settings)
	}
	for i := range r.state.Runs {
		if r.state.Runs[i].ID == run.ID {
			r.state.Runs[i] = run
			return r.saveLocked()
		}
	}
	return ErrRunNotFound
}

func (r *FileRepository) InsertCodexInspectionResult(ctx context.Context, result CodexInspectionResult) (CodexInspectionResult, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return CodexInspectionResult{}, err
	}
	if result.CreatedAtMS <= 0 {
		result.CreatedAtMS = time.Now().UnixMilli()
	}
	if result.QuotaWindowsJSON == "" && len(result.QuotaWindows) > 0 {
		result.QuotaWindowsJSON = MarshalCodexInspectionQuotaWindows(result.QuotaWindows)
	}
	result.ActionStatus = NormalizeCodexInspectionActionStatus(result.ActionStatus, result.Action)
	for i := range r.state.Results {
		if r.state.Results[i].RunID == result.RunID && r.state.Results[i].AccountKey == result.AccountKey {
			result.ID = r.state.Results[i].ID
			r.state.Results[i] = result
			return result, r.saveLocked()
		}
	}
	result.ID = r.state.NextResultID
	r.state.NextResultID++
	r.state.Results = append(r.state.Results, result)
	if err := r.saveLocked(); err != nil {
		return CodexInspectionResult{}, err
	}
	return result, nil
}

func (r *FileRepository) InsertCodexInspectionLog(ctx context.Context, entry CodexInspectionLog) (CodexInspectionLog, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return CodexInspectionLog{}, err
	}
	if entry.CreatedAtMS <= 0 {
		entry.CreatedAtMS = time.Now().UnixMilli()
	}
	if entry.DetailJSON == "" && entry.Detail != nil {
		if data, err := json.Marshal(entry.Detail); err == nil {
			entry.DetailJSON = string(data)
		}
	}
	entry.ID = r.state.NextLogID
	r.state.NextLogID++
	r.state.Logs = append(r.state.Logs, entry)
	if err := r.saveLocked(); err != nil {
		return CodexInspectionLog{}, err
	}
	return entry, nil
}

func (r *FileRepository) ListCodexInspectionRuns(ctx context.Context, limit int) ([]CodexInspectionRun, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs := append([]CodexInspectionRun(nil), r.state.Runs...)
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAtMS == runs[j].StartedAtMS {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].StartedAtMS > runs[j].StartedAtMS
	})
	if len(runs) > limit {
		runs = runs[:limit]
	}
	for i := range runs {
		runs[i].Settings = UnmarshalCodexInspectionSettings(runs[i].SettingsJSON)
	}
	return runs, nil
}

func (r *FileRepository) GetCodexInspectionRun(ctx context.Context, id int64) (CodexInspectionRun, bool, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return CodexInspectionRun{}, false, err
	}
	for _, run := range r.state.Runs {
		if run.ID == id {
			run.Settings = UnmarshalCodexInspectionSettings(run.SettingsJSON)
			return run, true, nil
		}
	}
	return CodexInspectionRun{}, false, nil
}

func (r *FileRepository) GetLatestCodexInspectionRunByTrigger(ctx context.Context, triggerType, triggerKey string) (CodexInspectionRun, bool, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return CodexInspectionRun{}, false, err
	}
	var latest CodexInspectionRun
	for _, run := range r.state.Runs {
		if run.TriggerType != triggerType || run.TriggerKey != triggerKey {
			continue
		}
		if latest.ID == 0 || run.StartedAtMS > latest.StartedAtMS || (run.StartedAtMS == latest.StartedAtMS && run.ID > latest.ID) {
			latest = run
		}
	}
	if latest.ID == 0 {
		return CodexInspectionRun{}, false, nil
	}
	latest.Settings = UnmarshalCodexInspectionSettings(latest.SettingsJSON)
	return latest, true, nil
}

func (r *FileRepository) ListCodexInspectionResults(ctx context.Context, runID int64) ([]CodexInspectionResult, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return nil, err
	}
	results := make([]CodexInspectionResult, 0)
	for _, result := range r.state.Results {
		if result.RunID == runID {
			result.ActionStatus = NormalizeCodexInspectionActionStatus(result.ActionStatus, result.Action)
			result.QuotaWindows = UnmarshalCodexInspectionQuotaWindows(result.QuotaWindowsJSON)
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].FileName == results[j].FileName {
			return results[i].DisplayAccount < results[j].DisplayAccount
		}
		return results[i].FileName < results[j].FileName
	})
	return results, nil
}

func (r *FileRepository) ListCodexInspectionLogs(ctx context.Context, runID int64) ([]CodexInspectionLog, error) {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(); err != nil {
		return nil, err
	}
	logs := make([]CodexInspectionLog, 0)
	for _, entry := range r.state.Logs {
		if entry.RunID != runID {
			continue
		}
		if strings.TrimSpace(entry.DetailJSON) != "" {
			var detail any
			if err := json.Unmarshal([]byte(entry.DetailJSON), &detail); err == nil {
				entry.Detail = detail
			}
		}
		logs = append(logs, entry)
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].CreatedAtMS == logs[j].CreatedAtMS {
			return logs[i].ID < logs[j].ID
		}
		return logs[i].CreatedAtMS < logs[j].CreatedAtMS
	})
	return logs, nil
}

func (r *FileRepository) loadLocked() error {
	if r.loaded {
		return nil
	}
	r.loaded = true
	if r.state.NextRunID == 0 {
		r.state.NextRunID = 1
	}
	if r.state.NextResultID == 0 {
		r.state.NextResultID = 1
	}
	if r.state.NextLogID == 0 {
		r.state.NextLogID = 1
	}
	if strings.TrimSpace(r.path) == "" {
		return errors.New("codex inspection repository path is empty")
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state repositoryState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	r.state = state
	if r.state.NextRunID == 0 {
		r.state.NextRunID = 1
	}
	if r.state.NextResultID == 0 {
		r.state.NextResultID = 1
	}
	if r.state.NextLogID == 0 {
		r.state.NextLogID = 1
	}
	return nil
}

func (r *FileRepository) saveLocked() error {
	if strings.TrimSpace(r.path) == "" {
		return errors.New("codex inspection repository path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
