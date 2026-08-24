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
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

const (
	OpenCodeModelSnapshotSettingKey = "opencode_zen_free_models_v2"
	openCodeZenModelsURL            = OpenCodeZenBaseURL + "/models"
	openCodeZenModelsMaxBytes       = int64(4 << 20)
	openCodeSubscriptionMaxBytes    = int64(8 << 20)
	openCodeProbeTimeout            = 20 * time.Second
	openCodeNodeProbeFreshness      = 15 * time.Minute
	openCodeWorkerProbeValidity     = 26 * time.Hour
	openCodeDefaultSyncInterval     = 360
	openCodeListenerPortStart       = 22000
	openCodeListenerPortEnd         = 29999
)

var (
	ErrProxySubscriptionNotFound = infraerrors.NotFound("PROXY_SUBSCRIPTION_NOT_FOUND", "proxy subscription not found")
	ErrProxySubscriptionInUse    = infraerrors.Conflict("PROXY_SUBSCRIPTION_IN_USE", "proxy subscription has nodes bound to accounts")
	ErrManagedProxyNodeNotFound  = infraerrors.NotFound("MANAGED_PROXY_NODE_NOT_FOUND", "managed OpenCode proxy node not found")
	ErrOpenCodeDuplicateExitIP   = infraerrors.Conflict("OPENCODE_DUPLICATE_EXIT_IP", "OpenCode exit IP is already leased")
	ErrOpenCodeProxyRequired     = infraerrors.BadRequest("OPENCODE_PROXY_REQUIRED", "OpenCode proxy egress requires a managed proxy")
	ErrOpenCodeProxyUnhealthy    = infraerrors.Conflict("OPENCODE_PROXY_UNHEALTHY", "OpenCode managed proxy is not healthy")
	ErrOpenCodePoolUnavailable   = infraerrors.New(http.StatusServiceUnavailable, "OPENCODE_POOL_UNAVAILABLE", "OpenCode worker pool has no available workers")
	ErrOpenCodeSystemResource    = infraerrors.Conflict("OPENCODE_SYSTEM_RESOURCE", "OpenCode system-managed resources are read-only")
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
	HealthyNodeCount    int        `json:"healthy_node_count"`
	FailedNodeCount     int        `json:"failed_node_count"`
	InactiveNodeCount   int        `json:"inactive_node_count"`
	LastFormat          string     `json:"last_format,omitempty"`
	LastUserAgent       string     `json:"last_user_agent,omitempty"`
	HasURL              bool       `json:"has_url"`
	URLMasked           string     `json:"url_masked"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	URLCiphertext       string     `json:"-"`
	SourceType          string     `json:"source_type"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
}

type ExpiredProxySourceCleanupResult struct {
	Subscriptions int
	Nodes         int
	Accounts      int
}

type ManagedProxyNode struct {
	ID                    int64      `json:"id"`
	SubscriptionID        int64      `json:"subscription_id"`
	ProxyID               *int64     `json:"proxy_id"`
	NodeKey               string     `json:"node_key"`
	DisplayName           string     `json:"display_name"`
	MihomoName            string     `json:"mihomo_name"`
	Protocol              string     `json:"protocol"`
	ListenerPort          int        `json:"listener_port"`
	TransportMode         string     `json:"transport_mode"`
	SyncStatus            string     `json:"sync_status"`
	HealthStatus          string     `json:"health_status"`
	LastSeenAt            *time.Time `json:"last_seen_at"`
	ExitIP                string     `json:"exit_ip,omitempty"`
	Country               string     `json:"country,omitempty"`
	Region                string     `json:"region,omitempty"`
	LatencyMs             *int64     `json:"latency_ms"`
	OpenCodeHTTPStatus    *int       `json:"opencode_http_status"`
	FailureType           string     `json:"failure_type,omitempty"`
	FailureMessage        string     `json:"failure_message,omitempty"`
	LastProbeAt           *time.Time `json:"last_probe_at"`
	DuplicateOfNodeID     *int64     `json:"duplicate_of_node_id"`
	UnavailableSince      *time.Time `json:"unavailable_since"`
	LastSuccessfulProbeAt *time.Time `json:"last_successful_probe_at"`
	ConsecutiveFailures   int        `json:"consecutive_failures"`
	RetryAt               *time.Time `json:"retry_at"`
	RetryCount            int        `json:"retry_count"`
	ListenerUsername      string     `json:"-"`
	ListenerPassword      string     `json:"-"`
	ProxyProtocol         string     `json:"-"`
	ProxyHost             string     `json:"-"`
	ProxyPort             int        `json:"-"`
	ConfigCiphertext      string     `json:"-"`
}

