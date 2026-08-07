package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	OpenCodeModelSnapshotSettingKey = "opencode_zen_free_models_v1"
	openCodePricingURL              = "https://opencode.ai/docs/zen"
	openCodeSubscriptionMaxBytes    = int64(8 << 20)
	openCodeProbeTimeout            = 20 * time.Second
	openCodeNodeProbeFreshness      = 15 * time.Minute
	openCodeDefaultSyncInterval     = 360
	openCodeListenerPortStart       = 22000
	openCodeListenerPortEnd         = 29999
)

var (
	ErrProxySubscriptionNotFound = infraerrors.NotFound("PROXY_SUBSCRIPTION_NOT_FOUND", "proxy subscription not found")
	ErrProxySubscriptionInUse    = infraerrors.Conflict("PROXY_SUBSCRIPTION_IN_USE", "proxy subscription has nodes bound to accounts")
	ErrOpenCodeDuplicateExitIP   = infraerrors.Conflict("OPENCODE_DUPLICATE_EXIT_IP", "OpenCode exit IP is already leased")
	ErrOpenCodeProxyRequired     = infraerrors.BadRequest("OPENCODE_PROXY_REQUIRED", "OpenCode proxy egress requires a managed proxy")
	ErrOpenCodeProxyUnhealthy    = infraerrors.Conflict("OPENCODE_PROXY_UNHEALTHY", "OpenCode managed proxy is not healthy")
	openCodeGeoBaseURL           = "http://ip-api.com/json/"
)

type ProxySubscription struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Enabled             bool       `json:"enabled"`
	SyncIntervalMinutes int        `json:"sync_interval_minutes"`
	LastFetchedAt       *time.Time `json:"last_fetched_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastError           string     `json:"last_error,omitempty"`
	NodeCount           int        `json:"node_count"`
	LastFormat          string     `json:"last_format,omitempty"`
	LastUserAgent       string     `json:"last_user_agent,omitempty"`
	HasURL              bool       `json:"has_url"`
	URLMasked           string     `json:"url_masked"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	URLCiphertext       string     `json:"-"`
}

type ManagedProxyNode struct {
	ID                 int64      `json:"id"`
	SubscriptionID     int64      `json:"subscription_id"`
	ProxyID            *int64     `json:"proxy_id"`
	NodeKey            string     `json:"node_key"`
	DisplayName        string     `json:"display_name"`
	MihomoName         string     `json:"mihomo_name"`
	Protocol           string     `json:"protocol"`
	ListenerPort       int        `json:"listener_port"`
	SyncStatus         string     `json:"sync_status"`
	HealthStatus       string     `json:"health_status"`
	LastSeenAt         *time.Time `json:"last_seen_at"`
	ExitIP             string     `json:"exit_ip,omitempty"`
	Country            string     `json:"country,omitempty"`
	Region             string     `json:"region,omitempty"`
	LatencyMs          *int64     `json:"latency_ms"`
	OpenCodeHTTPStatus *int       `json:"opencode_http_status"`
	FailureType        string     `json:"failure_type,omitempty"`
	FailureMessage     string     `json:"failure_message,omitempty"`
	LastProbeAt        *time.Time `json:"last_probe_at"`
	DuplicateOfNodeID  *int64     `json:"duplicate_of_node_id"`
	ListenerUsername   string     `json:"-"`
	ListenerPassword   string     `json:"-"`
	ConfigCiphertext   string     `json:"-"`
}

type ManagedProxyNodeDraft struct {
	ManagedProxyNode
	ProxyConfig map[string]any
}

type OpenCodeNodeProbeResult struct {
	NodeID             int64  `json:"node_id"`
	Success            bool   `json:"success"`
	HealthStatus       string `json:"health_status"`
	ExitIP             string `json:"exit_ip,omitempty"`
	Country            string `json:"country,omitempty"`
	Region             string `json:"region,omitempty"`
	LatencyMs          int64  `json:"latency_ms,omitempty"`
	OpenCodeHTTPStatus int    `json:"opencode_http_status,omitempty"`
	FailureType        string `json:"failure_type,omitempty"`
	FailureMessage     string `json:"failure_message,omitempty"`
	DuplicateOfNodeID  *int64 `json:"duplicate_of_node_id,omitempty"`
}

type OpenCodeModelRegistryStatus struct {
	IDs           []string   `json:"ids"`
	Count         int        `json:"count"`
	LastFetchedAt *time.Time `json:"last_fetched_at"`
	LastError     string     `json:"last_error,omitempty"`
	UsingBaseline bool       `json:"using_baseline"`
}

type OpenCodeProxyPoolRepository interface {
	ListSubscriptions(context.Context) ([]ProxySubscription, error)
	GetSubscription(context.Context, int64) (*ProxySubscription, error)
	CreateSubscription(context.Context, ProxySubscription) (*ProxySubscription, error)
	UpdateSubscription(context.Context, ProxySubscription) (*ProxySubscription, error)
	DeleteSubscription(context.Context, int64) error
	ListNodes(context.Context, *int64) ([]ManagedProxyNode, error)
	UsedListenerPorts(context.Context) (map[int]struct{}, error)
	CommitSubscriptionSync(context.Context, int64, []ManagedProxyNodeDraft, string, string) error
	UpdateSubscriptionError(context.Context, int64, string) error
	UpdateNodeProbeResults(context.Context, []OpenCodeNodeProbeResult) error
	ReconcileEgressLeases(context.Context) ([]int64, error)
	IsManagedProxy(context.Context, int64) (bool, error)
	AcquireEgressLease(context.Context, int64, *int64, string, string, time.Time) error
	ReleaseEgressLease(context.Context, int64) error
}

