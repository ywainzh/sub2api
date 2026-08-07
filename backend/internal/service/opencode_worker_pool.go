package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *OpenCodeProxyPoolService) hydrateOpenCodePool(ctx context.Context, pool *OpenCodePool) (*OpenCodePool, error) {
	if pool == nil {
		return nil, errors.New("OpenCode pool is nil")
	}
	if s.groupRepo != nil {
		group, err := s.groupRepo.GetByID(ctx, pool.GroupID)
		if err != nil {
			return nil, fmt.Errorf("load OpenCode system group: %w", err)
		}
		pool.Group = group
	}
	return pool, nil
}

func cloneOpenCodePool(pool *OpenCodePool) *OpenCodePool {
	if pool == nil {
		return nil
	}
	clone := *pool
	if pool.Group != nil {
		groupClone := *pool.Group
		clone.Group = &groupClone
	}
	return &clone
}

func (s *OpenCodeProxyPoolService) cacheOpenCodePool(pool *OpenCodePool) {
	if s == nil {
		return
	}
	s.poolMu.Lock()
	s.poolStatus = cloneOpenCodePool(pool)
	s.poolMu.Unlock()
}

func (s *OpenCodeProxyPoolService) cachedOpenCodePool() *OpenCodePool {
	if s == nil {
		return nil
	}
	s.poolMu.RLock()
	pool := cloneOpenCodePool(s.poolStatus)
	s.poolMu.RUnlock()
	return pool
}

func (s *OpenCodeProxyPoolService) BootstrapPool(ctx context.Context) (*OpenCodePool, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("OpenCode pool repository is not configured")
	}
	pool, err := s.repo.EnsureDefaultPool(ctx)
	if err != nil {
		return nil, err
	}
	pool, err = s.hydrateOpenCodePool(ctx, pool)
	if err != nil {
		return nil, err
	}
	s.cacheOpenCodePool(pool)
	return cloneOpenCodePool(pool), nil
}

func (s *OpenCodeProxyPoolService) GetPool(ctx context.Context) (*OpenCodePool, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("OpenCode pool repository is not configured")
	}
	pool, err := s.repo.GetOpenCodePool(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return s.BootstrapPool(ctx)
	}
	if err != nil {
		return nil, err
	}
	pool, err = s.hydrateOpenCodePool(ctx, pool)
	if err != nil {
		return nil, err
	}
	s.cacheOpenCodePool(pool)
	return cloneOpenCodePool(pool), nil
}

func (s *OpenCodeProxyPoolService) invalidateBoundAPIKeys(ctx context.Context, poolID int64) {
	if s == nil || s.repo == nil || s.authCacheInvalidator == nil || poolID <= 0 {
		return
	}
	userIDs, err := s.repo.ListOpenCodeBoundUserIDs(ctx, poolID)
	if err != nil {
		return
	}
	for _, userID := range userIDs {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

func (s *OpenCodeProxyPoolService) UpdatePool(ctx context.Context, update OpenCodePoolUpdate) (*OpenCodePool, error) {
	pool, err := s.GetPool(ctx)
	if err != nil {
		return nil, err
	}
	if update.Enabled != nil {
		pool.Enabled = *update.Enabled
	}
	if update.IncludeServerDirect != nil {
		pool.IncludeServerDirect = *update.IncludeServerDirect
	}
	if update.WorkerConcurrency != nil {
		if *update.WorkerConcurrency < 1 || *update.WorkerConcurrency > 100 {
			return nil, errors.New("worker_concurrency must be between 1 and 100")
		}
		pool.WorkerConcurrency = *update.WorkerConcurrency
	}
	if update.ClearUpstreamAPIKey {
		pool.UpstreamKeyCiphertext = ""
	} else if update.UpstreamAPIKey != nil {
		value := strings.TrimSpace(*update.UpstreamAPIKey)
		if value != "" {
			ciphertext, encryptErr := s.encryptor.Encrypt(value)
			if encryptErr != nil {
				return nil, encryptErr
			}
			pool.UpstreamKeyCiphertext = ciphertext
		}
	}
	pool.ReconcileStatus = "pending"
	pool.ReconcileError = ""
	updated, err := s.repo.UpdateOpenCodePool(ctx, *pool)
	if err != nil {
		return nil, err
	}
	updated, err = s.hydrateOpenCodePool(ctx, updated)
	if err != nil {
		return nil, err
	}
	s.cacheOpenCodePool(updated)
	s.invalidateBoundAPIKeys(ctx, updated.ID)
	if _, err := s.ReconcileWorkers(ctx); err != nil {
		return updated, err
	}
	return s.GetPool(ctx)
}

func (s *OpenCodeProxyPoolService) ListWorkers(ctx context.Context) ([]OpenCodePoolWorker, error) {
	pool, err := s.GetPool(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListOpenCodePoolWorkers(ctx, pool.ID)
}

func (s *OpenCodeProxyPoolService) ReconcileWorkers(ctx context.Context) ([]OpenCodePoolWorker, error) {
	pool, err := s.GetPool(ctx)
	if err != nil {
		return nil, err
	}
	upstreamKey := ""
	if strings.TrimSpace(pool.UpstreamKeyCiphertext) != "" {
		upstreamKey, err = s.encryptor.Decrypt(pool.UpstreamKeyCiphertext)
		if err != nil {
			pool.ReconcileStatus = "error"
			pool.ReconcileError = "failed to decrypt upstream key"
			_, _ = s.repo.UpdateOpenCodePool(ctx, *pool)
			return nil, errors.New("failed to decrypt OpenCode upstream key")
		}
	}
	var directProbe *OpenCodeNodeProbeResult
	if pool.Enabled && pool.IncludeServerDirect {
		probe := s.probeDirect(ctx)
		directProbe = &probe
	}
	workers, err := s.repo.ReconcileOpenCodePoolWorkers(ctx, *pool, upstreamKey, directProbe)
	if err != nil {
		pool.ReconcileStatus = "error"
		pool.ReconcileError = truncateOpenCodeStatusError(err.Error())
		_, _ = s.repo.UpdateOpenCodePool(ctx, *pool)
		return nil, err
	}
	refreshed, refreshErr := s.GetPool(ctx)
	if refreshErr == nil {
		s.cacheOpenCodePool(refreshed)
	}
	s.invalidateBoundAPIKeys(ctx, pool.ID)
	return workers, nil
}

func truncateOpenCodeStatusError(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 1000 {
		runes = runes[:1000]
	}
	return string(runes)
}

func (s *OpenCodeProxyPoolService) BindAPIKey(ctx context.Context, apiKeyID int64, boundBy *int64) error {
	pool, err := s.GetPool(ctx)
	if err != nil {
		return err
	}
	mutation, err := s.repo.BindAPIKeyToOpenCodePool(ctx, apiKeyID, pool.ID, boundBy)
	if err != nil {
		return err
	}
	if s.authCacheInvalidator != nil && mutation != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, mutation.UserID)
	}
	return nil
}

func (s *OpenCodeProxyPoolService) UnbindAPIKey(ctx context.Context, apiKeyID int64) error {
	mutation, err := s.repo.UnbindAPIKeyFromOpenCodePool(ctx, apiKeyID)
	if err != nil {
		return err
	}
	if s.authCacheInvalidator != nil && mutation != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, mutation.UserID)
	}
	return nil
}