type ManagedProxyNodeDraft struct {
	ManagedProxyNode
	ProxyConfig map[string]any
}

type OpenCodeNodeProbeResult struct {
	NodeID             int64      `json:"node_id"`
	Success            bool       `json:"success"`
	HealthStatus       string     `json:"health_status"`
	ExitIP             string     `json:"exit_ip,omitempty"`
	Country            string     `json:"country,omitempty"`
	Region             string     `json:"region,omitempty"`
	LatencyMs          int64      `json:"latency_ms,omitempty"`
	OpenCodeHTTPStatus int        `json:"opencode_http_status,omitempty"`
	FailureType        string     `json:"failure_type,omitempty"`
	FailureMessage     string     `json:"failure_message,omitempty"`
	DuplicateOfNodeID  *int64     `json:"duplicate_of_node_id,omitempty"`
	RetryAt            *time.Time `json:"retry_at,omitempty"`
}

type OpenCodeMaintenanceJob struct {
	ID               int64      `json:"id"`
	JobType          string     `json:"job_type"`
	TriggerType      string     `json:"trigger_type"`
	Status           string     `json:"status"`
	SourceName       string     `json:"source_name,omitempty"`
	SubscriptionID   *int64     `json:"subscription_id,omitempty"`
	TotalNodes       int        `json:"total_nodes"`
	ProcessedNodes   int        `json:"processed_nodes"`
	HealthyNodes     int        `json:"healthy_nodes"`
	FailedNodes      int        `json:"failed_nodes"`
	RateLimitedNodes int        `json:"rate_limited_nodes"`
	DuplicateNodes   int        `json:"duplicate_nodes"`
	DeletedNodes     int        `json:"deleted_nodes"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type OpenCodeMaintenanceStatus struct {
	NextRunAt *time.Time              `json:"next_run_at,omitempty"`
	LatestJob *OpenCodeMaintenanceJob `json:"latest_job,omitempty"`
}

type OpenCodeModelRegistryStatus struct {
	IDs           []string   `json:"ids"`
	Count         int        `json:"count"`
	LastFetchedAt *time.Time `json:"last_fetched_at"`
	LastError     string     `json:"last_error,omitempty"`
	UsingBaseline bool       `json:"using_baseline"`
	// Source 取值 baseline / snapshot / live，UsingBaseline == (Source == "baseline") 为派生不变式。
	Source      string   `json:"source,omitempty"`
	LiveIDs     []string `json:"live_ids,omitempty"`
	ZeroCostIDs []string `json:"zero_cost_ids,omitempty"`
	PreviousIDs []string `json:"previous_ids,omitempty"`
	Added       []string `json:"added,omitempty"`
	Removed     []string `json:"removed,omitempty"`
}

type OpenCodePool struct {
	ID                    int64      `json:"id"`
	Name                  string     `json:"name"`
	GroupID               int64      `json:"group_id"`
	Enabled               bool       `json:"enabled"`
	UpstreamKeyConfigured bool       `json:"upstream_key_configured"`
	IncludeServerDirect   bool       `json:"include_server_direct"`
	WorkerConcurrency     int        `json:"worker_concurrency"`
	ReconcileStatus       string     `json:"reconcile_status"`
	ReconcileError        string     `json:"reconcile_error,omitempty"`
	LastReconciledAt      *time.Time `json:"last_reconciled_at"`
	ActiveWorkers         int        `json:"active_workers"`
	HealthyNodes          int        `json:"healthy_nodes"`
	RateLimitedNodes      int        `json:"rate_limited_nodes"`
	DuplicateNodes        int        `json:"duplicate_nodes"`
	FailedNodes           int        `json:"failed_nodes"`
	ProbeIntervalMinutes  int        `json:"probe_interval_minutes"`
	ServerDirectStatus    string     `json:"server_direct_status,omitempty"`
	AnonymousLaneEnabled  bool       `json:"anonymous_lane_enabled"`
	// AnonymousWorkerLimit 为 0 表示不限量（铺满全部合格节点）。
	AnonymousWorkerLimit   int       `json:"anonymous_worker_limit"`
	ActiveKeyedWorkers     int       `json:"active_keyed_workers"`
	ActiveAnonymousWorkers int       `json:"active_anonymous_workers"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	UpstreamKeyCiphertext  string    `json:"-"`
	Group                  *Group    `json:"-"`
}

