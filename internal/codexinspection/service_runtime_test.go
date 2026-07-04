package codexinspection

import (
	"context"
	"net/http"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestServiceRuntimeShortWindowAutoDisableAndRestore(t *testing.T) {
	ctx := context.Background()
	repo := NewFileRepository(t.TempDir())
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(ctx, &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-auth.json",
		Label:    "Codex A",
		Metadata: map[string]any{
			"account_id": "acct-1",
			"email":      "codex@example.com",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	service := New(
		repo,
		func() ManagerCodexInspectionConfig {
			cfg := DefaultCodexInspectionConfig()
			cfg.ShortWindowAutoDisable = true
			return cfg
		},
		func() string { return "" },
		func() *coreauth.Manager { return manager },
	)

	retryAfter := 5 * time.Hour
	service.OnResult(ctx, coreauth.Result{
		AuthID:     "auth-1",
		Provider:   "codex",
		Model:      "codex-mini-latest",
		Success:    false,
		RetryAfter: &retryAfter,
		Error: &coreauth.Error{
			Code:       "usage_limit_reached",
			Message:    "usage limit reached",
			HTTPStatus: http.StatusTooManyRequests,
		},
	})

	disabledAuth, ok := manager.GetByID("auth-1")
	if !ok || disabledAuth == nil {
		t.Fatal("auth not found after auto-disable")
	}
	if !disabledAuth.Disabled || disabledAuth.Status != coreauth.StatusDisabled {
		t.Fatalf("auth disabled state = disabled %v status %q", disabledAuth.Disabled, disabledAuth.Status)
	}
	cooldowns, err := repo.ListCodexInspectionCooldowns(ctx, false, 10)
	if err != nil {
		t.Fatalf("ListCodexInspectionCooldowns() error = %v", err)
	}
	if len(cooldowns) != 1 {
		t.Fatalf("cooldowns len = %d, want 1: %#v", len(cooldowns), cooldowns)
	}
	if cooldowns[0].Status != CodexInspectionCooldownPending || cooldowns[0].WindowID != "five-hour" || cooldowns[0].AccountID != "acct-1" {
		t.Fatalf("cooldown = %#v", cooldowns[0])
	}

	if err := service.ProcessDueCooldowns(ctx, time.UnixMilli(cooldowns[0].RestoreAtMS).Add(time.Second)); err != nil {
		t.Fatalf("ProcessDueCooldowns() error = %v", err)
	}
	restoredAuth, ok := manager.GetByID("auth-1")
	if !ok || restoredAuth == nil {
		t.Fatal("auth not found after restore")
	}
	if restoredAuth.Disabled || restoredAuth.Status != coreauth.StatusActive {
		t.Fatalf("auth restored state = disabled %v status %q", restoredAuth.Disabled, restoredAuth.Status)
	}
	resolved, err := repo.ListCodexInspectionCooldowns(ctx, true, 10)
	if err != nil {
		t.Fatalf("ListCodexInspectionCooldowns(includeResolved) error = %v", err)
	}
	if len(resolved) != 1 || resolved[0].Status != CodexInspectionCooldownRestored || resolved[0].RestoredAtMS == 0 {
		t.Fatalf("resolved cooldowns = %#v", resolved)
	}
}

func TestServiceRuntimeShortWindowAutoDisableDefaultOff(t *testing.T) {
	ctx := context.Background()
	repo := NewFileRepository(t.TempDir())
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(ctx, &coreauth.Auth{
		ID:       "auth-1",
		Provider: "codex",
		FileName: "codex-auth.json",
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	service := New(
		repo,
		DefaultCodexInspectionConfig,
		func() string { return "" },
		func() *coreauth.Manager { return manager },
	)

	retryAfter := 5 * time.Hour
	service.OnResult(ctx, coreauth.Result{
		AuthID:     "auth-1",
		Provider:   "codex",
		Success:    false,
		RetryAfter: &retryAfter,
		Error: &coreauth.Error{
			Code:       "usage_limit_reached",
			Message:    "usage limit reached",
			HTTPStatus: http.StatusTooManyRequests,
		},
	})

	auth, ok := manager.GetByID("auth-1")
	if !ok || auth == nil {
		t.Fatal("auth not found")
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		t.Fatalf("auth should remain enabled by default: disabled %v status %q", auth.Disabled, auth.Status)
	}
	cooldowns, err := repo.ListCodexInspectionCooldowns(ctx, true, 10)
	if err != nil {
		t.Fatalf("ListCodexInspectionCooldowns() error = %v", err)
	}
	if len(cooldowns) != 0 {
		t.Fatalf("cooldowns = %#v, want empty", cooldowns)
	}
}
