package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// rejectOpenAIGroupModel performs the common early validation for regular
// OpenAI-compatible HTTP handlers. It deliberately runs before security audit,
// channel/model mapping, concurrency slots, billing and account selection.
func (h *OpenAIGatewayHandler) rejectOpenAIGroupModel(c *gin.Context, apiKey *service.APIKey, model string) bool {
	err := service.ValidateOpenAIGroupModel(groupFromAPIKey(apiKey), model)
	return h.writeOpenAIGroupModelRejection(c, err)
}

// rejectOpenAIGroupModelPayload is the raw-body variant used by HTTP handlers.
// It rejects duplicate client model keys before the request is mapped or
// decoded by another layer, preventing first-key/last-key interpretation
// differences from bypassing a strict group allowlist.
func (h *OpenAIGatewayHandler) rejectOpenAIGroupModelPayload(c *gin.Context, apiKey *service.APIKey, payload []byte, modelPath, model string) bool {
	err := service.ValidateOpenAIGroupModelPayload(groupFromAPIKey(apiKey), payload, modelPath, model)
	return h.writeOpenAIGroupModelRejection(c, err)
}

func (h *OpenAIGatewayHandler) writeOpenAIGroupModelRejection(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	h.errorResponseWithCode(
		c,
		infraerrors.Code(err),
		"invalid_request_error",
		infraerrors.Reason(err),
		infraerrors.Message(err),
	)
	return true
}

// rejectAnthropicOpenAIGroupModel is the same policy with the Anthropic
// Messages/Count Tokens error envelope.
func (h *OpenAIGatewayHandler) rejectAnthropicOpenAIGroupModel(c *gin.Context, apiKey *service.APIKey, model string) bool {
	err := service.ValidateOpenAIGroupModel(groupFromAPIKey(apiKey), model)
	return h.writeAnthropicOpenAIGroupModelRejection(c, err)
}

func (h *OpenAIGatewayHandler) rejectAnthropicOpenAIGroupModelPayload(c *gin.Context, apiKey *service.APIKey, payload []byte, modelPath, model string) bool {
	err := service.ValidateOpenAIGroupModelPayload(groupFromAPIKey(apiKey), payload, modelPath, model)
	return h.writeAnthropicOpenAIGroupModelRejection(c, err)
}

func (h *OpenAIGatewayHandler) writeAnthropicOpenAIGroupModelRejection(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	h.anthropicErrorResponseWithCode(
		c,
		infraerrors.Code(err),
		"invalid_request_error",
		infraerrors.Reason(err),
		infraerrors.Message(err),
	)
	return true
}

func groupFromAPIKey(apiKey *service.APIKey) *service.Group {
	if apiKey == nil {
		return nil
	}
	return apiKey.Group
}

// openAIWSRequestedModelForWhitelist resolves only client-facing model fields.
// response.create uses its root model, while session.update uses session.model.
// The adapter-provided original model is a raw client/session fallback, and the
// first validated model is the final fallback for follow-up turns that omit it.
func openAIWSRequestedModelForWhitelist(payload []byte, originalModel, initialModel string) string {
	modelPath := openAIWSModelPathForWhitelist(payload)
	modelResult := gjson.GetBytes(payload, modelPath)
	if modelResult.Exists() {
		// An explicitly supplied non-string/blank model is invalid and must not
		// silently fall back to a previously validated model.
		if modelResult.Type != gjson.String {
			return ""
		}
		return strings.TrimSpace(modelResult.String())
	}
	if model := strings.TrimSpace(originalModel); model != "" {
		return model
	}
	return strings.TrimSpace(initialModel)
}

func openAIWSModelPathForWhitelist(payload []byte) string {
	if strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "session.update" {
		return "session.model"
	}
	return "model"
}

func validateOpenAIWSGroupModel(group *service.Group, payload []byte, model string) error {
	if group != nil && group.ModelWhitelistEnabled() && service.HasDuplicateJSONPathField(payload, "type") {
		// The event type decides whether the client-facing model lives at model
		// or session.model.  Reject an ambiguous type before choosing either path.
		return service.ValidateOpenAIGroupModel(group, "")
	}
	return service.ValidateOpenAIGroupModelPayload(group, payload, openAIWSModelPathForWhitelist(payload), model)
}

func writeModelNotAllowedWSError(ctx context.Context, conn *coderws.Conn, err error) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := infraerrors.Message(err)
	if strings.TrimSpace(message) == "" {
		message = "model is not allowed for this group"
	}
	payload, marshalErr := json.Marshal(gin.H{
		"event_id": "evt_model_not_allowed",
		"type":     "error",
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    service.ModelNotAllowedErrorCode,
			"message": message,
		},
	})
	if marshalErr != nil {
		payload = []byte(`{"event_id":"evt_model_not_allowed","type":"error","error":{"type":"invalid_request_error","code":"model_not_allowed","message":"model is not allowed for this group"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
}