type OpenCodeProxyPoolService struct {
	repo        OpenCodeProxyPoolRepository
	encryptor   SecretEncryptor
	settingRepo SettingRepository
	accountRepo AccountRepository
	httpClient  *http.Client
	controller  string
	secret      string
	configDir   string
	proxyHost   string
	syncMu      sync.Mutex
	modelMu     sync.RWMutex
	modelStatus OpenCodeModelRegistryStatus
	stop        chan struct{}
	stopOnce    sync.Once
}

func NewOpenCodeProxyPoolService(repo OpenCodeProxyPoolRepository, encryptor SecretEncryptor, settingRepo SettingRepository, accountRepo AccountRepository) *OpenCodeProxyPoolService {
	controller := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENCODE_MIHOMO_CONTROLLER_URL")), "/")
	if controller == "" {
		controller = "http://opencode-mihomo:9090"
	}
	configDir := strings.TrimSpace(os.Getenv("OPENCODE_MIHOMO_CONFIG_DIR"))
	if configDir == "" {
		configDir = "/app/data/opencode-mihomo"
	}
	proxyHost := strings.TrimSpace(os.Getenv("OPENCODE_MIHOMO_PROXY_HOST"))
	if proxyHost == "" {
		proxyHost = "opencode-mihomo"
	}
	return &OpenCodeProxyPoolService{
		repo: repo, encryptor: encryptor, settingRepo: settingRepo, accountRepo: accountRepo,
		httpClient: &http.Client{Timeout: openCodeProbeTimeout},
		controller: controller, secret: strings.TrimSpace(os.Getenv("OPENCODE_MIHOMO_SECRET")),
		configDir: configDir, proxyHost: proxyHost, stop: make(chan struct{}),
		modelStatus: OpenCodeModelRegistryStatus{IDs: defaultOpenCodeFreeModels.IDs(), Count: len(defaultOpenCodeFreeModels.IDs()), UsingBaseline: true},
	}
}

func (s *OpenCodeProxyPoolService) Start() {
	if s == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = s.LoadModelSnapshot(ctx)
		_, _ = s.RefreshModels(ctx)
		cancel()
		modelTicker := time.NewTicker(6 * time.Hour)
		probeTicker := time.NewTicker(15 * time.Minute)
		syncTicker := time.NewTicker(time.Minute)
		defer modelTicker.Stop()
		defer probeTicker.Stop()
		defer syncTicker.Stop()
		for {
			select {
			case <-modelTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				_, _ = s.RefreshModels(ctx)
				cancel()
			case <-probeTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				_, _ = s.ProbeNodes(ctx, nil)
				cancel()
			case <-syncTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				s.SyncDueSubscriptions(ctx)
				cancel()
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *OpenCodeProxyPoolService) SyncDueSubscriptions(ctx context.Context) {
	subscriptions, err := s.repo.ListSubscriptions(ctx)
	if err != nil {
		slog.Warn("opencode_subscription_schedule_list_failed", "error", err)
		return
	}
	now := time.Now()
	for _, subscription := range subscriptions {
		if !subscription.Enabled {
			continue
		}
		interval := time.Duration(subscription.SyncIntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = openCodeDefaultSyncInterval * time.Minute
		}
		due := subscription.LastFetchedAt == nil || now.Sub(*subscription.LastFetchedAt) >= interval
		if subscription.LastError != "" && subscription.LastFetchedAt != nil {
			due = now.Sub(*subscription.LastFetchedAt) >= 5*time.Minute
		}
		if due {
			if _, err := s.SyncSubscription(ctx, subscription.ID); err != nil {
				slog.Warn("opencode_subscription_scheduled_sync_failed", "subscription_id", subscription.ID, "error", err)
			}
		}
	}
}

func (s *OpenCodeProxyPoolService) Stop() {
	if s != nil {
		s.stopOnce.Do(func() { close(s.stop) })
	}
}

func maskSubscriptionURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "***"
	}
	return u.Scheme + "://" + u.Host + "/***"
}

func (s *OpenCodeProxyPoolService) ListSubscriptions(ctx context.Context) ([]ProxySubscription, error) {
	subscriptions, err := s.repo.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	for i := range subscriptions {
		subscriptions[i].HasURL = subscriptions[i].URLCiphertext != ""
		subscriptions[i].URLMasked = "***"
		if rawURL, decryptErr := s.encryptor.Decrypt(subscriptions[i].URLCiphertext); decryptErr == nil {
			subscriptions[i].URLMasked = maskSubscriptionURL(rawURL)
		}
	}
	return subscriptions, nil
}

