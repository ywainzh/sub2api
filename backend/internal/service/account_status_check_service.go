package service

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	accountStatusCheckWorkerCount  = 5
	accountStatusCheckDefaultModel = "gpt-5.5"
)

var accountStatusCheckModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

type AccountStatusCheckCategory string

const (
	AccountStatusCheckNormal         AccountStatusCheckCategory = "normal"
	AccountStatusCheckUnauthorized   AccountStatusCheckCategory = "unauthorized"
	AccountStatusCheckQuotaExhausted AccountStatusCheckCategory = "quota_exhausted"
	AccountStatusCheckForbidden      AccountStatusCheckCategory = "forbidden"
	AccountStatusCheckOtherError     AccountStatusCheckCategory = "other_error"
)

type AccountStatusCheckRequest struct {
	GroupID int64  `json:"group_id"`
	ModelID string `json:"model_id"`
	Mode    string `json:"mode"`
}

type AccountStatusCheckStats struct {
	Total          int `json:"total"`
	Completed      int `json:"completed"`
	Normal         int `json:"normal"`
	Unauthorized   int `json:"unauthorized"`
	QuotaExhausted int `json:"quota_exhausted"`
	Forbidden      int `json:"forbidden"`
	OtherError     int `json:"other_error"`
}

func (s *AccountStatusCheckStats) add(category AccountStatusCheckCategory) {
	s.Completed++
	switch category {
	case AccountStatusCheckNormal:
		s.Normal++
	case AccountStatusCheckUnauthorized:
		s.Unauthorized++
	case AccountStatusCheckQuotaExhausted:
		s.QuotaExhausted++
	case AccountStatusCheckForbidden:
		s.Forbidden++
	default:
		s.OtherError++
	}
}

type AccountStatusCheckEvent struct {
	Type            string                     `json:"type"`
	GroupID         int64                      `json:"group_id,omitempty"`
	GroupName       string                     `json:"group_name,omitempty"`
	ModelID         string                     `json:"model_id,omitempty"`
	Mode            string                     `json:"mode,omitempty"`
	AccountID       int64                      `json:"account_id,omitempty"`
	AccountName     string                     `json:"account_name,omitempty"`
	AccountPriority int                        `json:"account_priority,omitempty"`
	LogType         string                     `json:"log_type,omitempty"`
	Text            string                     `json:"text,omitempty"`
	Category        AccountStatusCheckCategory `json:"category,omitempty"`
	HTTPStatus      int                        `json:"http_status,omitempty"`
	Error           string                     `json:"error,omitempty"`
	LatencyMs       int64                      `json:"latency_ms,omitempty"`
	Consecutive403  int64                      `json:"consecutive_403,omitempty"`
	Threshold403    int                        `json:"threshold_403,omitempty"`
	Total           int                        `json:"total,omitempty"`
	Completed       int                        `json:"completed,omitempty"`
	Canceled        bool                       `json:"canceled,omitempty"`
	Stats           *AccountStatusCheckStats   `json:"stats,omitempty"`
}

type AccountStatusCheckEmitter func(AccountStatusCheckEvent) error

type accountStatusCheckSource interface {
	GetGroup(ctx context.Context, id int64) (*Group, error)
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, error)
}

type accountStatusCheckTester interface {
	RunAccountTest(ctx context.Context, accountID int64, modelID string, mode string, sink AccountTestEventSink) AccountTestRunResult
}

type accountStatusCheckStateManager interface {
	RecoverAccountAfterSuccessfulTest(ctx context.Context, accountID int64) (*SuccessfulTestRecoveryResult, error)
	HandleOpenAI403ForAccountTest(ctx context.Context, account *Account, responseBody []byte) OpenAI403TestResult
}

// AccountStatusCheckService coordinates ephemeral group-wide account tests.
// It intentionally keeps no history and does not create scheduled-test plans.
type AccountStatusCheckService struct {
	source       accountStatusCheckSource
	tester       accountStatusCheckTester
	stateManager accountStatusCheckStateManager

	runningMu sync.Mutex
	running   map[int64]struct{}
}

func NewAccountStatusCheckService(
	adminService AdminService,
	accountTestService *AccountTestService,
	rateLimitService *RateLimitService,
) *AccountStatusCheckService {
	service := &AccountStatusCheckService{
		source:  adminService,
		running: make(map[int64]struct{}),
	}
	if accountTestService != nil {
		service.tester = accountTestService
	}
	if rateLimitService != nil {
		service.stateManager = rateLimitService
	}
	return service
}

// AccountStatusCheckTask owns a same-group execution slot from Start until
// Run/Close completes.
type AccountStatusCheckTask struct {
	service   *AccountStatusCheckService
	group     *Group
	accounts  []Account
	modelID   string
	mode      string
	closeOnce sync.Once
}

