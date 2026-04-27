package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

const (
	denoProxyProbeHTTPPath = "/codex/responses"
	denoProxyProbeTimeout  = 5 * time.Second
)

type denoProxyUsageRef struct {
	Source      string `json:"source"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Label       string `json:"label,omitempty"`
	Provider    string `json:"provider,omitempty"`
	AuthIndex   string `json:"auth_index,omitempty"`
	FileName    string `json:"file_name,omitempty"`
	Prefix      string `json:"prefix,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
	RuntimeOnly bool   `json:"runtime_only,omitempty"`
}

type denoProxyUsageItem struct {
	Host       string              `json:"host"`
	UsageCount int                 `json:"usage_count"`
	UsedBy     []denoProxyUsageRef `json:"used_by"`
	Unused     bool                `json:"unused"`
}

type denoProxyListResponse struct {
	Items           []denoProxyUsageItem `json:"items"`
	UnmanagedInUse  []denoProxyUsageItem `json:"unmanaged_in_use"`
	ManagedHosts    []string             `json:"managed_hosts,omitempty"`
	UnmanagedHosts  []string             `json:"unmanaged_hosts,omitempty"`
	TotalUsageCount int                  `json:"total_usage_count,omitempty"`
}

type denoProxyProbeCheck struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Error      string `json:"error,omitempty"`
}

type denoProxyProbeResponse struct {
	Host           string              `json:"host"`
	Root           denoProxyProbeCheck `json:"root"`
	Robots         denoProxyProbeCheck `json:"robots"`
	CodexHTTP      denoProxyProbeCheck `json:"codexHttp"`
	CodexWebsocket denoProxyProbeCheck `json:"codexWebsocket"`
	CheckedAt      time.Time           `json:"checkedAt"`
	LatencyMs      int64               `json:"latencyMs"`
	Summary        string              `json:"summary"`
}

type denoProxyUsageAggregate struct {
	host   string
	usedBy []denoProxyUsageRef
	seen   map[string]struct{}
}

func newDenoProxyUsageAggregate(host string) *denoProxyUsageAggregate {
	return &denoProxyUsageAggregate{
		host: host,
		seen: make(map[string]struct{}),
	}
}

func (a *denoProxyUsageAggregate) add(ref denoProxyUsageRef) {
	if a == nil {
		return
	}
	key := denoProxyUsageRefKey(ref)
	if _, exists := a.seen[key]; exists {
		return
	}
	a.seen[key] = struct{}{}
	a.usedBy = append(a.usedBy, ref)
}

func (a *denoProxyUsageAggregate) item() denoProxyUsageItem {
	if a == nil {
		return denoProxyUsageItem{}
	}
	sort.Slice(a.usedBy, func(i, j int) bool {
		left := a.usedBy[i]
		right := a.usedBy[j]
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		if left.Name != right.Name {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return strings.ToLower(left.ID) < strings.ToLower(right.ID)
	})
	return denoProxyUsageItem{
		Host:       a.host,
		UsageCount: len(a.usedBy),
		UsedBy:     append([]denoProxyUsageRef{}, a.usedBy...),
		Unused:     len(a.usedBy) == 0,
	}
}

func denoProxyUsageRefKey(ref denoProxyUsageRef) string {
	return strings.Join([]string{
		ref.Source,
		ref.ID,
		ref.Name,
		ref.Label,
		ref.FileName,
		ref.AuthIndex,
		ref.BaseURL,
		ref.Prefix,
	}, "|")
}

func (h *Handler) GetDenoProxies(c *gin.Context) {
	c.JSON(http.StatusOK, h.buildDenoProxyListResponse())
}

func (h *Handler) PutDenoProxies(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	items, err := parseDenoProxyHostListBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}
	h.cfg.DenoProxies = append([]string(nil), items...)
	h.persistLocked(c)
}

func (h *Handler) PatchDenoProxies(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	var req struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	addHosts, err := config.NormalizeDenoProxyHosts(req.Add)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	removeHosts, err := config.NormalizeDenoProxyHosts(req.Remove)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(addHosts) == 0 && len(removeHosts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "add or remove is required"})
		return
	}

	removeSet := make(map[string]struct{}, len(removeHosts))
	for _, host := range removeHosts {
		removeSet[host] = struct{}{}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}

	next := make([]string, 0, len(h.cfg.DenoProxies)+len(addHosts))
	seen := make(map[string]struct{}, len(h.cfg.DenoProxies)+len(addHosts))
	for _, host := range h.cfg.DenoProxies {
		if _, shouldRemove := removeSet[host]; shouldRemove {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		next = append(next, host)
	}
	for _, host := range addHosts {
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		next = append(next, host)
	}

	h.cfg.DenoProxies = next
	h.persistLocked(c)
}