func (s *OpenCodeProxyPoolService) CreateSubscription(ctx context.Context, name, rawURL string, enabled bool, interval int) (*ProxySubscription, error) {
	name, rawURL = strings.TrimSpace(name), strings.TrimSpace(rawURL)
	if name == "" || rawURL == "" {
		return nil, errors.New("name and url are required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("subscription URL must be an absolute HTTP(S) URL")
	}
	if interval <= 0 {
		interval = openCodeDefaultSyncInterval
	}
	ciphertext, err := s.encryptor.Encrypt(rawURL)
	if err != nil {
		return nil, fmt.Errorf("encrypt subscription URL: %w", err)
	}
	return s.repo.CreateSubscription(ctx, ProxySubscription{Name: name, URLCiphertext: ciphertext, Enabled: enabled, SyncIntervalMinutes: interval, HasURL: true, URLMasked: maskSubscriptionURL(rawURL)})
}

func (s *OpenCodeProxyPoolService) UpdateSubscription(ctx context.Context, id int64, name string, rawURL *string, enabled bool, interval int) (*ProxySubscription, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	existing, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	wasEnabled := existing.Enabled
	existing.Name = strings.TrimSpace(name)
	existing.Enabled = enabled
	if interval > 0 {
		existing.SyncIntervalMinutes = interval
	}
	if rawURL != nil && strings.TrimSpace(*rawURL) != "" {
		parsed, parseErr := url.Parse(strings.TrimSpace(*rawURL))
		if parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return nil, errors.New("subscription URL must be an absolute HTTP(S) URL")
		}
		existing.URLCiphertext, err = s.encryptor.Encrypt(strings.TrimSpace(*rawURL))
		if err != nil {
			return nil, err
		}
	}
	updated, err := s.repo.UpdateSubscription(ctx, *existing)
	if err != nil {
		return nil, err
	}
	if wasEnabled != updated.Enabled {
		if err := s.reloadPersistedMihomo(ctx); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

func (s *OpenCodeProxyPoolService) DeleteSubscription(ctx context.Context, id int64) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if err := s.repo.DeleteSubscription(ctx, id); err != nil {
		return err
	}
	return s.reloadPersistedMihomo(ctx)
}

func (s *OpenCodeProxyPoolService) reloadPersistedMihomo(ctx context.Context) error {
	drafts, err := s.mergeEnabledSubscriptionDrafts(ctx, -1, nil)
	if err != nil {
		return err
	}
	return s.reloadMihomo(ctx, drafts)
}

func (s *OpenCodeProxyPoolService) ListNodes(ctx context.Context, subscriptionID *int64) ([]ManagedProxyNode, error) {
	return s.repo.ListNodes(ctx, subscriptionID)
}

func decodeSubscriptionYAML(body []byte) ([]byte, string) {
	trimmed := bytes.TrimSpace(body)
	if bytes.Contains(trimmed, []byte("proxies:")) {
		return trimmed, "clash_yaml"
	}
	compact := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, string(trimmed))
	if decoded, err := decodeBase64Flexible(compact); err == nil {
		if bytes.Contains(decoded, []byte("proxies:")) {
			return bytes.TrimSpace(decoded), "base64_clash_yaml"
		}
		if bytes.Contains(decoded, []byte("://")) {
			return bytes.TrimSpace(decoded), "base64_share_links"
		}
	}
	if bytes.Contains(trimmed, []byte("://")) {
		return trimmed, "share_links"
	}
	return trimmed, "unknown"
}

func parseOpenCodeSubscription(body []byte) ([]map[string]any, string, error) {
	yamlBody, format := decodeSubscriptionYAML(body)
	if format == "clash_yaml" || format == "base64_clash_yaml" {
		var document struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(yamlBody, &document); err != nil {
			return nil, format, fmt.Errorf("parse subscription: %w", err)
		}
		if len(document.Proxies) == 0 {
			return nil, format, errors.New("subscription contains no Clash proxies")
		}
		return document.Proxies, format, nil
	}
	proxies, err := parseOpenCodeShareLinks(string(yamlBody))
	if err != nil {
		return nil, format, err
	}
	return proxies, format, nil
}

func decodeBase64Flexible(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	encodings := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func parseOpenCodeShareLinks(payload string) ([]map[string]any, error) {
	proxies := make([]map[string]any, 0)
	for _, line := range strings.Fields(payload) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		proxy, err := openCodeShareLinkToMihomo(line)
		if err != nil {
			continue
		}
		proxies = append(proxies, proxy)
	}
	if len(proxies) == 0 {
		return nil, errors.New("subscription contains no supported proxies")
	}
	return proxies, nil
}

func openCodeShareLinkToMihomo(raw string) (map[string]any, error) {
	if strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		decoded, err := decodeBase64Flexible(strings.TrimPrefix(raw, "vmess://"))
		if err != nil {
			return nil, err
		}
		var input map[string]any
		if err := json.Unmarshal(decoded, &input); err != nil {
			return nil, err
		}
		port, _ := strconv.Atoi(fmt.Sprint(input["port"]))
		proxy := map[string]any{"name": fmt.Sprint(input["ps"]), "type": "vmess", "server": fmt.Sprint(input["add"]), "port": port, "uuid": fmt.Sprint(input["id"]), "alterId": 0, "cipher": "auto"}
		if network := strings.TrimSpace(fmt.Sprint(input["net"])); network != "" {
			proxy["network"] = network
		}
		if strings.EqualFold(fmt.Sprint(input["tls"]), "tls") {
			proxy["tls"] = true
		}
		if sni := strings.TrimSpace(fmt.Sprint(input["sni"])); sni != "" {
			proxy["servername"] = sni
		}
		if path := strings.TrimSpace(fmt.Sprint(input["path"])); path != "" {
			proxy["ws-opts"] = map[string]any{"path": path, "headers": map[string]string{"Host": fmt.Sprint(input["host"])}}
		}
		return proxy, nil
	}
	if strings.HasPrefix(strings.ToLower(raw), "ss://") {
		raw = normalizeSSShareLink(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("invalid share link")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return nil, errors.New("invalid share link port")
	}
	protocol := strings.ToLower(u.Scheme)
	if protocol == "hy2" {
		protocol = "hysteria2"
	}
	name, _ := url.QueryUnescape(u.Fragment)
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("%s-%s-%d", protocol, u.Hostname(), port)
	}
	proxy := map[string]any{"name": name, "type": protocol, "server": u.Hostname(), "port": port}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	switch protocol {
	case "ss":
		decoded, err := decodeBase64Flexible(username)
		if err == nil {
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				username, password = parts[0], parts[1]
			}
		}
		if username == "" || password == "" {
			return nil, errors.New("invalid Shadowsocks credentials")
		}
		proxy["cipher"], proxy["password"] = username, password
	case "vless":
		proxy["uuid"] = username
	case "trojan", "hysteria2", "anytls":
		proxy["password"] = username
		if password != "" {
			proxy["password"] = password
		}
	case "tuic":
		proxy["uuid"], proxy["password"] = username, password
	case "http", "https", "socks", "socks5", "socks5h":
		if protocol == "socks" || protocol == "socks5h" {
			proxy["type"] = "socks5"
		}
		if username != "" {
			proxy["username"], proxy["password"] = username, password
		}
	default:
		return nil, fmt.Errorf("unsupported share link protocol %q", protocol)
	}
	applyShareLinkQuery(proxy, u.Query())
	return proxy, nil
}