func (s *AccountStatusCheckService) Start(ctx context.Context, request AccountStatusCheckRequest) (*AccountStatusCheckTask, error) {
	if s == nil || s.source == nil || s.tester == nil {
		return nil, infraerrors.ServiceUnavailable("ACCOUNT_STATUS_CHECK_UNAVAILABLE", "account status check service is unavailable")
	}
	if request.GroupID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_GROUP_ID", "group_id must be greater than zero")
	}

	modelID := strings.TrimSpace(request.ModelID)
	if modelID == "" {
		modelID = accountStatusCheckDefaultModel
	}
	if !accountStatusCheckModelPattern.MatchString(modelID) {
		return nil, infraerrors.BadRequest("INVALID_MODEL_ID", "model_id is invalid")
	}

	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = AccountTestModeDefault
	}
	if mode != AccountTestModeDefault && mode != AccountTestModeCompact {
		return nil, infraerrors.BadRequest("INVALID_TEST_MODE", "mode must be default or compact")
	}

	group, err := s.source.GetGroup(ctx, request.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil {
		return nil, infraerrors.NotFound("GROUP_NOT_FOUND", "group not found")
	}
	if group.Platform != PlatformOpenAI {
		return nil, infraerrors.BadRequest("UNSUPPORTED_GROUP_PLATFORM", "status check currently supports OpenAI groups only")
	}

	s.runningMu.Lock()
	if s.running == nil {
		s.running = make(map[int64]struct{})
	}
	if _, exists := s.running[request.GroupID]; exists {
		s.runningMu.Unlock()
		return nil, infraerrors.Conflict("ACCOUNT_STATUS_CHECK_RUNNING", "a status check is already running for this group")
	}
	s.running[request.GroupID] = struct{}{}
	s.runningMu.Unlock()

	release := true
	defer func() {
		if release {
			s.release(request.GroupID)
		}
	}()

	// Empty status and platform filters intentionally include active, inactive,
	// error, rate-limited, unschedulable, and mixed-platform rows. Ent's
	// soft-delete interceptor still excludes deleted accounts.
	accounts, err := s.source.ListAccountsForSchedulerScoreFilter(ctx, "", "", "", "", request.GroupID, "")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})

	release = false
	return &AccountStatusCheckTask{
		service:  s,
		group:    group,
		accounts: accounts,
		modelID:  modelID,
		mode:     mode,
	}, nil
}

func (s *AccountStatusCheckService) release(groupID int64) {
	if s == nil {
		return
	}
	s.runningMu.Lock()
	delete(s.running, groupID)
	s.runningMu.Unlock()
}

func (t *AccountStatusCheckTask) Close() {
	if t == nil || t.service == nil || t.group == nil {
		return
	}
	t.closeOnce.Do(func() { t.service.release(t.group.ID) })
}

type accountStatusCheckWorkerResult struct {
	account        Account
	category       AccountStatusCheckCategory
	httpStatus     int
	errorMessage   string
	latencyMs      int64
	consecutive403 int64
	threshold403   int
	canceled       bool
}

