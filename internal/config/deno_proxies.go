package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NormalizeDenoProxyHost validates and canonicalizes a managed Deno relay host.
func NormalizeDenoProxyHost(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("host is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed == nil || !parsed.IsAbs() {
		return "", fmt.Errorf("host must be an absolute http or https url")
	}

	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("host must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("host is required")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("query parameters are not allowed")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("fragments are not allowed")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("path is not allowed")
	}

	hostName := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if hostName == "" {
		return "", fmt.Errorf("host is required")
	}

	normalizedHost := hostName
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		normalizedHost = net.JoinHostPort(hostName, port)
	}

	return scheme + "://" + normalizedHost, nil
}

// NormalizeDenoProxyHosts normalizes and deduplicates managed Deno relay hosts.
func NormalizeDenoProxyHosts(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		normalized, err := NormalizeDenoProxyHost(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", trimmed, err)
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// NormalizeDenoProxyHostForMatch normalizes host values found in existing config or runtime auths.
// It tolerates legacy entries without a scheme by assuming https.
func NormalizeDenoProxyHostForMatch(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if normalized, err := NormalizeDenoProxyHost(trimmed); err == nil {
		return normalized
	}
	if strings.Contains(trimmed, "://") {
		return ""
	}
	normalized, err := NormalizeDenoProxyHost("https://" + trimmed)
	if err != nil {
		return ""
	}
	return normalized
}
