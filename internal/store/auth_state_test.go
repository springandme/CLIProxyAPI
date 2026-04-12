package store

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestAuthDisabledState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		metadata     map[string]any
		wantDisabled bool
		wantStatus   cliproxyauth.Status
	}{
		{
			name:         "missing defaults active",
			metadata:     map[string]any{"type": "codex"},
			wantDisabled: false,
			wantStatus:   cliproxyauth.StatusActive,
		},
		{
			name:         "bool true disables auth",
			metadata:     map[string]any{"disabled": true},
			wantDisabled: true,
			wantStatus:   cliproxyauth.StatusDisabled,
		},
		{
			name:         "string true disables auth",
			metadata:     map[string]any{"disabled": "true"},
			wantDisabled: true,
			wantStatus:   cliproxyauth.StatusDisabled,
		},
		{
			name:         "numeric one disables auth",
			metadata:     map[string]any{"disabled": 1},
			wantDisabled: true,
			wantStatus:   cliproxyauth.StatusDisabled,
		},
		{
			name:         "string false remains active",
			metadata:     map[string]any{"disabled": "false"},
			wantDisabled: false,
			wantStatus:   cliproxyauth.StatusActive,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotDisabled, gotStatus := authDisabledState(tt.metadata)
			if gotDisabled != tt.wantDisabled {
				t.Fatalf("authDisabledState() disabled = %v, want %v", gotDisabled, tt.wantDisabled)
			}
			if gotStatus != tt.wantStatus {
				t.Fatalf("authDisabledState() status = %q, want %q", gotStatus, tt.wantStatus)
			}
		})
	}
}

func TestPersistDisabledMetadata(t *testing.T) {
	t.Parallel()

	auth := &cliproxyauth.Auth{
		Disabled: true,
		Metadata: map[string]any{"type": "codex"},
	}

	persistDisabledMetadata(auth)

	got, ok := auth.Metadata["disabled"].(bool)
	if !ok {
		t.Fatalf("persistDisabledMetadata() did not persist boolean disabled field")
	}
	if !got {
		t.Fatalf("persistDisabledMetadata() disabled = %v, want true", got)
	}
}
