package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeZenAccountContract(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			OpenAIProviderModeExtraKey: OpenAIProviderModeOpenCodeZen,
			OpenCodeEgressModeExtraKey: OpenCodeEgressModeServerDirect,
		},
		Credentials: map[string]any{"base_url": "https://ignored.example/v1"},
	}
	require.True(t, account.IsOpenCodeZen())
	require.Equal(t, OpenCodeZenBaseURL, account.GetOpenAIBaseURL())
	require.Equal(t, OpenCodeEgressModeServerDirect, account.GetOpenCodeEgressMode())
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
}

func TestTransformOpenCodeZenChatBody(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash-free","stream":false,"client_metadata":{"x":1},"messages":[{"role":"assistant","content":"prior"}]}`)
	transformed, err := transformOpenCodeZenChatBody(body, "deepseek-v4-flash-free", true)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(transformed, &payload))
	require.NotContains(t, payload, "client_metadata")
	require.Equal(t, true, payload["stream"])
	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	firstMessage, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, " ", firstMessage["reasoning_content"])
}

func TestTransformOpenCodeZenResponsesStringInput(t *testing.T) {
	normalized, changed, err := normalizeOpenCodeResponsesStringInput([]byte(`{"model":"deepseek-v4-flash-free","input":"Reply exactly OK","stream":false,"max_output_tokens":32}`))
	require.NoError(t, err)
	require.True(t, changed)

	var responsesReq apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal(normalized, &responsesReq))

	chatReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	require.NoError(t, err)
	chatBody, err := json.Marshal(chatReq)
	require.NoError(t, err)
	chatBody, err = transformOpenCodeZenChatBody(chatBody, responsesReq.Model, responsesReq.Stream)
	require.NoError(t, err)

	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(chatBody, &payload))
	require.Len(t, payload.Messages, 1)
	require.Equal(t, "user", payload.Messages[0].Role)
	require.Equal(t, "Reply exactly OK", payload.Messages[0].Content)

	var arrayReq apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"Reply exactly OK"}],"stream":false,"max_output_tokens":32}`), &arrayReq))
	arrayChatReq, err := apicompat.ResponsesToChatCompletionsRequest(&arrayReq)
	require.NoError(t, err)
	arrayChatBody, err := json.Marshal(arrayChatReq)
	require.NoError(t, err)
	arrayChatBody, err = transformOpenCodeZenChatBody(arrayChatBody, arrayReq.Model, arrayReq.Stream)
	require.NoError(t, err)
	require.JSONEq(t, string(arrayChatBody), string(chatBody))
}

func TestNormalizeOpenCodeResponsesStringInputLeavesArrayUnchanged(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash-free","input":[{"role":"user","content":"Reply exactly OK"}]}`)
	normalized, changed, err := normalizeOpenCodeResponsesStringInput(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, normalized)
}

func TestTransformOpenCodeZenChatBodyRejectsUnknownModel(t *testing.T) {
	_, err := transformOpenCodeZenChatBody([]byte(`{"model":"paid-model","messages":[]}`), "paid-model", false)
	require.ErrorIs(t, err, ErrOpenCodeModelNotAllowed)
}

func TestTransformOpenCodeZenChatBodyResolvesEffortAliasBeforeFreeCheck(t *testing.T) {
	previous := defaultOpenCodeFreeModels.IDs()
	defaultOpenCodeFreeModels.Replace([]string{"deepseek-v4-pro"})
	t.Cleanup(func() { defaultOpenCodeFreeModels.Replace(previous) })

	transformed, err := transformOpenCodeZenChatBody([]byte(`{"model":"deepseek-v4-pro-high","messages":[]}`), "deepseek-v4-pro-high", false)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(transformed, &payload))
	require.Equal(t, "deepseek-v4-pro", payload["model"])
	require.Equal(t, "high", payload["reasoning_effort"])
}

func TestSendCCUpstreamRequestKeylessOpenCodeOmitsAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}}
	service := &OpenAIGatewayService{httpUpstream: recorder}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	response := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(response)
	ginContext.Request = request
	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{OpenAIProviderModeExtraKey: OpenAIProviderModeOpenCodeZen},
	}

	resp, err := service.sendCCUpstreamRequest(context.Background(), ginContext, account, OpenCodeZenBaseURL+"/chat/completions", []byte(`{}`), false, "", "", "")
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, recorder.lastReq.Header.Get("Authorization"))
}

func TestParseOpenCode429ResetAt(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)

	retryAfter := parseOpenCode429ResetAt(http.Header{"Retry-After": []string{"90"}}, nil, now)
	require.NotNil(t, retryAfter)
	require.Equal(t, now.Add(90*time.Second), *retryAfter)

	bodyReset := parseOpenCode429ResetAt(nil, []byte(`{"error":{"retry_after_seconds":45}}`), now)
	require.NotNil(t, bodyReset)
	require.Equal(t, now.Add(45*time.Second), *bodyReset)
}

type openCodeRateLimitRepoStub struct {
	AccountRepository
	resetAt time.Time
}

func (s *openCodeRateLimitRepoStub) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	s.resetAt = resetAt
	return nil
}

func TestOpenCode429UsesSixtySecondFallbackAndPersists(t *testing.T) {
	repo := &openCodeRateLimitRepoStub{}
	service := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	account := &Account{
		ID: 77, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Extra: map[string]any{OpenAIProviderModeExtraKey: OpenAIProviderModeOpenCodeZen},
	}
	startedAt := time.Now()

	service.markOpenAIOAuth429RateLimited(context.Background(), account, nil, nil)

	require.WithinDuration(t, startedAt.Add(time.Minute), repo.resetAt, 2*time.Second)
	blockedUntil, ok := service.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	blockedAt, ok := blockedUntil.(time.Time)
	require.True(t, ok)
	require.WithinDuration(t, repo.resetAt, blockedAt, time.Second)
}

func TestApplyOpenCodeZenHeaders(t *testing.T) {
	headers := make(http.Header)
	client := http.Header{"X-Opencode-Session": []string{"session-from-client"}}
	applyOpenCodeZenHeaders(headers, client)
	require.Equal(t, "session-from-client", headers.Get("x-opencode-session"))
	require.NotEmpty(t, headers.Get("x-opencode-request"))
	require.Equal(t, "cli", headers.Get("x-opencode-client"))
	require.Equal(t, "default", headers.Get("x-opencode-project"))
}
