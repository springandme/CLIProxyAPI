package executor

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestApplyCodexDenoProxy_RewritesOfficialResponsesEndpoints(t *testing.T) {
	auth := &cliproxyauth.Auth{
		ID:         "codex-auth",
		Provider:   "codex",
		Attributes: map[string]string{"deno_proxy_host": "relay.example.com"},
	}

	gotResponses := applyCodexDenoProxy(auth, codexDefaultBaseURL+"/responses")
	if gotResponses != "https://relay.example.com/codex/responses" {
		t.Fatalf("responses URL = %q", gotResponses)
	}

	gotCompact := applyCodexDenoProxy(auth, codexDefaultBaseURL+"/responses/compact")
	if gotCompact != "https://relay.example.com/codex/responses/compact" {
		t.Fatalf("compact URL = %q", gotCompact)
	}
}

func TestApplyCodexDenoProxy_LeavesCustomBaseURLUntouched(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"deno_proxy_host": "https://relay.example.com"},
	}

	customURL := "https://custom.example.com/backend-api/codex/responses"
	if got := applyCodexDenoProxy(auth, customURL); got != customURL {
		t.Fatalf("custom URL should not be rewritten: %q", got)
	}
}

func TestCodexDenoProxyHost_FallsBackToMetadata(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"deno_proxy_host": "relay.example.com"},
	}

	if got := codexDenoProxyHost(auth); got != "https://relay.example.com" {
		t.Fatalf("codexDenoProxyHost = %q", got)
	}
}
