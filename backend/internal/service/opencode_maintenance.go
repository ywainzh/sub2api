package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	openCodeImportMaxNodes = 10000
)

func (s *OpenCodeProxyPoolService) setNextMaintenanceRun(now time.Time) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.Local
	}
	local := now.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
	s.nextMaintenanceMu.Lock()
	s.nextMaintenanceAt = next
	s.nextMaintenanceMu.Unlock()
	return next
}

func (s *OpenCodeProxyPoolService) GetMaintenanceStatus(ctx context.Context) (*OpenCodeMaintenanceStatus, error) {
	latest, err := s.repo.GetLatestMaintenanceJob(ctx)
	if err != nil {
		return nil, err
	}
	s.nextMaintenanceMu.RLock()
	next := s.nextMaintenanceAt
	s.nextMaintenanceMu.RUnlock()
	if next.IsZero() {
		next = s.setNextMaintenanceRun(time.Now())
	}
	return &OpenCodeMaintenanceStatus{NextRunAt: &next, LatestJob: latest}, nil
}

func (s *OpenCodeProxyPoolService) startRetryProbes() {
	s.retryMu.Lock()
	if s.retryRunning {
		s.retryMu.Unlock()
		return
	}
	s.retryRunning = true
	s.retryMu.Unlock()
	go func() {
		defer func() {
			s.retryMu.Lock()
			s.retryRunning = false
			s.retryMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		ids, err := s.repo.ListRetryableNodeIDs(ctx, time.Now(), 100)
		if err == nil && len(ids) > 0 {
			_, _ = s.probeNodes(ctx, ids, 10)
		}
	}()
}

func (s *OpenCodeProxyPoolService) GetMaintenanceJob(ctx context.Context, id int64) (*OpenCodeMaintenanceJob, error) {
	return s.repo.GetMaintenanceJob(ctx, id)
}

func (s *OpenCodeProxyPoolService) StartProbeJob(ctx context.Context, ids []int64, triggerType, scheduleKey string) (*OpenCodeMaintenanceJob, error) {
	total := len(ids)
	if total == 0 {
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
		nodes, err := s.repo.ListNodes(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			_, sourceEnabled := enabled[node.SubscriptionID]
			if node.SyncStatus == "active" && sourceEnabled {
				total++
			}
		}
	}
	job, err := s.repo.CreateMaintenanceJob(ctx, OpenCodeMaintenanceJob{
		JobType: "probe", TriggerType: triggerType, Status: "pending", TotalNodes: total,
	}, scheduleKey)
	if err != nil {
		return nil, err
	}
	if job.Status != "pending" {
		return job, nil
	}
	go s.runProbeJob(job, ids)
	return job, nil
}

func (s *OpenCodeProxyPoolService) runProbeJob(job *OpenCodeMaintenanceJob, ids []int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	claimed, err := s.repo.ClaimMaintenanceJob(ctx, job.ID)
	if err != nil || !claimed {
		return
	}
	now := time.Now()
	job.Status, job.StartedAt = "running", &now
	var progressMu sync.Mutex
	lastPersisted := 0
	results, err := s.probeNodesWithProgress(ctx, ids, 10, func(processed int) {
		progressMu.Lock()
		defer progressMu.Unlock()
		if processed-lastPersisted < 25 && processed < job.TotalNodes {
			return
		}
		job.ProcessedNodes = processed
		lastPersisted = processed
		_ = s.repo.UpdateMaintenanceJob(context.Background(), *job)
	})
	job.ProcessedNodes = len(results)
	for _, result := range results {
		switch result.HealthStatus {
		case "healthy":
			job.HealthyNodes++
		case "rate_limited":
			job.RateLimitedNodes++
		case "duplicate_exit":
			job.DuplicateNodes++
		default:
			job.FailedNodes++
		}
	}
	finished := time.Now()
	job.FinishedAt = &finished
	if err != nil {
		job.Status, job.ErrorMessage = "failed", err.Error()
	} else {
		job.Status = "completed"
	}
	_ = s.repo.UpdateMaintenanceJob(context.Background(), *job)
}

func (s *OpenCodeProxyPoolService) StartProxyImport(ctx context.Context, sourceName string, data []byte, expiresAt *time.Time) (*OpenCodeMaintenanceJob, error) {
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil, errors.New("proxy import file is empty")
	}
	if len(fields) > openCodeImportMaxNodes {
		return nil, fmt.Errorf("proxy import exceeds %d nodes", openCodeImportMaxNodes)
	}
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		sourceName = "imported-proxies"
	}
	if runes := []rune(sourceName); len(runes) > 100 {
		sourceName = string(runes[:100])
	}
	if expiresAt != nil {
		normalized := expiresAt.UTC()
		if !normalized.After(time.Now()) {
			return nil, errors.New("proxy import expiration must be in the future")
		}
		if normalized.After(time.Now().AddDate(10, 0, 0)) {
			return nil, errors.New("proxy import expiration must not exceed 10 years")
		}
		expiresAt = &normalized
	}
	job, err := s.repo.CreateMaintenanceJob(ctx, OpenCodeMaintenanceJob{
		JobType: "import", TriggerType: "manual", Status: "pending",
		SourceName: sourceName, TotalNodes: len(fields),
	}, "")
	if err != nil {
		return nil, err
	}
	copyData := append([]byte(nil), data...)
	go s.runProxyImport(job, copyData, expiresAt)
	return job, nil
}

