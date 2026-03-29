package config

import "testing"

func TestSanitizeCodexKeys_TrimsDenoProxyHost(t *testing.T) {
	cfg := &Config{
		CodexKey: []CodexKey{
			{
				APIKey:        " sk-test ",
				BaseURL:       " https://chatgpt.com/backend-api/codex ",
				DenoProxyHost: " https://relay.example.com/ ",
			},
		},
	}

	cfg.SanitizeCodexKeys()

	if len(cfg.CodexKey) != 1 {
		t.Fatalf("expected 1 codex key after sanitize, got %d", len(cfg.CodexKey))
	}
	if got := cfg.CodexKey[0].DenoProxyHost; got != "https://relay.example.com/" {
		t.Fatalf("DenoProxyHost = %q, want %q", got, "https://relay.example.com/")
	}
	if got := cfg.CodexKey[0].BaseURL; got != "https://chatgpt.com/backend-api/codex" {
		t.Fatalf("BaseURL = %q, want %q", got, "https://chatgpt.com/backend-api/codex")
	}
}
