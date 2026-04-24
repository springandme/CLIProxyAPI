package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeDenoProxyHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "https origin", input: "https://Relay.Example.com/", want: "https://relay.example.com"},
		{name: "http origin", input: " http://relay.example.com:8080/ ", want: "http://relay.example.com:8080"},
		{name: "reject path", input: "https://relay.example.com/codex", wantErr: "path"},
		{name: "reject query", input: "https://relay.example.com/?a=1", wantErr: "query"},
		{name: "reject fragment", input: "https://relay.example.com/#frag", wantErr: "fragment"},
		{name: "reject relative", input: "relay.example.com", wantErr: "absolute"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeDenoProxyHost(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("NormalizeDenoProxyHost() error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeDenoProxyHost() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeDenoProxyHost() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeDenoProxyHosts_Deduplicates(t *testing.T) {
	t.Parallel()

	got, err := NormalizeDenoProxyHosts([]string{
		"https://relay.example.com/",
		" https://relay.example.com ",
		"http://relay.example.com:8080/",
	})
	if err != nil {
		t.Fatalf("NormalizeDenoProxyHosts() error = %v", err)
	}

	want := []string{
		"https://relay.example.com",
		"http://relay.example.com:8080",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeDenoProxyHosts() = %#v, want %#v", got, want)
	}
}

func TestNormalizeDenoProxyHostForMatch_LegacyHost(t *testing.T) {
	t.Parallel()

	if got := NormalizeDenoProxyHostForMatch("relay.example.com/"); got != "https://relay.example.com" {
		t.Fatalf("NormalizeDenoProxyHostForMatch() = %q, want %q", got, "https://relay.example.com")
	}
}

func TestConfigDenoProxies_JSONSerialization(t *testing.T) {
	t.Parallel()

	cfg := Config{DenoProxies: []string{"https://relay.example.com"}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"deno-proxies":["https://relay.example.com"]`) {
		t.Fatalf("json output = %s", data)
	}
}

func TestLoadConfigOptional_NormalizesDenoProxies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := "deno-proxies:\n  - https://Relay.Example.com/\n  - https://relay.example.com\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.DenoProxies, []string{"https://relay.example.com"}) {
		t.Fatalf("cfg.DenoProxies = %#v", cfg.DenoProxies)
	}
}

func TestLoadConfigOptional_InvalidDenoProxies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := "deno-proxies:\n  - https://relay.example.com/codex\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := LoadConfigOptional(configPath, false)
	if err == nil || !strings.Contains(err.Error(), "invalid deno-proxies") {
		t.Fatalf("LoadConfigOptional() error = %v, want invalid deno-proxies", err)
	}
}
