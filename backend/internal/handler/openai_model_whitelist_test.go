package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func strictOpenAIAPIKeyForTest(models ...string) *service.APIKey {
	groupID := int64(1)
	return &service.APIKey{
		ID:      1,
		UserID:  1,
		GroupID: &groupID,
		User:    &service.User{ID: 1},
		Group: &service.Group{
			ID:                    groupID,
			Platform:              service.PlatformOpenAI,
			AllowMessagesDispatch: true,
			AllowImageGeneration:  true,
			ModelsListConfig: service.GroupModelsListConfig{
				Enforce: true,
				Models:  models,
			},
		},
	}
}

func TestRejectOpenAIGroupModel_WritesStableErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	blocked := (&OpenAIGatewayHandler{}).rejectOpenAIGroupModel(c, strictOpenAIAPIKeyForTest("gpt-5.6-terra"), "gpt-5.4")

	require.True(t, blocked)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

func TestRejectAnthropicOpenAIGroupModel_WritesStableErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	blocked := (&OpenAIGatewayHandler{}).rejectAnthropicOpenAIGroupModel(c, strictOpenAIAPIKeyForTest("gpt-5.6-terra"), "gpt-5.4")

	require.True(t, blocked)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
	require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}

func TestImageTaskModelNotAllowedUsesOpenAIErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	imageTaskError(c, infraerrors.New(
		http.StatusBadRequest,
		service.ModelNotAllowedErrorCode,
		"model is not allowed for this group",
	))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}

func TestOpenAIWSRequestedModelForWhitelist(t *testing.T) {
	require.Equal(t,
		"gpt-explicit",
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"response.create","model":"gpt-explicit"}`),
			"gpt-session",
			"gpt-initial",
		),
	)
	require.Equal(t,
		"gpt-session-update",
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"session.update","session":{"model":"gpt-session-update"}}`),
			"gpt-session",
			"gpt-initial",
		),
	)
	require.Equal(t,
		"gpt-session",
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"response.create"}`),
			"gpt-session",
			"gpt-initial",
		),
	)
	require.Equal(t,
		"gpt-initial",
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"response.create"}`),
			"",
			"gpt-initial",
		),
	)
	require.Empty(t,
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"response.create","model":123}`),
			"gpt-session",
			"gpt-initial",
		),
		"an explicitly invalid model must not fall back to an allowed session model",
	)
	require.Empty(t,
		openAIWSRequestedModelForWhitelist(
			[]byte(`{"type":"session.update","session":{"model":"   "}}`),
			"gpt-session",
			"gpt-initial",
		),
		"an explicitly blank session model must fail closed",
	)
}

func TestValidateOpenAIWSGroupModelRejectsAmbiguousEventTypeAndModelPath(t *testing.T) {
	group := strictOpenAIAPIKeyForTest("gpt-5.6-terra").Group

	err := validateOpenAIWSGroupModel(
		group,
		[]byte(`{"type":"response.create","type":"session.update","model":"gpt-5.6-terra","session":{"model":"gpt-5.4"}}`),
		"gpt-5.6-terra",
	)
	require.Error(t, err)
	require.Equal(t, service.ModelNotAllowedErrorCode, infraerrors.Reason(err))

	err = validateOpenAIWSGroupModel(
		group,
		[]byte(`{"type":"session.update","session":{"model":"gpt-5.6-terra","model":"gpt-5.4"}}`),
		"gpt-5.6-terra",
	)
	require.Error(t, err)
	require.Equal(t, service.ModelNotAllowedErrorCode, infraerrors.Reason(err))
}

func TestStrictOpenAIHandlersRejectNonStringModelBeforeConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		path string
		body string
		run  func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{name: "responses", path: "/openai/v1/responses", body: `{"model":123,"input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) }},
		{name: "compact", path: "/openai/v1/responses/compact", body: `{"model":123,"input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) }},
		{name: "chat_completions", path: "/openai/v1/chat/completions", body: `{"model":123,"messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.ChatCompletions(c) }},
		{name: "messages", path: "/v1/messages", body: `{"model":123,"messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Messages(c) }},
		{name: "count_tokens", path: "/v1/messages/count_tokens", body: `{"model":123,"messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.CountTokens(c) }},
		{name: "embeddings", path: "/v1/embeddings", body: `{"model":123,"input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Embeddings(c) }},
		{name: "alpha_search", path: "/v1/alpha/search", body: `{"model":123,"query":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.AlphaSearch(c) }},
		{name: "images", path: "/v1/images/generations", body: `{"model":123,"prompt":"draw"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Images(c) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var acquireCalls atomic.Int64
			cache := &concurrencyCacheMock{
				acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
					acquireCalls.Add(1)
					return true, nil
				},
			}
			h := newOpenAIImageChatRejectionHandlerWithCache(t, cache)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			apiKey := strictOpenAIAPIKeyForTest("123")
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})

			tt.run(h, c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
			require.Zero(t, acquireCalls.Load(), "strict rejection must precede concurrency and scheduling")
		})
	}
}

func TestStrictOpenAIHandlersRejectDuplicateModelBeforeConcurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		path string
		body string
		run  func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{name: "responses", path: "/openai/v1/responses", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) }},
		{name: "compact", path: "/openai/v1/responses/compact", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) }},
		{name: "chat_completions", path: "/openai/v1/chat/completions", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.ChatCompletions(c) }},
		{name: "messages", path: "/v1/messages", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Messages(c) }},
		{name: "count_tokens", path: "/v1/messages/count_tokens", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.CountTokens(c) }},
		{name: "embeddings", path: "/v1/embeddings", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","input":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Embeddings(c) }},
		{name: "alpha_search", path: "/v1/alpha/search", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","query":"hello"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.AlphaSearch(c) }},
		{name: "images", path: "/v1/images/generations", body: `{"model":"gpt-5.6-terra","model":"gpt-5.4","prompt":"draw"}`, run: func(h *OpenAIGatewayHandler, c *gin.Context) { h.Images(c) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var acquireCalls atomic.Int64
			cache := &concurrencyCacheMock{
				acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) {
					acquireCalls.Add(1)
					return true, nil
				},
			}
			h := newOpenAIImageChatRejectionHandlerWithCache(t, cache)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			apiKey := strictOpenAIAPIKeyForTest("gpt-5.6-terra")
			c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})

			tt.run(h, c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
			require.Zero(t, acquireCalls.Load(), "duplicate model rejection must precede concurrency and scheduling")
		})
	}
}

func TestStrictBatchImageRejectsNonStringModelBeforeJSONBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/images/batches",
		bytes.NewBufferString(`{"model":123,"items":[{"custom_id":"item-1","prompt":"draw"}]}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	apiKey := strictOpenAIAPIKeyForTest("123")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	(&BatchImageHandler{}).Submit(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}

func TestStrictBatchImageRejectsDuplicateModelBeforeJSONBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/images/batches",
		bytes.NewBufferString(`{"model":"gpt-5.6-terra","model":"gpt-5.4","items":[{"custom_id":"item-1","prompt":"draw"}]}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	apiKey := strictOpenAIAPIKeyForTest("gpt-5.6-terra")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	(&BatchImageHandler{}).Submit(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, service.ModelNotAllowedErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
}