type OpenCodePoolWorker struct {
	ID                 int64      `json:"id"`
	PoolID             int64      `json:"pool_id"`
	ManagedNodeID      *int64     `json:"managed_node_id"`
	AccountID          int64      `json:"account_id"`
	ProxyID            *int64     `json:"proxy_id"`
	DisplayName        string     `json:"display_name"`
	EgressMode         string     `json:"egress_mode"`
	Lane               string     `json:"lane"`
	ExitIP             string     `json:"exit_ip,omitempty"`
	Status             string     `json:"status"`
	ErrorReason        string     `json:"error_reason,omitempty"`
	HealthStatus       string     `json:"health_status,omitempty"`
	OpenCodeHTTPStatus *int       `json:"opencode_http_status"`
	LastProbeAt        *time.Time `json:"last_probe_at"`
	RateLimitResetAt   *time.Time `json:"rate_limit_reset_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type OpenCodePoolUpdate struct {
	Enabled              *bool
	IncludeServerDirect  *bool
	WorkerConcurrency    *int
	UpstreamAPIKey       *string
	ClearUpstreamAPIKey  bool
	AnonymousLaneEnabled *bool
	AnonymousWorkerLimit *int
}

type OpenCodePoolBindingMutation struct {
	APIKeyID int64
	PoolID   int64
	UserID   int64
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
	CreateMaintenanceJob(context.Context, OpenCodeMaintenanceJob, string) (*OpenCodeMaintenanceJob, error)
	ClaimMaintenanceJob(context.Context, int64) (bool, error)
	UpdateMaintenanceJob(context.Context, OpenCodeMaintenanceJob) error
	GetMaintenanceJob(context.Context, int64) (*OpenCodeMaintenanceJob, error)
	GetLatestMaintenanceJob(context.Context) (*OpenCodeMaintenanceJob, error)
	ListRetryableNodeIDs(context.Context, time.Time, int) ([]int64, error)
	CreateUploadedSubscription(context.Context, string, *time.Time) (*ProxySubscription, error)
	CommitUploadedNodes(context.Context, int64, []ManagedProxyNodeDraft) error
	DeleteExpiredUploadedSubscriptions(context.Context, time.Time) (ExpiredProxySourceCleanupResult, error)
	DeleteManagedNode(context.Context, int64, string) error
	ListTombstonedNodeKeys(context.Context, int64) (map[string]struct{}, error)
	ReconcileEgressLeases(context.Context) ([]int64, error)
	IsManagedProxy(context.Context, int64) (bool, error)
	AcquireEgressLease(context.Context, int64, *int64, string, string, string, time.Time) error
	ReleaseEgressLease(context.Context, int64) error
	EnsureDefaultPool(context.Context) (*OpenCodePool, error)
	GetOpenCodePool(context.Context) (*OpenCodePool, error)
	UpdateOpenCodePool(context.Context, OpenCodePool) (*OpenCodePool, error)
	ListOpenCodePoolWorkers(context.Context, int64) ([]OpenCodePoolWorker, error)
	ReconcileOpenCodePoolWorkers(context.Context, OpenCodePool, string, *OpenCodeNodeProbeResult) ([]OpenCodePoolWorker, error)
	BindAPIKeyToOpenCodePool(context.Context, int64, int64, *int64) (*OpenCodePoolBindingMutation, error)
	UnbindAPIKeyFromOpenCodePool(context.Context, int64) (*OpenCodePoolBindingMutation, error)
	ListOpenCodeBoundUserIDs(context.Context, int64) ([]int64, error)
	IsOpenCodeSystemWorkerAccount(context.Context, int64) (bool, error)
	IsOpenCodeSystemGroup(context.Context, int64) (bool, error)
}

type OpenCodeProxyPoolService struct {
	repo                 OpenCodeProxyPoolRepository
	encryptor            SecretEncryptor
	settingRepo          SettingRepository
	accountRepo          AccountRepository
	groupRepo            GroupRepository
	authCacheInvalidator APIKeyAuthCacheInvalidator
	httpClient           *http.Client
	controller           string
	secret               string
	configDir            string
	proxyHost            string
	syncMu               sync.Mutex
	// mihomoMu serializes the write-candidate → controller-load-by-path → promote
	// sequence. Concurrent runs can have the controller load one candidate while a
	// different candidate's bytes get promoted to config.yaml, leaving the runtime
	// state diverged from disk until the next reload. Distinct from syncMu, which
	// several reload callers already hold.
	mihomoMu          sync.Mutex
	modelMu           sync.RWMutex
	modelStatus       OpenCodeModelRegistryStatus
	poolMu            sync.RWMutex
	poolStatus        *OpenCodePool
	probeMu           sync.Mutex
	retryMu           sync.Mutex
	retryRunning      bool
	topologyMu        sync.Mutex
	topologyDirty     bool
	topologyRunning   bool
	stop              chan struct{}
	stopOnce          sync.Once
	nextMaintenanceMu sync.RWMutex
	nextMaintenanceAt time.Time
}

func NewOpenCodeProxyPoolService(repo OpenCodeProxyPoolRepository, encryptor SecretEncryptor, settingRepo SettingRepository, accountRepo AccountRepository, deps ...any) *OpenCodeProxyPoolService {
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
	sharedClient, err := httpclient.GetClient(httpclient.Options{Timeout: openCodeProbeTimeout})
	if err != nil {
		slog.Warn("opencode_shared_http_client_init_failed", "error", err)
		sharedClient = &http.Client{Timeout: openCodeProbeTimeout}
	}
	svc := &OpenCodeProxyPoolService{
		repo: repo, encryptor: encryptor, settingRepo: settingRepo, accountRepo: accountRepo,
		httpClient: sharedClient,
		controller: controller, secret: strings.TrimSpace(os.Getenv("OPENCODE_MIHOMO_SECRET")),
		configDir: configDir, proxyHost: proxyHost, stop: make(chan struct{}),
		modelStatus: OpenCodeModelRegistryStatus{IDs: defaultOpenCodeFreeModels.IDs(), Count: len(defaultOpenCodeFreeModels.IDs()), UsingBaseline: true, Source: "baseline"},
	}
	for _, dep := range deps {
		switch typed := dep.(type) {
		case GroupRepository:
			svc.groupRepo = typed
		case APIKeyAuthCacheInvalidator:
			svc.authCacheInvalidator = typed
		}
	}
	return svc
}

func (s *OpenCodeProxyPoolService) Start() {
	if s == nil {
		return
	}
	go func() {
		bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), time.Minute)
		_ = s.LoadModelSnapshot(bootstrapCtx)
		if _, err := s.BootstrapPool(bootstrapCtx); err != nil {
			slog.Warn("opencode_pool_bootstrap_failed", "error", err)
		} else {
			if _, err := s.CleanupExpiredUploadedSubscriptions(bootstrapCtx); err != nil {
				slog.Warn("opencode_expired_source_initial_cleanup_failed", "error", err)
			}
			if _, err := s.ReconcileWorkers(bootstrapCtx); err != nil {
				slog.Warn("opencode_pool_initial_reconcile_failed", "error", err)
			}
		}
		bootstrapCancel()
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = s.RefreshModels(refreshCtx)
		refreshCancel()
		if dead := deadOpenCodeEffortAliases(); len(dead) > 0 {
			slog.Warn("opencode_effort_alias_dead", "aliases", dead)
		}
		modelTicker := time.NewTicker(6 * time.Hour)
		syncTicker := time.NewTicker(time.Minute)
		retryTicker := time.NewTicker(time.Minute)
		maintenanceTimer := time.NewTimer(time.Until(s.setNextMaintenanceRun(time.Now())))
		defer modelTicker.Stop()
		defer syncTicker.Stop()
		defer retryTicker.Stop()
		defer maintenanceTimer.Stop()
		for {
			select {
			case <-modelTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				_, _ = s.RefreshModels(ctx)
				cancel()
			case <-maintenanceTimer.C:
				now := time.Now()
				_, _ = s.StartProbeJob(context.Background(), nil, "scheduled", "daily-"+now.Format("2006-01-02"))
				maintenanceTimer.Reset(time.Until(s.setNextMaintenanceRun(now.Add(time.Minute))))
			case <-retryTicker.C:
				s.startRetryProbes()
			case <-syncTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				cleanup, err := s.CleanupExpiredUploadedSubscriptions(ctx)
				if err != nil {
					slog.Warn("opencode_expired_source_cleanup_failed", "error", err)
				} else if cleanup.Subscriptions > 0 {
					if _, err := s.ReconcileWorkers(ctx); err != nil {
						slog.Warn("opencode_expired_source_reconcile_failed", "error", err)
					}
				}
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
		if !subscription.Enabled || subscription.SourceType != "url" {
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
		if subscriptions[i].SourceType == "upload" {
			subscriptions[i].URLMasked = "uploaded file"
			continue
		}
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
	return s.repo.CreateSubscription(ctx, ProxySubscription{Name: name, URLCiphertext: ciphertext, Enabled: enabled, SyncIntervalMinutes: interval, HasURL: true, URLMasked: maskSubscriptionURL(rawURL), SourceType: "url"})
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
		if existing.SourceType == "upload" {
			return nil, errors.New("uploaded proxy sources do not have a subscription URL")
		}
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
		if _, err := s.ReconcileWorkers(ctx); err != nil {
			return nil, fmt.Errorf("reconcile OpenCode workers after subscription update: %w", err)
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
	if err := s.reloadPersistedMihomo(ctx); err != nil {
		return err
	}
	_, err := s.ReconcileWorkers(ctx)
	return err
}

func (s *OpenCodeProxyPoolService) reloadPersistedMihomo(ctx context.Context) error {
	s.mihomoMu.Lock()
	defer s.mihomoMu.Unlock()
	drafts, err := s.mergeEnabledSubscriptionDrafts(ctx, -1, nil)
	if err != nil {
		return err
	}
	return s.reloadMihomoLocked(ctx, drafts)
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("invalid share link")
	}
	// HTTP proxy lists commonly omit the URL scheme and use either
	// username:password@host:port or host:port. Treat those entries as HTTP
	// proxies while keeping explicit share-link protocols unchanged.
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
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
	tombstones, err := s.repo.ListTombstonedNodeKeys(ctx, id)
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
		if _, deleted := tombstones[key]; deleted {
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
		if protocol == "http" || protocol == "https" {
			node.TransportMode = "direct_http"
			node.ListenerPort = 0
			node.ListenerUsername = strings.TrimSpace(fmt.Sprint(proxyConfig["username"]))
			node.ListenerPassword = strings.TrimSpace(fmt.Sprint(proxyConfig["password"]))
		} else {
			node.TransportMode = "mihomo_listener"
		}
		node.ConfigCiphertext = ciphertext
		node.SyncStatus = "active"
		if node.MihomoName == "" {
			node.MihomoName = fmt.Sprintf("oc-%d-%03d-%s", id, index+1, key[:8])
		}
		proxyConfig["name"] = node.MihomoName
		if node.TransportMode == "mihomo_listener" && node.ListenerPort == 0 {
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
	s.mihomoMu.Lock()
	defer s.mihomoMu.Unlock()
	if err := s.reloadMihomoLocked(ctx, drafts); err != nil {
		if len(previous) > 0 {
			if restoreErr := s.restoreMihomoConfigLocked(ctx, previous); restoreErr != nil {
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

func (s *OpenCodeProxyPoolService) reloadMihomoLocked(ctx context.Context, drafts []ManagedProxyNodeDraft) error {
	proxies := make([]map[string]any, 0, len(drafts))
	listeners := make([]map[string]any, 0, len(drafts))
	for _, draft := range drafts {
		if draft.TransportMode == "direct_http" {
			continue
		}
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
	s.mihomoMu.Lock()
	defer s.mihomoMu.Unlock()
	return s.restoreMihomoConfigLocked(ctx, previous)
}

func (s *OpenCodeProxyPoolService) restoreMihomoConfigLocked(ctx context.Context, previous []byte) error {
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
	return s.probeNodes(ctx, ids, 3)
}

func (s *OpenCodeProxyPoolService) probeNodes(ctx context.Context, ids []int64, concurrency int) ([]OpenCodeNodeProbeResult, error) {
	return s.probeNodesWithProgress(ctx, ids, concurrency, nil)
}

func (s *OpenCodeProxyPoolService) probeNodesWithProgress(ctx context.Context, ids []int64, concurrency int, progress func(int)) ([]OpenCodeNodeProbeResult, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	nodes, err := s.repo.ListNodes(ctx, nil)
	if err != nil {
		return nil, err
	}
	selected := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	enabledSubscriptions := make(map[int64]struct{})
	if len(selected) == 0 {
		subscriptions, listErr := s.repo.ListSubscriptions(ctx)
		if listErr != nil {
			return nil, listErr
		}
		for _, subscription := range subscriptions {
			if subscription.Enabled {
				enabledSubscriptions[subscription.ID] = struct{}{}
			}
		}
	}
	results := make([]OpenCodeNodeProbeResult, 0, len(nodes))
	if concurrency <= 0 {
		concurrency = 3
	}
	sem := make(chan struct{}, concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, node := range nodes {
		if node.SyncStatus != "active" {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[node.ID]; !ok {
				continue
			}
		} else if _, ok := enabledSubscriptions[node.SubscriptionID]; !ok {
			continue
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
			processed := len(results)
			mu.Unlock()
			if progress != nil {
				progress(processed)
			}
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].NodeID < results[j].NodeID })
	if len(results) >= 20 {
		exitProbeFailures := 0
		for i := range results {
			if results[i].FailureType == "exit_probe" {
				exitProbeFailures++
			}
		}
		if exitProbeFailures*100/len(results) >= 80 {
			for i := range results {
				if results[i].FailureType == "exit_probe" {
					results[i].HealthStatus = "probe_infrastructure_error"
					results[i].FailureType = "probe_infrastructure"
				}
			}
		}
	}
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
	markDuplicateOpenCodeExits(results, existingExitOwners)
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
	deletedDuplicates, deleteErr := s.deleteDuplicateProbeResults(ctx, results)
	var reloadErr error
	if deletedDuplicates > 0 {
		reloadErr = s.reloadAfterManagedNodeDeletion(ctx)
	}
	_, reconcileErr := s.ReconcileWorkers(ctx)
	if err := errors.Join(deleteErr, reloadErr, reconcileErr); err != nil {
		return nil, fmt.Errorf("finalize OpenCode probe: %w", err)
	}
	return results, nil
}

func markDuplicateOpenCodeExits(results []OpenCodeNodeProbeResult, existingExitOwners map[string]int64) {
	canonical := make(map[string]int)
	for i := range results {
		result := &results[i]
		if !result.Success || result.ExitIP == "" {
			continue
		}
		if _, exists := existingExitOwners[result.ExitIP]; exists {
			continue
		}
		if winner, exists := canonical[result.ExitIP]; !exists || result.LatencyMs < results[winner].LatencyMs {
			canonical[result.ExitIP] = i
		}
	}
	for i := range results {
		result := &results[i]
		if !result.Success || result.ExitIP == "" {
			continue
		}
		ownerID, hasExistingOwner := existingExitOwners[result.ExitIP]
		if !hasExistingOwner {
			winner, exists := canonical[result.ExitIP]
			if !exists || winner == i {
				continue
			}
			ownerID = results[winner].NodeID
		}
		result.Success = false
		result.HealthStatus = "duplicate_exit"
		result.FailureType = "duplicate_exit"
		result.DuplicateOfNodeID = &ownerID
	}
}

func (s *OpenCodeProxyPoolService) deleteDuplicateProbeResults(ctx context.Context, results []OpenCodeNodeProbeResult) (int, error) {
	seen := make(map[int64]struct{})
	deleted := 0
	var cleanupErr error
	for _, result := range results {
		if result.NodeID <= 0 || result.HealthStatus != "duplicate_exit" || result.DuplicateOfNodeID == nil {
			continue
		}
		if *result.DuplicateOfNodeID <= 0 || *result.DuplicateOfNodeID == result.NodeID {
			continue
		}
		if _, exists := seen[result.NodeID]; exists {
			continue
		}
		seen[result.NodeID] = struct{}{}
		if err := s.repo.DeleteManagedNode(ctx, result.NodeID, "duplicate_exit"); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete duplicate OpenCode node %d: %w", result.NodeID, err))
			continue
		}
		deleted++
	}
	return deleted, cleanupErr
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
			return s.repo.AcquireEgressLease(ctx, account.ID, nil, mode, result.ExitIP, account.GetOpenCodeLane(), time.Now().UTC())
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
		return s.repo.AcquireEgressLease(ctx, account.ID, account.ProxyID, mode, selected.ExitIP, account.GetOpenCodeLane(), time.Now().UTC())
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
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: openCodeProbeTimeout}
	start := time.Now()
	exitIP, err := probeOpenCodeExitIP(ctx, client)
	if err != nil {
		result.FailureType, result.FailureMessage = "exit_probe", err.Error()
		return result
	}
	result.ExitIP = exitIP
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

func sanitizeOpenCodeProbeFailure(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "***")
		}
	}
	return message
}

func (s *OpenCodeProxyPoolService) probeNode(ctx context.Context, node ManagedProxyNode) OpenCodeNodeProbeResult {
	result := OpenCodeNodeProbeResult{NodeID: node.ID, HealthStatus: "transport_error"}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", s.proxyHost, node.ListenerPort), User: url.UserPassword(node.ListenerUsername, node.ListenerPassword)}
	if node.TransportMode == "direct_http" {
		proxyURL = &url.URL{Scheme: node.ProxyProtocol, Host: net.JoinHostPort(node.ProxyHost, strconv.Itoa(node.ProxyPort))}
		if node.ListenerUsername != "" {
			proxyURL.User = url.UserPassword(node.ListenerUsername, node.ListenerPassword)
		}
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: openCodeProbeTimeout}
	start := time.Now()
	exitIP, err := probeOpenCodeExitIP(ctx, client)
	if err != nil {
		result.FailureType, result.FailureMessage = "exit_probe", err.Error()
		return result
	}
	result.ExitIP = exitIP
	populateOpenCodeGeo(ctx, client, &result)
	body := []byte(`{"model":"big-pickle","messages":[{"role":"user","content":"Reply OK"}],"stream":false,"max_tokens":8}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, OpenCodeZenBaseURL+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "opencode-cli/1.0.0")
	resp, err := client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.FailureType = classifyProbeError(err)
		result.FailureMessage = sanitizeOpenCodeProbeFailure(err.Error(), node.ListenerUsername, node.ListenerPassword)
		return result
	}
	result.OpenCodeHTTPStatus = resp.StatusCode
	result.RetryAt = openCodeRetryAt(resp, time.Now())
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		result.Success, result.HealthStatus = true, "healthy"
	case http.StatusTooManyRequests:
		result.HealthStatus, result.FailureType = "rate_limited", "rate_limited"
	case http.StatusUnauthorized, http.StatusForbidden:
		result.HealthStatus, result.FailureType = "auth_error", "auth"
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		result.HealthStatus, result.FailureType = "upstream_error", "upstream_error"
	default:
		result.HealthStatus, result.FailureType = "http_error", "http"
	}
	return result
}