func (s *OpenCodeProxyPoolService) IsSystemWorkerAccount(ctx context.Context, accountID int64) (bool, error) {
	if s == nil || s.repo == nil {
		return false, nil
	}
	return s.repo.IsOpenCodeSystemWorkerAccount(ctx, accountID)
}

func (s *OpenCodeProxyPoolService) IsSystemGroup(ctx context.Context, groupID int64) (bool, error) {
	if s == nil || s.repo == nil {
		return false, nil
	}
	return s.repo.IsOpenCodeSystemGroup(ctx, groupID)
}

func resolveOpenCodeRegistryModel(model string) string {
	normalized := normalizeOpenCodeModelID(model)
	if alias, ok := openCodeEffortAliases[normalized]; ok {
		return normalizeOpenCodeModelID(alias.model)
	}
	return normalized
}

func (s *OpenCodeProxyPoolService) IsFreeModel(model string) bool {
	return defaultOpenCodeFreeModels.Has(resolveOpenCodeRegistryModel(model))
}

func (s *OpenCodeProxyPoolService) FreeModelIDs() []string {
	return defaultOpenCodeFreeModels.IDs()
}

func (s *OpenCodeProxyPoolService) ResolveEffectiveAPIKey(apiKey *APIKey, model string) (*APIKey, bool, error) {
	if apiKey == nil || apiKey.OpenCodePoolID == nil || *apiKey.OpenCodePoolID <= 0 || !s.IsFreeModel(model) {
		return apiKey, false, nil
	}
	pool := s.cachedOpenCodePool()
	if pool == nil || pool.ID != *apiKey.OpenCodePoolID {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var err error
		pool, err = s.GetPool(ctx)
		if err != nil {
			return nil, true, ErrOpenCodePoolUnavailable
		}
	}
	if !pool.Enabled || pool.ActiveWorkers <= 0 || pool.Group == nil {
		return nil, true, ErrOpenCodePoolUnavailable
	}
	clone := *apiKey
	groupClone := *pool.Group
	clone.GroupID = &groupClone.ID
	clone.Group = &groupClone
	if apiKey.User != nil {
		userClone := *apiKey.User
		userClone.UserGroupRPMOverride = nil
		clone.User = &userClone
	}
	return &clone, true, nil
}

func mergeOpenCodeModelIDs(base, extra []string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, values := range [][]string{base, extra} {
		for _, model := range values {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			key := strings.ToLower(model)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model)
		}
	}
	return out
}

func (s *OpenCodeProxyPoolService) AppendFreeModelsForAPIKey(apiKey *APIKey, models []string) []string {
	if apiKey == nil || apiKey.OpenCodePoolID == nil {
		return models
	}
	pool := s.cachedOpenCodePool()
	if pool == nil || !pool.Enabled || pool.ID != *apiKey.OpenCodePoolID {
		return models
	}
	return mergeOpenCodeModelIDs(models, s.FreeModelIDs())
}

func sortedUniqueInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