func (h *Handler) DeleteDenoProxies(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	host, err := config.NormalizeDenoProxyHost(c.Query("host"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config not initialized"})
		return
	}

	next := make([]string, 0, len(h.cfg.DenoProxies))
	for _, item := range h.cfg.DenoProxies {
		if item == host {
			continue
		}
		next = append(next, item)
	}
	h.cfg.DenoProxies = next
	h.persistLocked(c)
}

func (h *Handler) ProbeDenoProxy(c *gin.Context) {
	var req struct {
		Host string `json:"host"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	host, err := config.NormalizeDenoProxyHost(req.Host)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result := probeDenoProxyHost(c.Request.Context(), host)
	c.JSON(http.StatusOK, result)
}

func parseDenoProxyHostListBody(c *gin.Context) ([]string, error) {
	data, err := c.GetRawData()
	if err != nil {
		return nil, fmt.Errorf("failed to read body")
	}

	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		return config.NormalizeDenoProxyHosts(arr)
	}

	var body struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("invalid body")
	}
	return config.NormalizeDenoProxyHosts(body.Items)
}

func (h *Handler) buildDenoProxyListResponse() denoProxyListResponse {
	managedHosts, authDir, auths, codexKeys := h.snapshotDenoProxySources()

	usageByHost := make(map[string]*denoProxyUsageAggregate, len(managedHosts))
	managedSet := make(map[string]struct{}, len(managedHosts))
	for _, host := range managedHosts {
		managedSet[host] = struct{}{}
		usageByHost[host] = newDenoProxyUsageAggregate(host)
	}

	for _, refWithHost := range collectDenoProxyUsageRefsFromCodexKeys(codexKeys) {
		aggregate := usageByHost[refWithHost.host]
		if aggregate == nil {
			aggregate = newDenoProxyUsageAggregate(refWithHost.host)
			usageByHost[refWithHost.host] = aggregate
		}
		aggregate.add(refWithHost.ref)
	}

	runtimeLookup := buildRuntimeAuthLookup(auths)
	authFileRefs := collectDenoProxyUsageRefsFromAuthFiles(authDir, runtimeLookup.fileAuthIndex)
	for _, refWithHost := range authFileRefs {
		aggregate := usageByHost[refWithHost.host]
		if aggregate == nil {
			aggregate = newDenoProxyUsageAggregate(refWithHost.host)
			usageByHost[refWithHost.host] = aggregate
		}
		aggregate.add(refWithHost.ref)
	}

	fileBackedUsageKeys := buildDenoProxyFileBackedUsageKeySet(authFileRefs)
	for _, refWithHost := range collectDenoProxyUsageRefsFromRuntimeAuths(auths) {
		if shouldSkipRuntimeAuthUsageRef(refWithHost, fileBackedUsageKeys) {
			continue
		}
		aggregate := usageByHost[refWithHost.host]
		if aggregate == nil {
			aggregate = newDenoProxyUsageAggregate(refWithHost.host)
			usageByHost[refWithHost.host] = aggregate
		}
		aggregate.add(refWithHost.ref)
	}

	items := make([]denoProxyUsageItem, 0, len(managedHosts))
	for _, host := range managedHosts {
		aggregate := usageByHost[host]
		if aggregate == nil {
			aggregate = newDenoProxyUsageAggregate(host)
		}
		items = append(items, aggregate.item())
	}

	unmanaged := make([]denoProxyUsageItem, 0)
	unmanagedHosts := make([]string, 0)
	totalUsageCount := 0
	for _, item := range items {
		totalUsageCount += item.UsageCount
	}
	for host, aggregate := range usageByHost {
		if aggregate == nil || len(aggregate.usedBy) == 0 {
			continue
		}
		if _, managed := managedSet[host]; managed {
			continue
		}
		item := aggregate.item()
		totalUsageCount += item.UsageCount
		unmanaged = append(unmanaged, item)
		unmanagedHosts = append(unmanagedHosts, host)
	}

	sort.Slice(unmanaged, func(i, j int) bool {
		return unmanaged[i].Host < unmanaged[j].Host
	})
	sort.Strings(unmanagedHosts)

	return denoProxyListResponse{
		Items:           items,
		UnmanagedInUse:  unmanaged,
		ManagedHosts:    append([]string(nil), managedHosts...),
		UnmanagedHosts:  unmanagedHosts,
		TotalUsageCount: totalUsageCount,
	}
}

func buildDenoProxyFileBackedUsageKeySet(
	refs []denoProxyUsageRefWithHost,
) map[string]struct{} {
	keys := make(map[string]struct{}, len(refs)*2)
	for _, refWithHost := range refs {
		addDenoProxyUsageLookupKeys(keys, refWithHost.host, refWithHost.ref)
	}
	return keys
}

func shouldSkipRuntimeAuthUsageRef(
	refWithHost denoProxyUsageRefWithHost,
	fileBackedKeys map[string]struct{},
) bool {
	if refWithHost.ref.RuntimeOnly {
		return false
	}
	if len(fileBackedKeys) == 0 {
		return false
	}
	for _, key := range denoProxyUsageLookupKeys(refWithHost.host, refWithHost.ref) {
		if _, exists := fileBackedKeys[key]; exists {
			return true
		}
	}
	return false
}

func addDenoProxyUsageLookupKeys(keys map[string]struct{}, host string, ref denoProxyUsageRef) {
	for _, key := range denoProxyUsageLookupKeys(host, ref) {
		keys[key] = struct{}{}
	}
}

func denoProxyUsageLookupKeys(host string, ref denoProxyUsageRef) []string {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}

	keys := make([]string, 0, 2)
	if authIndex := strings.TrimSpace(ref.AuthIndex); authIndex != "" {
		keys = append(keys, host+"|auth-index|"+authIndex)
	}
	if fileName := strings.TrimSpace(ref.FileName); fileName != "" {
		keys = append(keys, host+"|file-name|"+filepath.Base(fileName))
	}
	return keys
}

func (h *Handler) snapshotDenoProxySources() ([]string, string, []*coreauth.Auth, []config.CodexKey) {
	if h == nil {
		return nil, "", nil, nil
	}

	h.mu.Lock()
	var (
		managedHosts []string
		authDir      string
		codexKeys    []config.CodexKey
		manager      *coreauth.Manager
	)
	if h.cfg != nil {
		managedHosts = append([]string(nil), h.cfg.DenoProxies...)
		authDir = strings.TrimSpace(h.cfg.AuthDir)
		codexKeys = append([]config.CodexKey(nil), h.cfg.CodexKey...)
	}
	manager = h.authManager
	h.mu.Unlock()

	var auths []*coreauth.Auth
	if manager != nil {
		auths = manager.List()
	}
	sort.Strings(managedHosts)
	return managedHosts, authDir, auths, codexKeys
}

type denoProxyUsageRefWithHost struct {
	host string
	ref  denoProxyUsageRef
}

func collectDenoProxyUsageRefsFromCodexKeys(keys []config.CodexKey) []denoProxyUsageRefWithHost {
	refs := make([]denoProxyUsageRefWithHost, 0, len(keys))
	for index, key := range keys {
		host := config.NormalizeDenoProxyHostForMatch(key.DenoProxyHost)
		if host == "" {
			continue
		}
		name := strings.TrimSpace(key.Prefix)
		if name == "" {
			name = fmt.Sprintf("Codex key %d", index+1)
		}
		id := fmt.Sprintf("codex-api-key:%d", index)
		refs = append(refs, denoProxyUsageRefWithHost{
			host: host,
			ref: denoProxyUsageRef{
				Source:   "codex-api-key",
				ID:       id,
				Name:     name,
				Label:    name,
				Provider: "codex",
				Prefix:   strings.TrimSpace(key.Prefix),
				BaseURL:  strings.TrimSpace(key.BaseURL),
			},
		})
	}
	return refs
}

type runtimeAuthLookup struct {
	fileAuthIndex map[string]string
}

func buildRuntimeAuthLookup(auths []*coreauth.Auth) runtimeAuthLookup {
	out := runtimeAuthLookup{fileAuthIndex: make(map[string]string)}
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		index := strings.TrimSpace(auth.Index)
		if index == "" {
			index = auth.EnsureIndex()
		}
		if index == "" {
			continue
		}
		fileName := strings.TrimSpace(auth.FileName)
		if fileName == "" {
			continue
		}
		out.fileAuthIndex[fileName] = index
		out.fileAuthIndex[filepath.Base(fileName)] = index
	}
	return out
}

func collectDenoProxyUsageRefsFromAuthFiles(authDir string, fileAuthIndex map[string]string) []denoProxyUsageRefWithHost {
	authDir = strings.TrimSpace(authDir)
	if authDir == "" {
		return nil
	}
	entries, err := os.ReadDir(authDir)
	if err != nil {
		return nil
	}

	refs := make([]denoProxyUsageRefWithHost, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		path := filepath.Join(authDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		provider := strings.TrimSpace(gjson.GetBytes(data, "type").String())
		if !strings.EqualFold(provider, "codex") {
			continue
		}
		host := config.NormalizeDenoProxyHostForMatch(readAuthFileDenoProxyHost(data))
		if host == "" {
			continue
		}
		label := strings.TrimSpace(gjson.GetBytes(data, "label").String())
		email := strings.TrimSpace(gjson.GetBytes(data, "email").String())
		displayName := name
		if email != "" {
			displayName = email
		}
		if label != "" {
			displayName = label
		}
		refs = append(refs, denoProxyUsageRefWithHost{
			host: host,
			ref: denoProxyUsageRef{
				Source:    "auth-file",
				ID:        "auth-file:" + name,
				Name:      displayName,
				Label:     label,
				Provider:  "codex",
				AuthIndex: fileAuthIndex[name],
				FileName:  name,
			},
		})
	}
	return refs
}

func readAuthFileDenoProxyHost(data []byte) string {
	if raw := strings.TrimSpace(gjson.GetBytes(data, "deno_proxy_host").String()); raw != "" {
		return raw
	}
	return strings.TrimSpace(gjson.GetBytes(data, "deno-proxy-host").String())
}

func collectDenoProxyUsageRefsFromRuntimeAuths(auths []*coreauth.Auth) []denoProxyUsageRefWithHost {
	refs := make([]denoProxyUsageRefWithHost, 0, len(auths))
	for _, auth := range auths {
		if shouldSkipRuntimeAuthDenoProxyUsage(auth) {
			continue
		}
		host := runtimeAuthDenoProxyHost(auth)
		if host == "" {
			continue
		}

		name := strings.TrimSpace(auth.Label)
		if name == "" {
			name = strings.TrimSpace(auth.FileName)
		}
		if name == "" {
			name = strings.TrimSpace(auth.ID)
		}

		index := strings.TrimSpace(auth.Index)
		if index == "" {
			index = auth.EnsureIndex()
		}

		refs = append(refs, denoProxyUsageRefWithHost{
			host: host,
			ref: denoProxyUsageRef{
				Source:      "runtime-auth",
				ID:          strings.TrimSpace(auth.ID),
				Name:        name,
				Label:       strings.TrimSpace(auth.Label),
				Provider:    strings.TrimSpace(auth.Provider),
				AuthIndex:   index,
				FileName:    strings.TrimSpace(auth.FileName),
				Prefix:      strings.TrimSpace(auth.Prefix),
				RuntimeOnly: isRuntimeOnlyAuth(auth),
			},
		})
	}
	return refs
}

func shouldSkipRuntimeAuthDenoProxyUsage(auth *coreauth.Auth) bool {
	if auth == nil {
		return true
	}
	if isRuntimeOnlyAuth(auth) {
		return false
	}

	path := strings.TrimSpace(authAttribute(auth, "path"))
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return false
	} else if !os.IsNotExist(err) {
		return false
	}

	return auth.Disabled ||
		auth.Status == coreauth.StatusDisabled ||
		strings.EqualFold(strings.TrimSpace(auth.StatusMessage), "removed via management api")
}

func runtimeAuthDenoProxyHost(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if raw := strings.TrimSpace(authAttribute(auth, "deno_proxy_host")); raw != "" {
		return config.NormalizeDenoProxyHostForMatch(raw)
	}
	if auth.Metadata == nil {
		return ""
	}
	if raw, ok := auth.Metadata["deno_proxy_host"]; ok {
		return config.NormalizeDenoProxyHostForMatch(fmt.Sprint(raw))
	}
	if raw, ok := auth.Metadata["deno-proxy-host"]; ok {
		return config.NormalizeDenoProxyHostForMatch(fmt.Sprint(raw))
	}
	return ""
}

func probeDenoProxyHost(ctx context.Context, host string) denoProxyProbeResponse {
	startedAt := time.Now().UTC()
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, denoProxyProbeTimeout)
	defer cancel()

	httpClient := &http.Client{Timeout: denoProxyProbeTimeout}
	root := probeDenoProxyHTTP(ctx, httpClient, host, "/", func(status int) bool {
		return status >= 200 && status < 300
	})
	robots := probeDenoProxyHTTP(ctx, httpClient, host, "/robots.txt", func(status int) bool {
		return status >= 200 && status < 300
	})
	codexHTTP := probeDenoProxyHTTP(ctx, httpClient, host, denoProxyProbeHTTPPath, func(status int) bool {
		return status != http.StatusNotFound
	})
	codexWebsocket := probeDenoProxyWebsocket(ctx, host)

	return denoProxyProbeResponse{
		Host:           host,
		Root:           root,
		Robots:         robots,
		CodexHTTP:      codexHTTP,
		CodexWebsocket: codexWebsocket,
		CheckedAt:      startedAt,
		LatencyMs:      time.Since(start).Milliseconds(),
		Summary:        summarizeDenoProxyProbe(root, robots, codexHTTP, codexWebsocket),
	}
}

func probeDenoProxyHTTP(
	ctx context.Context,
	client *http.Client,
	host string,
	path string,
	isOK func(status int) bool,
) denoProxyProbeCheck {
	target, err := joinDenoProxyURL(host, path)
	if err != nil {
		return denoProxyProbeCheck{Error: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return denoProxyProbeCheck{Error: err.Error()}
	}
	req.Header.Set("User-Agent", "CLIProxyAPI-deno-probe")

	resp, err := client.Do(req)
	if err != nil {
		return denoProxyProbeCheck{Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	return denoProxyProbeCheck{
		OK:         isOK(resp.StatusCode),
		StatusCode: resp.StatusCode,
		Detail:     resp.Status,
	}
}

func probeDenoProxyWebsocket(ctx context.Context, host string) denoProxyProbeCheck {
	target, err := joinDenoProxyWebsocketURL(host, denoProxyProbeHTTPPath)
	if err != nil {
		return denoProxyProbeCheck{Error: err.Error()}
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: denoProxyProbeTimeout,
	}
	headers := http.Header{}
	headers.Set("User-Agent", "CLIProxyAPI-deno-probe")

	conn, resp, err := dialer.DialContext(ctx, target, headers)
	if conn != nil {
		_ = conn.Close()
		return denoProxyProbeCheck{
			OK:         true,
			StatusCode: http.StatusSwitchingProtocols,
			Detail:     "websocket upgrade succeeded",
		}
	}
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		detail := resp.Status
		if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
			detail = detail + ": " + trimmed
		}
		return denoProxyProbeCheck{
			OK:         resp.StatusCode != http.StatusNotFound,
			StatusCode: resp.StatusCode,
			Detail:     detail,
			Error:      errorString(err),
		}
	}
	return denoProxyProbeCheck{Error: errorString(err)}
}

func joinDenoProxyURL(host string, path string) (string, error) {
	parsed, err := url.Parse(host)
	if err != nil {
		return "", err
	}
	if parsed == nil {
		return "", fmt.Errorf("invalid host")
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func joinDenoProxyWebsocketURL(host string, path string) (string, error) {
	target, err := joinDenoProxyURL(host, path)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme")
	}
	return parsed.String(), nil
}

func summarizeDenoProxyProbe(
	root denoProxyProbeCheck,
	robots denoProxyProbeCheck,
	codexHTTP denoProxyProbeCheck,
	codexWebsocket denoProxyProbeCheck,
) string {
	parts := []string{
		summarizeDenoProxyProbeCheck("GET /", root),
		summarizeDenoProxyProbeCheck("GET /robots.txt", robots),
		summarizeDenoProxyProbeCheck("GET /codex/responses", codexHTTP),
		summarizeDenoProxyProbeCheck("WS /codex/responses", codexWebsocket),
	}
	return strings.Join(parts, "; ")
}

func summarizeDenoProxyProbeCheck(label string, check denoProxyProbeCheck) string {
	if check.OK {
		if check.StatusCode > 0 {
			return fmt.Sprintf("%s ok (%d)", label, check.StatusCode)
		}
		return label + " ok"
	}
	if check.StatusCode > 0 {
		return fmt.Sprintf("%s failed (%d)", label, check.StatusCode)
	}
	if check.Error != "" {
		return fmt.Sprintf("%s failed (%s)", label, check.Error)
	}
	return label + " failed"
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