func normalizeSSShareLink(raw string) string {
	body := strings.TrimPrefix(raw, "ss://")
	fragment := ""
	if index := strings.Index(body, "#"); index >= 0 {
		fragment, body = body[index:], body[:index]
	}
	query := ""
	if index := strings.Index(body, "?"); index >= 0 {
		query, body = body[index:], body[:index]
	}
	if !strings.Contains(body, "@") {
		if decoded, err := decodeBase64Flexible(body); err == nil {
			body = string(decoded)
		}
	}
	return "ss://" + body + query + fragment
}

func applyShareLinkQuery(proxy map[string]any, query url.Values) {
	if sni := strings.TrimSpace(firstOpenCodeNonEmpty(query.Get("sni"), query.Get("servername"))); sni != "" {
		proxy["servername"] = sni
	}
	if network := strings.TrimSpace(query.Get("type")); network != "" && network != "none" {
		proxy["network"] = network
	}
	if security := strings.ToLower(query.Get("security")); security == "tls" || security == "reality" {
		proxy["tls"] = true
	}
	if flow := query.Get("flow"); flow != "" {
		proxy["flow"] = flow
	}
	if fingerprint := firstOpenCodeNonEmpty(query.Get("fp"), query.Get("fingerprint")); fingerprint != "" {
		proxy["client-fingerprint"] = fingerprint
	}
	if query.Get("allowInsecure") == "1" || strings.EqualFold(query.Get("insecure"), "true") {
		proxy["skip-cert-verify"] = true
	}
	if path := query.Get("path"); path != "" {
		proxy["ws-opts"] = map[string]any{"path": path, "headers": map[string]string{"Host": query.Get("host")}}
	}
	if serviceName := firstOpenCodeNonEmpty(query.Get("serviceName"), query.Get("service_name")); serviceName != "" {
		proxy["grpc-opts"] = map[string]any{"grpc-service-name": serviceName}
	}
	if publicKey := query.Get("pbk"); publicKey != "" {
		proxy["reality-opts"] = map[string]any{"public-key": publicKey, "short-id": query.Get("sid")}
	}
	if plugin := query.Get("plugin"); plugin != "" {
		proxy["plugin"] = plugin
	}
	if obfs := query.Get("obfs"); obfs != "" {
		proxy["obfs"] = obfs
		proxy["obfs-password"] = query.Get("obfs-password")
	}
}

func firstOpenCodeNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nodeFingerprint(proxy map[string]any) (string, []byte, error) {
	copyMap := make(map[string]any, len(proxy))
	for key, value := range proxy {
		if key != "name" {
			copyMap[key] = value
		}
	}
	canonical, err := json.Marshal(copyMap)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), canonical, nil
}

func randomListenerCredential() string {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("oc-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func nextListenerPort(used map[int]struct{}) (int, error) {
	for port := openCodeListenerPortStart; port <= openCodeListenerPortEnd; port++ {
		if _, exists := used[port]; !exists {
			used[port] = struct{}{}
			return port, nil
		}
	}
	return 0, errors.New("OpenCode Mihomo listener port range is exhausted")
}

func (s *OpenCodeProxyPoolService) fetchSubscription(ctx context.Context, rawURL string) ([]byte, string, error) {
	userAgents := []string{"clash", "mihomo/1.19", "sub2api-opencode/1.0"}
	var lastErr error
	for _, ua := range userAgents {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", ua)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, openCodeSubscriptionMaxBytes+1))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK || int64(len(body)) > openCodeSubscriptionMaxBytes {
			lastErr = fmt.Errorf("subscription fetch HTTP %d", resp.StatusCode)
			continue
		}
		return body, ua, nil
	}
	return nil, "", fmt.Errorf("subscription fetch failed: %w", lastErr)
}