func (t *AccountStatusCheckTask) Run(ctx context.Context, emit AccountStatusCheckEmitter) error {
	if t == nil || t.service == nil || t.group == nil || emit == nil {
		return infraerrors.ServiceUnavailable("ACCOUNT_STATUS_CHECK_UNAVAILABLE", "account status check task is unavailable")
	}
	defer t.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var emitMu sync.Mutex
	emitEvent := func(event AccountStatusCheckEvent) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		if err := emit(event); err != nil {
			cancel()
			return err
		}
		return nil
	}

	stats := AccountStatusCheckStats{Total: len(t.accounts)}
	if err := emitEvent(AccountStatusCheckEvent{
		Type:      "batch_start",
		GroupID:   t.group.ID,
		GroupName: t.group.Name,
		ModelID:   t.modelID,
		Mode:      t.mode,
		Total:     stats.Total,
		Stats:     cloneAccountStatusCheckStats(stats),
	}); err != nil {
		return err
	}

	if len(t.accounts) == 0 {
		return emitEvent(AccountStatusCheckEvent{
			Type:      "batch_complete",
			GroupID:   t.group.ID,
			GroupName: t.group.Name,
			ModelID:   t.modelID,
			Mode:      t.mode,
			Stats:     cloneAccountStatusCheckStats(stats),
		})
	}

	workerCount := accountStatusCheckWorkerCount
	if len(t.accounts) < workerCount {
		workerCount = len(t.accounts)
	}
	jobs := make(chan Account)
	results := make(chan accountStatusCheckWorkerResult, workerCount)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case account, ok := <-jobs:
					if !ok {
						return
					}
					result := t.checkAccount(runCtx, account, emitEvent)
					select {
					case results <- result:
					case <-runCtx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, account := range t.accounts {
			select {
			case <-runCtx.Done():
				return
			default:
			}
			if err := emitEvent(AccountStatusCheckEvent{
				Type:            "account_start",
				AccountID:       account.ID,
				AccountName:     account.Name,
				AccountPriority: account.Priority,
			}); err != nil {
				return
			}
			select {
			case jobs <- account:
			case <-runCtx.Done():
				return
			}
		}
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	for result := range results {
		if result.canceled {
			continue
		}
		stats.add(result.category)
		resultEvent := AccountStatusCheckEvent{
			Type:           "account_result",
			AccountID:      result.account.ID,
			AccountName:    result.account.Name,
			Category:       result.category,
			HTTPStatus:     result.httpStatus,
			Error:          result.errorMessage,
			LatencyMs:      result.latencyMs,
			Consecutive403: result.consecutive403,
			Threshold403:   result.threshold403,
			Total:          stats.Total,
			Completed:      stats.Completed,
			Stats:          cloneAccountStatusCheckStats(stats),
		}
		if err := emitEvent(resultEvent); err != nil {
			break
		}
		if err := emitEvent(AccountStatusCheckEvent{
			Type:      "batch_progress",
			Total:     stats.Total,
			Completed: stats.Completed,
			Stats:     cloneAccountStatusCheckStats(stats),
		}); err != nil {
			break
		}
	}

	canceled := runCtx.Err() != nil
	completeEvent := AccountStatusCheckEvent{
		Type:      "batch_complete",
		GroupID:   t.group.ID,
		GroupName: t.group.Name,
		ModelID:   t.modelID,
		Mode:      t.mode,
		Total:     stats.Total,
		Completed: stats.Completed,
		Canceled:  canceled,
		Stats:     cloneAccountStatusCheckStats(stats),
	}
	if err := emitEvent(completeEvent); err != nil && !canceled {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (t *AccountStatusCheckTask) checkAccount(ctx context.Context, account Account, emit AccountStatusCheckEmitter) accountStatusCheckWorkerResult {
	result := accountStatusCheckWorkerResult{account: account}
	if account.Platform != PlatformOpenAI {
		result.category = AccountStatusCheckOtherError
		result.errorMessage = fmt.Sprintf("platform mismatch: expected openai, got %s", account.Platform)
		return result
	}

	testResult := t.service.tester.RunAccountTest(ctx, account.ID, t.modelID, t.mode, func(event TestEvent) {
		text := accountStatusCheckLogText(event)
		if text == "" {
			return
		}
		_ = emit(AccountStatusCheckEvent{
			Type:        "account_log",
			AccountID:   account.ID,
			AccountName: account.Name,
			LogType:     event.Type,
			Text:        text,
			HTTPStatus:  event.HTTPStatus,
		})
	})
	if ctx.Err() != nil {
		result.canceled = true
		return result
	}

	result.latencyMs = testResult.LatencyMs
	result.httpStatus = testResult.HTTPStatus
	result.errorMessage = testResult.Error
	if testResult.Success {
		result.category = AccountStatusCheckNormal
		result.httpStatus = http.StatusOK
		if t.service.stateManager != nil {
			if _, err := t.service.stateManager.RecoverAccountAfterSuccessfulTest(ctx, account.ID); err != nil {
				_ = emit(AccountStatusCheckEvent{
					Type:        "account_log",
					AccountID:   account.ID,
					AccountName: account.Name,
					LogType:     "warning",
					Text:        fmt.Sprintf("state recovery warning: %s", err.Error()),
				})
			}
		}
		return result
	}

	switch testResult.HTTPStatus {
	case http.StatusUnauthorized:
		result.category = AccountStatusCheckUnauthorized
	case http.StatusTooManyRequests:
		result.category = AccountStatusCheckQuotaExhausted
	case http.StatusForbidden:
		result.category = AccountStatusCheckForbidden
		if t.service.stateManager != nil && !testResult.AccountStateHandled {
			body := []byte(testResult.RawErrorBody)
			if len(body) == 0 {
				body = []byte(testResult.Error)
			}
			forbidden := t.service.stateManager.HandleOpenAI403ForAccountTest(ctx, &account, body)
			if forbidden.Message != "" {
				result.errorMessage = forbidden.Message
			}
			result.consecutive403 = forbidden.Count
			result.threshold403 = forbidden.Threshold
		}
	default:
		result.category = AccountStatusCheckOtherError
	}
	if strings.TrimSpace(result.errorMessage) == "" {
		result.errorMessage = "account test failed"
	}
	return result
}

func accountStatusCheckLogText(event TestEvent) string {
	var text string
	switch event.Type {
	case "test_start":
		if event.Model != "" {
			text = fmt.Sprintf("testing model %s", event.Model)
		} else {
			text = "test started"
		}
	case "status", "content":
		text = event.Text
	case "image":
		text = "image response received"
	case "error":
		text = event.Error
	case "test_complete":
		if event.Success {
			text = "test completed successfully"
		} else if event.Error != "" {
			text = event.Error
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return truncateForLog([]byte(text), 4096)
}

func cloneAccountStatusCheckStats(stats AccountStatusCheckStats) *AccountStatusCheckStats {
	copy := stats
	return &copy
}
