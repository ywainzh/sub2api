package service

import (
	"net/http"
	"testing"

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