func probeOpenCodeExitIP(ctx context.Context, client *http.Client) (string, error) {
	endpoints := []struct {
		url  string
		json bool
	}{
		{url: "https://api64.ipify.org?format=json", json: true},
		{url: "https://api.ip.sb/ip", json: false},
	}
	var failures []string
	for _, endpoint := range endpoints {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
		resp, err := client.Do(req)
		if err != nil {
			failures = append(failures, classifyProbeError(err))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			failures = append(failures, fmt.Sprintf("HTTP %d", resp.StatusCode))
			continue
		}
		value := strings.TrimSpace(string(body))
		if endpoint.json {
			var payload struct {
				IP string `json:"ip"`
			}
			if json.Unmarshal(body, &payload) != nil {
				failures = append(failures, "invalid JSON")
				continue
			}
			value = strings.TrimSpace(payload.IP)
		}
		addr, parseErr := netip.ParseAddr(value)
		if parseErr == nil {
			return addr.Unmap().String(), nil
		}
		failures = append(failures, "invalid IP")
	}
	return "", fmt.Errorf("exit probe services failed: %s", strings.Join(failures, ", "))
}

func openCodeRetryAt(resp *http.Response, now time.Time) *time.Time {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	if seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && seconds > 0 {
		value := now.Add(time.Duration(seconds) * time.Second)
		return &value
	}
	if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
		if value, err := http.ParseTime(raw); err == nil && value.After(now) {
			return &value
		}
	}
	for _, name := range []string{"x-ratelimit-reset", "x-ratelimit-reset-requests"} {
		raw := strings.TrimSpace(resp.Header.Get(name))
		if raw == "" {
			continue
		}
		if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
			value := time.Unix(epoch, 0)
			if value.After(now) {
				return &value
			}
		}
	}
	return nil
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
	snapshot.UsingBaseline = false
	snapshot.Source = "snapshot"
	s.modelMu.Lock()
	s.modelStatus = snapshot
	s.modelMu.Unlock()
	return nil
}

