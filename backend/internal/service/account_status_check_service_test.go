package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type statusCheckSourceStub struct {
	group        *Group
	accounts     []Account
	getErr       error
	listErr      error
	lastPlatform string
	lastStatus   string
	lastGroupID  int64
}

func (s *statusCheckSourceStub) GetGroup(context.Context, int64) (*Group, error) {
	return s.group, s.getErr
}

func (s *statusCheckSourceStub) ListAccountsForSchedulerScoreFilter(
	_ context.Context,
	platform, _, status, _ string,
	groupID int64,
	_ string,
) ([]Account, error) {
	s.lastPlatform = platform
	s.lastStatus = status
	s.lastGroupID = groupID
	return s.accounts, s.listErr
}

type statusCheckTesterStub struct {
	active    int32
	maxActive int32
	delay     time.Duration
	results   map[int64]AccountTestRunResult
}

func (s *statusCheckTesterStub) RunAccountTest(
	ctx context.Context,
	accountID int64,
	modelID string,
	_ string,
	sink AccountTestEventSink,
) AccountTestRunResult {
	active := atomic.AddInt32(&s.active, 1)
	for {
		max := atomic.LoadInt32(&s.maxActive)
		if active <= max || atomic.CompareAndSwapInt32(&s.maxActive, max, active) {
			break
		}
	}
	defer atomic.AddInt32(&s.active, -1)

	if sink != nil {
		sink(TestEvent{Type: "test_start", Model: modelID})
	}
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return AccountTestRunResult{}
		case <-time.After(s.delay):
		}
	}
	result := s.results[accountID]
	if result.Success && sink != nil {
		sink(TestEvent{Type: "test_complete", Success: true})
	}
	return result
}

type statusCheckStateStub struct {
	recovered int32
	forbidden int32
}

func (s *statusCheckStateStub) RecoverAccountAfterSuccessfulTest(context.Context, int64) (*SuccessfulTestRecoveryResult, error) {
	atomic.AddInt32(&s.recovered, 1)
	return &SuccessfulTestRecoveryResult{}, nil
}

func (s *statusCheckStateStub) HandleOpenAI403ForAccountTest(context.Context, *Account, []byte) OpenAI403TestResult {
	atomic.AddInt32(&s.forbidden, 1)
	return OpenAI403TestResult{Message: "Access forbidden (403): Agent runtime has been deleted. | consecutive_403=3/3", Count: 3, Threshold: 3}
}

func newStatusCheckTestService(source *statusCheckSourceStub, tester accountStatusCheckTester, state accountStatusCheckStateManager) *AccountStatusCheckService {
	return &AccountStatusCheckService{source: source, tester: tester, stateManager: state, running: make(map[int64]struct{})}
}

func TestAccountStatusCheckStartValidatesGroupAndRequest(t *testing.T) {
	tests := []struct {
		name    string
		group   *Group
		request AccountStatusCheckRequest
		wantErr bool
	}{
		{name: "missing group", request: AccountStatusCheckRequest{GroupID: 1}, wantErr: true},
		{name: "non openai group", group: &Group{ID: 1, Platform: PlatformAnthropic}, request: AccountStatusCheckRequest{GroupID: 1}, wantErr: true},
		{name: "invalid mode", group: &Group{ID: 1, Platform: PlatformOpenAI}, request: AccountStatusCheckRequest{GroupID: 1, Mode: "stream"}, wantErr: true},
		{name: "invalid model", group: &Group{ID: 1, Platform: PlatformOpenAI}, request: AccountStatusCheckRequest{GroupID: 1, ModelID: "gpt 5"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &statusCheckSourceStub{group: tt.group}
			service := newStatusCheckTestService(source, &statusCheckTesterStub{}, nil)
			task, err := service.Start(context.Background(), tt.request)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, task)
				return
			}
			require.NoError(t, err)
			task.Close()
		})
	}
}

func TestAccountStatusCheckRunUsesFiveWorkersAndClassifiesResults(t *testing.T) {
	accounts := make([]Account, 0, 8)
	for id := int64(1); id <= 8; id++ {
		accounts = append(accounts, Account{ID: id, Name: "account", Platform: PlatformOpenAI, Priority: int(10 - id)})
	}
	source := &statusCheckSourceStub{group: &Group{ID: 9, Name: "OpenAI", Platform: PlatformOpenAI}, accounts: accounts}
	tester := &statusCheckTesterStub{delay: 5 * time.Millisecond, results: map[int64]AccountTestRunResult{
		1: {Success: true},
		2: {HTTPStatus: 401, Error: "API returned 401: unauthorized"},
		3: {HTTPStatus: 429, Error: "API returned 429: quota exhausted"},
		4: {HTTPStatus: 403, Error: "upstream forbidden", RawErrorBody: `{"detail":"Agent runtime has been deleted."}`},
	}}
	state := &statusCheckStateStub{}
	service := newStatusCheckTestService(source, tester, state)
	task, err := service.Start(context.Background(), AccountStatusCheckRequest{GroupID: 9, ModelID: "gpt-5.5", Mode: AccountTestModeDefault})
	require.NoError(t, err)

	var events []AccountStatusCheckEvent
	err = task.Run(context.Background(), func(event AccountStatusCheckEvent) error {
		events = append(events, event)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "", source.lastPlatform)
	require.Equal(t, "", source.lastStatus)
	require.Equal(t, int64(9), source.lastGroupID)
	require.LessOrEqual(t, atomic.LoadInt32(&tester.maxActive), int32(accountStatusCheckWorkerCount))
	require.Equal(t, int32(1), atomic.LoadInt32(&state.recovered))
	require.Equal(t, int32(1), atomic.LoadInt32(&state.forbidden))

	var complete AccountStatusCheckEvent
	for _, event := range events {
		if event.Type == "batch_complete" {
			complete = event
		}
	}
	require.Equal(t, 8, complete.Stats.Total)
	require.Equal(t, 8, complete.Stats.Completed)
	require.Equal(t, 1, complete.Stats.Normal)
	require.Equal(t, 1, complete.Stats.Unauthorized)
	require.Equal(t, 1, complete.Stats.QuotaExhausted)
	require.Equal(t, 1, complete.Stats.Forbidden)
	require.Equal(t, 4, complete.Stats.OtherError)

	var forbidden AccountStatusCheckEvent
	for _, event := range events {
		if event.Type == "account_result" && event.Category == AccountStatusCheckForbidden {
			forbidden = event
		}
	}
	require.Contains(t, forbidden.Error, "consecutive_403=3/3")
}

func TestAccountStatusCheckRejectsDuplicateGroupRunAndCancels(t *testing.T) {
	source := &statusCheckSourceStub{group: &Group{ID: 4, Platform: PlatformOpenAI}, accounts: []Account{{ID: 1, Platform: PlatformOpenAI}}}
	service := newStatusCheckTestService(source, &statusCheckTesterStub{delay: time.Second}, nil)
	task, err := service.Start(context.Background(), AccountStatusCheckRequest{GroupID: 4})
	require.NoError(t, err)
	defer task.Close()
	_, err = service.Start(context.Background(), AccountStatusCheckRequest{GroupID: 4})
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = task.Run(ctx, func(AccountStatusCheckEvent) error { return nil })
	require.ErrorIs(t, err, context.Canceled)
	_, err = service.Start(context.Background(), AccountStatusCheckRequest{GroupID: 4})
	require.NoError(t, err)
}