func (s *OpenCodeProxyPoolService) SyncSubscription(ctx context.Context, id int64) ([]ManagedProxyNode, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	subscription, err := s.repo.GetSubscription(ctx, id)
	if err != nil {
		return nil, err
	}
	rawURL, err := s.encryptor.Decrypt(subscription.URLCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt subscription URL: %w", err)
	}
	body, ua, err := s.fetchSubscription(ctx, rawURL)
	if err != nil {
		_ = s.repo.UpdateSubscriptionError(ctx, id, err.Error())
		return nil, err
	}
	proxies, format, err := parseOpenCodeSubscription(body)
	if err != nil {
		_ = s.repo.UpdateSubscriptionError(ctx, id, err.Error())
		return nil, err
	}
	existing, err := s.repo.ListNodes(ctx, &id)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]ManagedProxyNode, len(existing))
	for _, node := range existing {
		byKey[node.NodeKey] = node
	}
	used, err := s.repo.UsedListenerPorts(ctx)
	if err != nil {
		return nil, err
	}
	drafts := make([]ManagedProxyNodeDraft, 0, len(proxies))
	seenKeys := make(map[string]struct{}, len(proxies))
	for index, proxyConfig := range proxies {
		name := strings.TrimSpace(fmt.Sprint(proxyConfig["name"]))
		protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(proxyConfig["type"])))
		if name == "" || protocol == "" {
			continue
		}
		key, canonical, keyErr := nodeFingerprint(proxyConfig)
		if keyErr != nil {
			return nil, keyErr
		}
		if _, duplicate := seenKeys[key]; duplicate {
			continue
		}
		seenKeys[key] = struct{}{}
		ciphertext, encErr := s.encryptor.Encrypt(string(canonical))
		if encErr != nil {
			return nil, encErr
		}
		node := byKey[key]
		node.SubscriptionID = id
		node.NodeKey = key
		node.DisplayName = name
		node.Protocol = protocol
		node.ConfigCiphertext = ciphertext
		node.SyncStatus = "active"
		if node.MihomoName == "" {
			node.MihomoName = fmt.Sprintf("oc-%d-%03d-%s", id, index+1, key[:8])
		}
		proxyConfig["name"] = node.MihomoName
		if node.ListenerPort == 0 {
			node.ListenerPort, err = nextListenerPort(used)
			if err != nil {
				return nil, err
			}
			node.ListenerUsername = "oc-" + key[:10]
			node.ListenerPassword = randomListenerCredential()
		}
		drafts = append(drafts, ManagedProxyNodeDraft{ManagedProxyNode: node, ProxyConfig: proxyConfig})
	}
	if len(drafts) == 0 {
		return nil, errors.New("subscription contains no usable nodes")
	}
	allDrafts, err := s.mergeEnabledSubscriptionDrafts(ctx, id, drafts)
	if err != nil {
		return nil, err
	}
	previousConfig, _ := os.ReadFile(filepath.Join(s.configDir, "config.yaml"))
	if err := s.reloadMihomoWithRollback(ctx, allDrafts, previousConfig); err != nil {
		_ = s.repo.UpdateSubscriptionError(ctx, id, err.Error())
		return nil, err
	}
	if err := s.repo.CommitSubscriptionSync(ctx, id, drafts, format, ua); err != nil {
		if restoreErr := s.restoreMihomoConfig(ctx, previousConfig); restoreErr != nil {
			slog.Error("opencode_mihomo_rollback_failed", "subscription_id", id, "error", restoreErr)
		}
		return nil, err
	}
	nodes, err := s.repo.ListNodes(ctx, &id)
	if err == nil {
		_, _ = s.ProbeNodes(ctx, nodeIDs(nodes))
	}
	return nodes, err
}

func (s *OpenCodeProxyPoolService) reloadMihomoWithRollback(ctx context.Context, drafts []ManagedProxyNodeDraft, previous []byte) error {
	if err := s.reloadMihomo(ctx, drafts); err != nil {
		if len(previous) > 0 {
			if restoreErr := s.restoreMihomoConfig(ctx, previous); restoreErr != nil {
				return fmt.Errorf("reload Mihomo candidate: %w; restore last-known-good: %v", err, restoreErr)
			}
		}
		return err
	}
	return nil
}

func (s *OpenCodeProxyPoolService) mergeEnabledSubscriptionDrafts(ctx context.Context, syncingID int64, current []ManagedProxyNodeDraft) ([]ManagedProxyNodeDraft, error) {
	subscriptions, err := s.repo.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	enabled := make(map[int64]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		if subscription.Enabled {
			enabled[subscription.ID] = struct{}{}
		}
	}
	merged := append([]ManagedProxyNodeDraft(nil), current...)
	nodes, err := s.repo.ListNodes(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if node.SubscriptionID == syncingID || node.SyncStatus != "active" {
			continue
		}
		if _, ok := enabled[node.SubscriptionID]; !ok {
			continue
		}
		raw, err := s.encryptor.Decrypt(node.ConfigCiphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt managed node %d: %w", node.ID, err)
		}
		var proxyConfig map[string]any
		if err := json.Unmarshal([]byte(raw), &proxyConfig); err != nil {
			return nil, fmt.Errorf("decode managed node %d: %w", node.ID, err)
		}
		proxyConfig["name"] = node.MihomoName
		merged = append(merged, ManagedProxyNodeDraft{ManagedProxyNode: node, ProxyConfig: proxyConfig})
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].SubscriptionID == merged[j].SubscriptionID {
			return merged[i].ListenerPort < merged[j].ListenerPort
		}
		return merged[i].SubscriptionID < merged[j].SubscriptionID
	})
	return merged, nil
}