func (s *OpenCodeProxyPoolService) fetchOpenCodeCatalog(ctx context.Context, endpoint string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sub2api-opencode/1.0")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", endpoint, err)
	}
	return body, nil
}

// parseOpenCodeLiveModelIDs 解析 /zen/v1/models 返回的标准 OpenAI 目录响应。
func parseOpenCodeLiveModelIDs(data []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode zen model catalog: %w", err)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if normalized := normalizeOpenCodeModelID(item.ID); normalized != "" {
			ids = append(ids, normalized)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("zen model catalog is empty")
	}
	sort.Strings(ids)
	return ids, nil
}

// RefreshModels 用两个上游目录的交集重建免费模型注册表。
//
// 来源 A（openCodeZenModelsURL，不带 Authorization，与 probeNode 一致）决定「可路由」，
// 来源 B（models.dev）决定「零成本」。旧实现抓 docs 页 HTML 表格的展示名列再 slug 化，
// 而同一展示名在 Zen 与 Go 两个 tier 下对应不同 ID，于是把 Go tier 的 ox-alpha-free
// 写进了 Zen 注册表；请求它返回 401 ModelError，被当成凭据失效连锁禁用了 11 个账号。
// 交集规则从构造上排除这类跨 tier 串味：ox-alpha-free 不在来源 A 也不在来源 B。
//
// 任一来源失败或交集为空时保留旧集合并只记 LastError——宁可发一份陈旧列表，
// 也好过让注册表清空导致所有模型 403。
func (s *OpenCodeProxyPoolService) RefreshModels(ctx context.Context) (*OpenCodeModelRegistryStatus, error) {
	liveBody, err := s.fetchOpenCodeCatalog(ctx, openCodeZenModelsURL, openCodeZenModelsMaxBytes)
	if err != nil {
		return s.recordModelError(err)
	}
	liveIDs, err := parseOpenCodeLiveModelIDs(liveBody)
	if err != nil {
		return s.recordModelError(err)
	}
	catalogBody, err := s.fetchOpenCodeCatalog(ctx, openCodeModelsDevURL, openCodeModelsDevMaxBytes)
	if err != nil {
		return s.recordModelError(err)
	}
	zeroCostIDs, err := parseOpenCodeZeroCostModelIDs(catalogBody)
	if err != nil {
		return s.recordModelError(err)
	}

	ids := intersectOpenCodeModelIDs(liveIDs, zeroCostIDs)
	if len(ids) == 0 {
		// 两个来源都健康却无交集，需要人工介入，不是普通抓取失败。
		slog.Error("opencode_free_model_intersection_empty", "live_count", len(liveIDs), "zero_cost_count", len(zeroCostIDs))
		return s.recordModelError(errors.New("no zero-cost model is routable in the live OpenCode Zen catalog"))
	}

	previous := defaultOpenCodeFreeModels.IDs()
	added, removed := defaultOpenCodeFreeModels.ReplaceStrict(ids)
	now := time.Now().UTC()
	status := OpenCodeModelRegistryStatus{
		IDs:           ids,
		Count:         len(ids),
		LastFetchedAt: &now,
		Source:        "live",
		LiveIDs:       liveIDs,
		ZeroCostIDs:   zeroCostIDs,
		PreviousIDs:   previous,
		Added:         added,
		Removed:       removed,
	}
	s.modelMu.Lock()
	s.modelStatus = status
	s.modelMu.Unlock()
	if len(added)+len(removed) > 0 {
		slog.Warn("opencode_free_model_registry_changed",
			"added", added, "removed", removed, "count", len(ids), "previous_count", len(previous))
	}
	if s.settingRepo != nil {
		encoded, _ := json.Marshal(status)
		if err := s.settingRepo.Set(ctx, OpenCodeModelSnapshotSettingKey, string(encoded)); err != nil {
			slog.Warn("opencode_model_snapshot_persist_failed", "error", err)
		}
	}
	if pool := s.cachedOpenCodePool(); pool != nil {
		s.invalidateBoundAPIKeys(ctx, pool.ID)
	}
	return &status, nil
}

