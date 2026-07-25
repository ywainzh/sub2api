package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSPassthroughClientModelForValidation(t *testing.T) {
	model, ok := openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"response.create","model":"gpt-explicit"}`),
		"gpt-session",
	)
	require.True(t, ok)
	require.Equal(t, "gpt-explicit", model)

	model, ok = openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"response.create"}`),
		"gpt-session",
	)
	require.True(t, ok)
	require.Equal(t, "gpt-session", model)

	model, ok = openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"session.update","session":{"model":"gpt-rotated"}}`),
		"gpt-session",
	)
	require.True(t, ok)
	require.Equal(t, "gpt-rotated", model)

	model, ok = openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"session.update","session":{"voice":"alloy"}}`),
		"gpt-session",
	)
	require.False(t, ok)
	require.Empty(t, model)

	model, ok = openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"response.create","model":123}`),
		"gpt-session",
	)
	require.True(t, ok)
	require.Empty(t, model, "an explicitly invalid model must not reuse the session model")

	model, ok = openAIWSPassthroughClientModelForValidation(
		[]byte(`{"type":"session.update","session":{"model":false}}`),
		"gpt-session",
	)
	require.True(t, ok)
	require.Empty(t, model, "an explicitly invalid session model must fail closed")
}

func TestNormalizeOpenAIWSPassthroughClientEventType(t *testing.T) {
	eventType, normalized, err := normalizeOpenAIWSPassthroughClientEventType(
		[]byte(`{"model":"gpt-5.6-terra","input":"hello"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "response.create", eventType)
	require.Equal(t, "response.create", gjson.GetBytes(normalized, "type").String())

	eventType, normalized, err = normalizeOpenAIWSPassthroughClientEventType(
		[]byte(`{"type":"session.update","session":{"model":"gpt-5.6-terra"}}`),
	)
	require.NoError(t, err)
	require.Equal(t, "session.update", eventType)
	require.Equal(t, "session.update", gjson.GetBytes(normalized, "type").String())

	_, _, err = normalizeOpenAIWSPassthroughClientEventType([]byte(`{"type":123}`))
	require.Error(t, err)

	_, _, err = normalizeOpenAIWSPassthroughClientEventType([]byte(`not-json`))
	require.Error(t, err)
}