func nodeIDs(nodes []ManagedProxyNode) []int64 {
	ids := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func (s *OpenCodeProxyPoolService) reloadMihomo(ctx context.Context, drafts []ManagedProxyNodeDraft) error {
	proxies := make([]map[string]any, 0, len(drafts))
	listeners := make([]map[string]any, 0, len(drafts))
	for _, draft := range drafts {
		proxies = append(proxies, draft.ProxyConfig)
		listeners = append(listeners, map[string]any{
			"name": "listener-" + draft.NodeKey[:12], "type": "mixed", "port": draft.ListenerPort,
			"listen": "0.0.0.0", "proxy": draft.MihomoName,
			"users": []map[string]string{{"username": draft.ListenerUsername, "password": draft.ListenerPassword}},
		})
	}
	config := map[string]any{
		"mode": "rule", "log-level": "warning", "allow-lan": true,
		"external-controller": "0.0.0.0:9090", "secret": s.secret,
		"proxies": proxies, "listeners": listeners,
		"rules": []string{"MATCH,DIRECT"},
	}
	encoded, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return fmt.Errorf("create Mihomo config directory: %w", err)
	}
	nextPath := filepath.Join(s.configDir, "config.next.yaml")
	if err := os.WriteFile(nextPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write Mihomo candidate config: %w", err)
	}
	if err := s.loadMihomoConfig(ctx, "/root/.config/mihomo/config.next.yaml"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.configDir, "config.yaml"), encoded, 0o600); err != nil {
		return fmt.Errorf("promote Mihomo config: %w", err)
	}
	return nil
}

func (s *OpenCodeProxyPoolService) loadMihomoConfig(ctx context.Context, path string) error {
	payload, _ := json.Marshal(map[string]string{"path": path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.controller+"/configs?force=true", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.secret != "" {
		req.Header.Set("Authorization", "Bearer "+s.secret)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reload Mihomo candidate: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("reload Mihomo candidate HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *OpenCodeProxyPoolService) restoreMihomoConfig(ctx context.Context, previous []byte) error {
	if len(previous) == 0 {
		return errors.New("last-known-good Mihomo config is unavailable")
	}
	rollbackPath := filepath.Join(s.configDir, "config.rollback.yaml")
	if err := os.WriteFile(rollbackPath, previous, 0o600); err != nil {
		return err
	}
	if err := s.loadMihomoConfig(ctx, "/root/.config/mihomo/config.rollback.yaml"); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.configDir, "config.yaml"), previous, 0o600)
}

func classifyProbeError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "tls") || strings.Contains(text, "certificate"):
		return "tls"
	case strings.Contains(text, "no such host") || strings.Contains(text, "dns"):
		return "dns"
	case strings.Contains(text, "timeout") || strings.Contains(text, "deadline"):
		return "timeout"
	default:
		return "transport"
	}
}

func (s *OpenCodeProxyPoolService) ProbeNodes(ctx context.Context, ids []int64) ([]OpenCodeNodeProbeResult, error) {
	nodes, err := s.repo.ListNodes(ctx, nil)
	if err != nil {
		return nil, err
	}
	selected := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	results := make([]OpenCodeNodeProbeResult, 0, len(nodes))
	sem := make(chan struct{}, 3)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, node := range nodes {
		if len(selected) > 0 {
			if _, ok := selected[node.ID]; !ok {
				continue
			}
		}
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result := s.probeNode(ctx, node)
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].NodeID < results[j].NodeID })
	resultNodeIDs := make(map[int64]struct{}, len(results))
	for _, result := range results {
		resultNodeIDs[result.NodeID] = struct{}{}
	}
	existingExitOwners := make(map[string]int64)
	for _, node := range nodes {
		if _, beingProbed := resultNodeIDs[node.ID]; beingProbed {
			continue
		}
		if node.HealthStatus == "healthy" && node.ExitIP != "" && node.DuplicateOfNodeID == nil {
			existingExitOwners[node.ExitIP] = node.ID
		}
	}
	canonical := make(map[string]int)
	for i := range results {
		if !results[i].Success || results[i].ExitIP == "" {
			continue
		}
		if ownerID, exists := existingExitOwners[results[i].ExitIP]; exists {
			results[i].Success = false
			results[i].HealthStatus = "duplicate_exit"
			results[i].FailureType = "duplicate_exit"
			results[i].DuplicateOfNodeID = &ownerID
			continue
		}
		if first, exists := canonical[results[i].ExitIP]; exists {
			winner := &results[first]
			candidate := &results[i]
			if candidate.LatencyMs < winner.LatencyMs {
				winner, candidate = candidate, winner
				canonical[results[i].ExitIP] = i
			}
			winnerID := winner.NodeID
			candidate.Success = false
			candidate.HealthStatus = "duplicate_exit"
			candidate.FailureType = "duplicate_exit"
			candidate.DuplicateOfNodeID = &winnerID
		} else {
			canonical[results[i].ExitIP] = i
		}
	}
	if err := s.repo.UpdateNodeProbeResults(ctx, results); err != nil {
		return nil, err
	}
	stoppedAccountIDs, err := s.repo.ReconcileEgressLeases(ctx)
	if err != nil {
		return nil, err
	}
	for _, accountID := range stoppedAccountIDs {
		if s.accountRepo != nil {
			if err := s.accountRepo.SetSchedulable(ctx, accountID, false); err != nil {
				slog.Warn("opencode_duplicate_exit_disable_account_failed", "account_id", accountID, "error", err)
			}
		}
	}
	return results, nil
}

