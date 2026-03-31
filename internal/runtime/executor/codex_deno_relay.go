package executor

import (
	"net/http"
	"net/url"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexDefaultBaseURL  = "https://chatgpt.com/backend-api/codex"
	codexDenoProxyPrefix = "/codex"
)

func codexDenoProxyHost(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}

	read := func(values map[string]string) string {
		if len(values) == 0 {
			return ""
		}
		return strings.TrimSpace(values["deno_proxy_host"])
	}

	if denoHost := read(auth.Attributes); denoHost != "" {
		return normalizeCodexDenoProxyHost(denoHost)
	}
	if auth.Metadata != nil {
		if raw, ok := auth.Metadata["deno_proxy_host"].(string); ok {
			return normalizeCodexDenoProxyHost(raw)
		}
		if raw, ok := auth.Metadata["deno-proxy-host"].(string); ok {
			return normalizeCodexDenoProxyHost(raw)
		}
	}
	return ""
}

func normalizeCodexDenoProxyHost(raw string) string {
	denoHost := strings.TrimSpace(raw)
	if denoHost == "" {
		return ""
	}
	if !strings.HasPrefix(denoHost, "http://") && !strings.HasPrefix(denoHost, "https://") {
		denoHost = "https://" + denoHost
	}
	return strings.TrimSuffix(denoHost, "/")
}

func codexUsesDenoProxy(auth *cliproxyauth.Auth) bool {
	return codexDenoProxyHost(auth) != ""
}

func applyCodexDenoProxy(auth *cliproxyauth.Auth, targetURL string) string {
	denoHost := codexDenoProxyHost(auth)
	if denoHost == "" {
		return targetURL
	}

	normalizedTarget := strings.TrimSpace(targetURL)
	if normalizedTarget == "" {
		return targetURL
	}

	proxyURL, rewritten := rewriteCodexDenoProxyURL(denoHost, normalizedTarget)
	if !rewritten {
		return targetURL
	}

	authID := ""
	if auth != nil {
		authID = strings.TrimSpace(auth.ID)
	}
	log.Infof("codex deno proxy: forwarding request [auth=%s] from %s -> %s", authID, normalizedTarget, proxyURL)
	return proxyURL
}

func rewriteCodexDenoProxyURL(denoHost string, targetURL string) (string, bool) {
	parsedTarget, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsedTarget == nil {
		return "", false
	}
	parsedOfficial, err := url.Parse(codexDefaultBaseURL)
	if err != nil || parsedOfficial == nil {
		return "", false
	}
	parsedRelay, err := url.Parse(strings.TrimSpace(denoHost))
	if err != nil || parsedRelay == nil {
		return "", false
	}

	targetScheme := strings.ToLower(parsedTarget.Scheme)
	websocket := false
	switch targetScheme {
	case "http", "https":
	case "ws":
		websocket = true
	case "wss":
		websocket = true
	default:
		return "", false
	}

	if !strings.EqualFold(parsedTarget.Host, parsedOfficial.Host) {
		return "", false
	}

	officialPath := strings.TrimSuffix(parsedOfficial.Path, "/")
	targetPath := parsedTarget.Path
	if targetPath == "" {
		targetPath = "/"
	}
	if targetPath != officialPath && !strings.HasPrefix(targetPath, officialPath+"/") {
		return "", false
	}

	suffix := strings.TrimPrefix(targetPath, officialPath)
	if suffix == "" {
		suffix = "/"
	}

	relayPath := strings.TrimSuffix(parsedRelay.Path, "/")
	parsedRelay.Path = relayPath + codexDenoProxyPrefix + suffix
	parsedRelay.RawPath = ""
	parsedRelay.RawQuery = parsedTarget.RawQuery
	parsedRelay.Fragment = parsedTarget.Fragment

	if websocket {
		switch strings.ToLower(parsedRelay.Scheme) {
		case "http":
			parsedRelay.Scheme = "ws"
		case "https":
			parsedRelay.Scheme = "wss"
		default:
			return "", false
		}
	}

	return parsedRelay.String(), true
}

func rewriteCodexHTTPRequestForDeno(auth *cliproxyauth.Auth, req *http.Request) error {
	if req == nil || req.URL == nil {
		return nil
	}

	rewritten := applyCodexDenoProxy(auth, req.URL.String())
	if rewritten == req.URL.String() {
		return nil
	}

	parsed, err := http.NewRequestWithContext(req.Context(), req.Method, rewritten, nil)
	if err != nil {
		return err
	}
	req.URL = parsed.URL
	req.Host = ""
	return nil
}
