package codexinspection

import (
	"context"
	"testing"
)

func TestFileRepositoryPersistsRunDetail(t *testing.T) {
	ctx := context.Background()
	repo := NewFileRepository(t.TempDir())

	run, err := repo.CreateCodexInspectionRun(ctx, CodexInspectionRun{
		TriggerType: CodexInspectionTriggerManual,
		Status:      CodexInspectionStatusRunning,
		Settings:    DefaultCodexInspectionConfig(),
	})
	if err != nil {
		t.Fatalf("CreateCodexInspectionRun() error = %v", err)
	}
	if run.ID == 0 {
		t.Fatal("CreateCodexInspectionRun() returned empty id")
	}

	if _, err = repo.InsertCodexInspectionResult(ctx, CodexInspectionResult{
		RunID:          run.ID,
		AccountKey:     "auth-a::idx-a",
		FileName:       "auth-a.json",
		DisplayAccount: "alice@example.com",
		Provider:       "codex",
		Action:         "disable",
		ActionReason:   "quota",
	}); err != nil {
		t.Fatalf("InsertCodexInspectionResult() error = %v", err)
	}
	if _, err = repo.InsertCodexInspectionLog(ctx, CodexInspectionLog{
		RunID:   run.ID,
		Level:   "info",
		Message: "started",
	}); err != nil {
		t.Fatalf("InsertCodexInspectionLog() error = %v", err)
	}

	run.Status = CodexInspectionStatusCompleted
	run.DisableCount = 1
	if err = repo.UpdateCodexInspectionRun(ctx, run); err != nil {
		t.Fatalf("UpdateCodexInspectionRun() error = %v", err)
	}

	reopened := NewFileRepository(t.TempDir())
	reopened.path = repo.path
	gotRun, ok, err := reopened.GetCodexInspectionRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetCodexInspectionRun() error = %v", err)
	}
	if !ok {
		t.Fatal("GetCodexInspectionRun() not found")
	}
	if gotRun.Status != CodexInspectionStatusCompleted || gotRun.DisableCount != 1 {
		t.Fatalf("run = %#v", gotRun)
	}
	results, err := reopened.ListCodexInspectionResults(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListCodexInspectionResults() error = %v", err)
	}
	if len(results) != 1 || results[0].ActionStatus != CodexInspectionActionStatusPending {
		t.Fatalf("results = %#v", results)
	}
	logs, err := reopened.ListCodexInspectionLogs(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListCodexInspectionLogs() error = %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "started" {
		t.Fatalf("logs = %#v", logs)
	}
}

func TestFileRepositoryLatestTrigger(t *testing.T) {
	ctx := context.Background()
	repo := NewFileRepository(t.TempDir())

	first, err := repo.CreateCodexInspectionRun(ctx, CodexInspectionRun{
		TriggerType: CodexInspectionTriggerScheduled,
		TriggerKey:  "interval:60:1",
		StartedAtMS: 100,
	})
	if err != nil {
		t.Fatalf("Create first run: %v", err)
	}
	second, err := repo.CreateCodexInspectionRun(ctx, CodexInspectionRun{
		TriggerType: CodexInspectionTriggerScheduled,
		TriggerKey:  "interval:60:1",
		StartedAtMS: 200,
	})
	if err != nil {
		t.Fatalf("Create second run: %v", err)
	}

	got, ok, err := repo.GetLatestCodexInspectionRunByTrigger(ctx, CodexInspectionTriggerScheduled, "interval:60:1")
	if err != nil {
		t.Fatalf("GetLatestCodexInspectionRunByTrigger() error = %v", err)
	}
	if !ok {
		t.Fatal("latest trigger not found")
	}
	if got.ID != second.ID || got.ID == first.ID {
		t.Fatalf("latest id = %d, want %d", got.ID, second.ID)
	}
}