func (s *OpenCodeProxyPoolService) recordModelError(err error) (*OpenCodeModelRegistryStatus, error) {
	s.modelMu.Lock()
	s.modelStatus.LastError = err.Error()
	status := cloneOpenCodeModelRegistryStatus(s.modelStatus)
	s.modelMu.Unlock()
	return &status, err
}

func (s *OpenCodeProxyPoolService) ModelStatus() OpenCodeModelRegistryStatus {
	s.modelMu.RLock()
	status := cloneOpenCodeModelRegistryStatus(s.modelStatus)
	s.modelMu.RUnlock()
	return status
}

func cloneOpenCodeModelRegistryStatus(status OpenCodeModelRegistryStatus) OpenCodeModelRegistryStatus {
	status.IDs = append([]string(nil), status.IDs...)
	status.LiveIDs = append([]string(nil), status.LiveIDs...)
	status.ZeroCostIDs = append([]string(nil), status.ZeroCostIDs...)
	status.PreviousIDs = append([]string(nil), status.PreviousIDs...)
	status.Added = append([]string(nil), status.Added...)
	status.Removed = append([]string(nil), status.Removed...)
	if status.LastFetchedAt != nil {
		fetchedAt := *status.LastFetchedAt
		status.LastFetchedAt = &fetchedAt
	}
	return status
}
