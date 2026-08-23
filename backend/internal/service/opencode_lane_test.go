package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newOpenCodeZenTestAccount(lane string) *Account {
	extra := map[string]any{
		OpenAIProviderModeExtraKey: OpenAIProviderModeOpenCodeZen,
	}
	if lane != "" {
		extra[OpenCodeLaneExtraKey] = lane
	}
	return &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    extra,
	}
}

// OpenCode Zen 用同一个 401 表达三种含义。只有 ModelError 该走「冷却账号×模型」，
// AuthError / CreditsError 必须原样落到禁用账号的既有逻辑。
func TestIsOpenCodeZenModelRejectionDiscriminatesByErrorType(t *testing.T) {
	modelError := []byte(`{"error":{"type":"ModelError","message":"Model ox-alpha-free is not supported"}}`)
	require.True(t, isOpenCodeZenModelRejection(http.StatusUnauthorized, modelError))

	for _, body := range [][]byte{
		[]byte(`{"error":{"type":"AuthError","message":"Invalid API key"}}`),
		[]byte(`{"error":{"type":"CreditsError","message":"Insufficient credits"}}`),
	} {
		require.False(t, isOpenCodeZenModelRejection(http.StatusUnauthorized, body), string(body))
	}

	// 只对 401 生效。
	require.False(t, isOpenCodeZenModelRejection(http.StatusNotFound, modelError))
	require.False(t, isOpenCodeZenModelRejection(http.StatusTooManyRequests, modelError))

	// error.type 缺失时退回措辞匹配。
	require.True(t, isOpenCodeZenModelRejection(http.StatusUnauthorized,
		[]byte(`{"error":{"message":"Model ox-alpha-free is not supported"}}`)))
	require.False(t, isOpenCodeZenModelRejection(http.StatusUnauthorized,
		[]byte(`{"error":{"message":"Missing API key"}}`)))
	require.False(t, isOpenCodeZenModelRejection(http.StatusUnauthorized, nil))
}

func TestGetOpenCodeLaneDefaultsToKeyed(t *testing.T) {
	require.Equal(t, OpenCodeLaneKeyed, newOpenCodeZenTestAccount("").GetOpenCodeLane())
	require.Equal(t, OpenCodeLaneKeyed, newOpenCodeZenTestAccount(OpenCodeLaneKeyed).GetOpenCodeLane())
	require.Equal(t, OpenCodeLaneAnonymous, newOpenCodeZenTestAccount(OpenCodeLaneAnonymous).GetOpenCodeLane())

	// 非 OpenCode 账号没有 lane 概念。
	require.Equal(t, "", (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).GetOpenCodeLane())
	require.Equal(t, "", (*Account)(nil).GetOpenCodeLane())
}

// 空 api_key 绝不能被当成匿名信号：池级 key 被清空时 keyed worker 会短暂拿到空 key。
func TestOpenCodeLaneIgnoresEmptyAPIKey(t *testing.T) {
	account := newOpenCodeZenTestAccount("")
	account.Credentials = map[string]any{"api_key": ""}
	require.False(t, account.IsOpenCodeAnonymousLane())
	require.Equal(t, OpenCodeLaneKeyed, account.GetOpenCodeLane())
}

func TestSkipPersistRateLimitOnlyForAnonymousOpenCode(t *testing.T) {
	require.True(t, skipPersistRateLimit(newOpenCodeZenTestAccount(OpenCodeLaneAnonymous)))
	require.False(t, skipPersistRateLimit(newOpenCodeZenTestAccount(OpenCodeLaneKeyed)))
	require.False(t, skipPersistRateLimit(newOpenCodeZenTestAccount("")))
	require.False(t, skipPersistRateLimit(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	require.False(t, skipPersistRateLimit(&Account{Platform: PlatformAnthropic}))
	require.False(t, skipPersistRateLimit(nil))
}

type setRateLimitedSpyRepo struct {
	*sessionWindowMockRepo
	calls []time.Time
}

func newSetRateLimitedSpy() *setRateLimitedSpyRepo {
	return &setRateLimitedSpyRepo{sessionWindowMockRepo: &sessionWindowMockRepo{}}
}

func (r *setRateLimitedSpyRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.calls = append(r.calls, resetAt)
	return nil
}

// handle429 是 429 的第二个持久化写入点，跑在 fastpath 之后。OpenCode Zen 的重置时间
// 只出现在 Retry-After / x-ratelimit-reset 里，x-codex-* 解析对它恒为 nil；缺了这个分支
// 就会坠到末尾的全局兜底（5 秒），把 fastpath 刚落库的精确冷却覆盖成秒级。
func TestHandle429KeepsOpenCodeRetryAfterInsteadOfGlobalFallback(t *testing.T) {
	spy := newSetRateLimitedSpy()
	s := &RateLimitService{accountRepo: spy}

	headers := http.Header{}
	headers.Set("Retry-After", "900")
	before := time.Now()
	s.handle429(context.Background(), newOpenCodeZenTestAccount(OpenCodeLaneKeyed), headers, nil)

	require.Len(t, spy.calls, 1)
	cooldown := spy.calls[0].Sub(before)
	require.Greater(t, cooldown, 890*time.Second)
	require.Less(t, cooldown, 910*time.Second)
}

// 反向对照：真的解析不出重置时间时仍要落全局兜底，不能静默不冷却。
func TestHandle429FallsBackWhenOpenCodeGivesNoResetTime(t *testing.T) {
	spy := newSetRateLimitedSpy()
	s := &RateLimitService{accountRepo: spy}

	before := time.Now()
	s.handle429(context.Background(), newOpenCodeZenTestAccount(OpenCodeLaneKeyed), http.Header{}, nil)

	require.Len(t, spy.calls, 1)
	require.Less(t, spy.calls[0].Sub(before),
		time.Duration(defaultRateLimit429CooldownSeconds)*time.Second+5*time.Second)
}

// 匿名 lane 的守卫排在最前面：解析得出精确时间也不得落库，否则触发器会删租约。
func TestHandle429NeverPersistsForAnonymousLane(t *testing.T) {
	spy := newSetRateLimitedSpy()
	s := &RateLimitService{accountRepo: spy}

	headers := http.Header{}
	headers.Set("Retry-After", "900")
	s.handle429(context.Background(), newOpenCodeZenTestAccount(OpenCodeLaneAnonymous), headers, nil)

	require.Empty(t, spy.calls)
}