func (s *OpenCodeProxyPoolService) ValidateAccountEgress(ctx context.Context, account *Account, acquire bool) error {
	if account == nil || !account.IsOpenCodeZen() {
		return nil
	}
	mode := account.GetOpenCodeEgressMode()
	if mode == OpenCodeEgressModeServerDirect {
		if account.ProxyID != nil {
			return infraerrors.BadRequest("OPENCODE_DIRECT_WITH_PROXY", "server_direct OpenCode accounts cannot select a proxy")
		}
		result := s.probeDirect(ctx)
		if !result.Success {
			return fmt.Errorf("%w: %s", ErrOpenCodeProxyUnhealthy, result.FailureType)
		}
		if acquire {
			return s.repo.AcquireEgressLease(ctx, account.ID, nil, mode, result.ExitIP, time.Now().UTC())
		}
		return nil
	}
	if account.ProxyID == nil || *account.ProxyID <= 0 {
		return ErrOpenCodeProxyRequired
	}
	nodes, err := s.repo.ListNodes(ctx, nil)
	if err != nil {
		return err
	}
	var selected *ManagedProxyNode
	for i := range nodes {
		if nodes[i].ProxyID != nil && *nodes[i].ProxyID == *account.ProxyID {
			selected = &nodes[i]
			break
		}
	}
	if selected == nil || selected.SyncStatus != "active" {
		return infraerrors.BadRequest("OPENCODE_MANAGED_PROXY_REQUIRED", "selected proxy is not an active managed OpenCode node")
	}
	if selected.LastProbeAt == nil || time.Since(*selected.LastProbeAt) > openCodeNodeProbeFreshness {
		if _, err := s.ProbeNodes(ctx, []int64{selected.ID}); err != nil {
			return err
		}
		nodes, err = s.repo.ListNodes(ctx, nil)
		if err != nil {
			return err
		}
		for i := range nodes {
			if nodes[i].ID == selected.ID {
				selected = &nodes[i]
				break
			}
		}
	}
	if selected.HealthStatus != "healthy" || selected.ExitIP == "" || selected.DuplicateOfNodeID != nil {
		return fmt.Errorf("%w: %s", ErrOpenCodeProxyUnhealthy, selected.HealthStatus)
	}
	if acquire {
		return s.repo.AcquireEgressLease(ctx, account.ID, account.ProxyID, mode, selected.ExitIP, time.Now().UTC())
	}
	return nil
}

func (s *OpenCodeProxyPoolService) ReleaseAccountEgress(ctx context.Context, accountID int64) error {
	return s.repo.ReleaseEgressLease(ctx, accountID)
}

func (s *OpenCodeProxyPoolService) IsManagedProxy(ctx context.Context, proxyID int64) (bool, error) {
	return s.repo.IsManagedProxy(ctx, proxyID)
}

func (s *OpenCodeProxyPoolService) probeDirect(ctx context.Context) OpenCodeNodeProbeResult {
	result := OpenCodeNodeProbeResult{HealthStatus: "transport_error"}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: openCodeProbeTimeout}
	start := time.Now()
	exitReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api64.ipify.org?format=json", nil)
	exitResp, err := client.Do(exitReq)
	if err != nil {
		result.FailureType, result.FailureMessage = classifyProbeError(err), err.Error()
		return result
	}
	var exitPayload struct {
		IP string `json:"ip"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(exitResp.Body, 4096)).Decode(&exitPayload)
	_ = exitResp.Body.Close()
	if decodeErr != nil || exitResp.StatusCode != http.StatusOK {
		result.FailureType = "exit_probe"
		return result
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(exitPayload.IP))
	if err != nil {
		result.FailureType = "exit_probe"
		return result
	}
	result.ExitIP = addr.Unmap().String()
	populateOpenCodeGeo(ctx, client, &result)
	body := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"Reply OK"}],"stream":false,"max_tokens":8}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, OpenCodeZenBaseURL+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "opencode-cli/1.0.0")
	resp, err := client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.FailureType, result.FailureMessage = classifyProbeError(err), err.Error()
		return result
	}
	result.OpenCodeHTTPStatus = resp.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		result.Success = true
		result.HealthStatus = "healthy"
	case http.StatusTooManyRequests:
		result.HealthStatus, result.FailureType = "rate_limited", "rate_limited"
	default:
		result.HealthStatus, result.FailureType = "http_error", "http"
	}
	return result
}

func (s *OpenCodeProxyPoolService) probeNode(ctx context.Context, node ManagedProxyNode) OpenCodeNodeProbeResult {
	result := OpenCodeNodeProbeResult{NodeID: node.ID, HealthStatus: "transport_error"}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", s.proxyHost, node.ListenerPort), User: url.UserPassword(node.ListenerUsername, node.ListenerPassword)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	client := &http.Client{Transport: transport, Timeout: openCodeProbeTimeout}
	start := time.Now()
	exitReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api64.ipify.org?format=json", nil)
	exitResp, err := client.Do(exitReq)
	if err != nil {
		result.FailureType, result.FailureMessage = classifyProbeError(err), err.Error()
		return result
	}
	var exitPayload struct {
		IP string `json:"ip"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(exitResp.Body, 4096)).Decode(&exitPayload)
	_ = exitResp.Body.Close()
	if decodeErr != nil || exitResp.StatusCode != http.StatusOK {
		result.FailureType, result.FailureMessage = "exit_probe", fmt.Sprintf("exit probe HTTP %d", exitResp.StatusCode)
		return result
	}
	addr, parseErr := netip.ParseAddr(strings.TrimSpace(exitPayload.IP))
	if parseErr != nil {
		result.FailureType, result.FailureMessage = "exit_probe", "exit probe returned an invalid IP"
		return result
	}
	result.ExitIP = addr.Unmap().String()
	populateOpenCodeGeo(ctx, client, &result)
	body := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"Reply OK"}],"stream":false,"max_tokens":8}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, OpenCodeZenBaseURL+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "opencode-cli/1.0.0")
	resp, err := client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.FailureType, result.FailureMessage = classifyProbeError(err), err.Error()
		return result
	}
	result.OpenCodeHTTPStatus = resp.StatusCode
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		result.Success, result.HealthStatus = true, "healthy"
	case http.StatusTooManyRequests:
		result.HealthStatus, result.FailureType = "rate_limited", "rate_limited"
	case http.StatusUnauthorized, http.StatusForbidden:
		result.HealthStatus, result.FailureType = "auth_error", "auth"
	default:
		result.HealthStatus, result.FailureType = "http_error", "http"
	}
	return result
}