func (s *OpenCodeProxyPoolService) runProxyImport(job *OpenCodeMaintenanceJob, data []byte, expiresAt *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	claimed, claimErr := s.repo.ClaimMaintenanceJob(ctx, job.ID)
	if claimErr != nil || !claimed {
		return
	}
	now := time.Now()
	job.Status, job.StartedAt = "running", &now
	fail := func(err error) {
		finished := time.Now()
		job.Status, job.ErrorMessage, job.FinishedAt = "failed", err.Error(), &finished
		_ = s.repo.UpdateMaintenanceJob(context.Background(), *job)
	}

	subscription, err := s.repo.CreateUploadedSubscription(ctx, job.SourceName, expiresAt)
	if err != nil {
		fail(err)
		return
	}
	job.SubscriptionID = &subscription.ID
	seen := make(map[string]struct{})
	drafts := make([]ManagedProxyNodeDraft, 0, job.TotalNodes)
	for index, raw := range strings.Fields(string(data)) {
		config, parseErr := openCodeShareLinkToMihomo(raw)
		if parseErr != nil {
			job.FailedNodes++
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(fmt.Sprint(config["type"])))
		if protocol != "http" && protocol != "https" {
			job.FailedNodes++
			continue
		}
		key, canonical, keyErr := nodeFingerprint(config)
		if keyErr != nil {
			job.FailedNodes++
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			job.FailedNodes++
			continue
		}
		seen[key] = struct{}{}
		ciphertext, encryptErr := s.encryptor.Encrypt(string(canonical))
		if encryptErr != nil {
			fail(encryptErr)
			return
		}
		name := strings.TrimSpace(fmt.Sprint(config["name"]))
		node := ManagedProxyNode{
			SubscriptionID: subscription.ID, NodeKey: key, DisplayName: name,
			MihomoName: fmt.Sprintf("oc-upload-%d-%05d-%s", subscription.ID, index+1, key[:8]),
			Protocol:   protocol, TransportMode: "direct_http", SyncStatus: "active",
			ConfigCiphertext: ciphertext,
		}
		drafts = append(drafts, ManagedProxyNodeDraft{ManagedProxyNode: node, ProxyConfig: config})
	}
	if len(drafts) == 0 {
		fail(errors.New("import contains no valid HTTP proxies"))
		return
	}
	if err := s.repo.CommitUploadedNodes(ctx, subscription.ID, drafts); err != nil {
		fail(err)
		return
	}
	nodes, err := s.repo.ListNodes(ctx, &subscription.ID)
	if err != nil {
		fail(err)
		return
	}
	parseFailures := job.FailedNodes
	var progressMu sync.Mutex
	lastPersisted := 0
	results, probeErr := s.probeNodesWithProgress(ctx, nodeIDs(nodes), 10, func(processed int) {
		progressMu.Lock()
		defer progressMu.Unlock()
		processed += parseFailures
		if processed-lastPersisted < 25 && processed < job.TotalNodes {
			return
		}
		job.ProcessedNodes = processed
		lastPersisted = processed
		_ = s.repo.UpdateMaintenanceJob(context.Background(), *job)
	})
	for _, result := range results {
		switch result.HealthStatus {
		case "healthy":
			job.HealthyNodes++
		case "rate_limited":
			job.RateLimitedNodes++
		case "duplicate_exit":
			job.DuplicateNodes++
		default:
			job.FailedNodes++
		}
	}
	job.ProcessedNodes = len(results) + parseFailures
	finished := time.Now()
	job.FinishedAt = &finished
	if probeErr != nil {
		job.Status, job.ErrorMessage = "failed", probeErr.Error()
	} else {
		job.Status = "completed"
	}
	_ = s.repo.UpdateMaintenanceJob(context.Background(), *job)
}

func (s *OpenCodeProxyPoolService) CleanupExpiredUploadedSubscriptions(ctx context.Context) (ExpiredProxySourceCleanupResult, error) {
	result, err := s.repo.DeleteExpiredUploadedSubscriptions(ctx, time.Now())
	if err != nil || result.Subscriptions == 0 {
		return result, err
	}
	slog.Info("opencode_uploaded_proxy_sources_expired",
		"subscriptions", result.Subscriptions,
		"nodes", result.Nodes,
		"accounts", result.Accounts,
	)
	return result, nil
}

func (s *OpenCodeProxyPoolService) DeleteManagedNode(ctx context.Context, nodeID int64) error {
	if err := s.repo.DeleteManagedNode(ctx, nodeID, "manual"); err != nil {
		return err
	}
	drafts, err := s.mergeEnabledSubscriptionDrafts(ctx, 0, nil)
	if err != nil {
		return err
	}
	previous, _ := os.ReadFile(filepath.Join(s.configDir, "config.yaml"))
	if err := s.reloadMihomoWithRollback(ctx, drafts, previous); err != nil {
		return err
	}
	_, err = s.ReconcileWorkers(ctx)
	return err
}