func populateOpenCodeGeo(ctx context.Context, client *http.Client, result *OpenCodeNodeProbeResult) {
	if client == nil || result == nil || result.ExitIP == "" {
		return
	}
	endpoint := openCodeGeoBaseURL + url.PathEscape(result.ExitIP) + "?fields=status,country,regionName,city"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var payload struct {
		Status     string `json:"status"`
		Country    string `json:"country"`
		RegionName string `json:"regionName"`
		City       string `json:"city"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&payload) != nil || payload.Status != "success" {
		return
	}
	result.Country = strings.TrimSpace(payload.Country)
	parts := make([]string, 0, 2)
	if region := strings.TrimSpace(payload.RegionName); region != "" {
		parts = append(parts, region)
	}
	if city := strings.TrimSpace(payload.City); city != "" && !strings.EqualFold(city, strings.TrimSpace(payload.RegionName)) {
		parts = append(parts, city)
	}
	result.Region = strings.Join(parts, " / ")
}

func (s *OpenCodeProxyPoolService) LoadModelSnapshot(ctx context.Context) error {
	if s.settingRepo == nil {
		return nil
	}
	raw, err := s.settingRepo.GetValue(ctx, OpenCodeModelSnapshotSettingKey)
	if err != nil {
		return err
	}
	var snapshot OpenCodeModelRegistryStatus
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil || len(snapshot.IDs) == 0 {
		return errors.New("invalid OpenCode model snapshot")
	}
	defaultOpenCodeFreeModels.Replace(snapshot.IDs)
	s.modelMu.Lock()
	s.modelStatus = snapshot
	s.modelMu.Unlock()
	return nil
}

func parseOpenCodeFreeModelIDs(html string) []string {
	tablePattern := regexpMustCompile(`(?is)<table[^>]*>(.*?)</table>`)
	rowPattern := regexpMustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	cellPattern := regexpMustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	tagPattern := regexpMustCompile(`(?is)<[^>]+>`)
	set := make(map[string]struct{})
	for _, table := range tablePattern.FindAllStringSubmatch(html, -1) {
		if !strings.Contains(strings.ToLower(table[1]), "cached") || !strings.Contains(strings.ToLower(table[1]), "model") {
			continue
		}
		for _, row := range rowPattern.FindAllStringSubmatch(table[1], -1) {
			cells := cellPattern.FindAllStringSubmatch(row[1], -1)
			if len(cells) < 3 {
				continue
			}
			clean := func(value string) string { return strings.TrimSpace(tagPattern.ReplaceAllString(value, "")) }
			if strings.EqualFold(clean(cells[1][1]), "Free") && strings.EqualFold(clean(cells[2][1]), "Free") {
				if id := normalizeOpenCodeModelID(clean(cells[0][1])); id != "" {
					set[id] = struct{}{}
				}
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func regexpMustCompile(pattern string) *regexp.Regexp { return regexp.MustCompile(pattern) }

func (s *OpenCodeProxyPoolService) RefreshModels(ctx context.Context) (*OpenCodeModelRegistryStatus, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, openCodePricingURL, nil)
	req.Header.Set("User-Agent", "sub2api-opencode/1.0")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return s.recordModelError(err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	_ = resp.Body.Close()
	if readErr != nil || resp.StatusCode != http.StatusOK {
		return s.recordModelError(fmt.Errorf("pricing page HTTP %d", resp.StatusCode))
	}
	ids := parseOpenCodeFreeModelIDs(string(body))
	if len(ids) == 0 {
		return s.recordModelError(errors.New("pricing page parsed zero free models"))
	}
	now := time.Now().UTC()
	status := OpenCodeModelRegistryStatus{IDs: ids, Count: len(ids), LastFetchedAt: &now}
	defaultOpenCodeFreeModels.Replace(ids)
	s.modelMu.Lock()
	s.modelStatus = status
	s.modelMu.Unlock()
	if s.settingRepo != nil {
		encoded, _ := json.Marshal(status)
		if err := s.settingRepo.Set(ctx, OpenCodeModelSnapshotSettingKey, string(encoded)); err != nil {
			slog.Warn("opencode_model_snapshot_persist_failed", "error", err)
		}
	}
	return &status, nil
}

func (s *OpenCodeProxyPoolService) recordModelError(err error) (*OpenCodeModelRegistryStatus, error) {
	s.modelMu.Lock()
	s.modelStatus.LastError = err.Error()
	status := s.modelStatus
	s.modelMu.Unlock()
	return &status, err
}

func (s *OpenCodeProxyPoolService) ModelStatus() OpenCodeModelRegistryStatus {
	s.modelMu.RLock()
	status := s.modelStatus
	s.modelMu.RUnlock()
	status.IDs = append([]string(nil), status.IDs...)
	return status
}
