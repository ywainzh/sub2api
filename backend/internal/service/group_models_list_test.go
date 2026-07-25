package service

import (
	"encoding/json"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestGroupModelsListConfigLegacyJSONDefaultsEnforceFalse(t *testing.T) {
	var cfg GroupModelsListConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"models":["gpt-5.6-terra"]}`), &cfg))
	require.True(t, cfg.Enabled)
	require.False(t, cfg.Enforce)
}

func TestValidateOpenAIGroupModel_StrictExactAllowlist(t *testing.T) {
	group := &Group{
		Platform: PlatformOpenAI,
		ModelsListConfig: GroupModelsListConfig{
			Enforce: true,
			Models:  []string{"gpt-5.6-terra"},
		},
	}

	require.NoError(t, ValidateOpenAIGroupModel(group, "  GPT-5.6-TERRA  "))
	err := ValidateOpenAIGroupModel(group, "gpt-5.4")
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, ModelNotAllowedErrorCode, infraerrors.Reason(err))

	group.ModelsListConfig.Models = []string{"gpt-5.*"}
	require.Error(t, ValidateOpenAIGroupModel(group, "gpt-5.6-terra"), "configured wildcards must not expand")
	require.Error(t, ValidateOpenAIGroupModel(group, "gpt-5.*"), "configured wildcards are not concrete model IDs")
}

func TestValidateOpenAIGroupModel_FailClosedAndCompatibility(t *testing.T) {
	strictEmpty := &Group{
		Platform:         PlatformOpenAI,
		ModelsListConfig: GroupModelsListConfig{Enforce: true},
	}
	require.Error(t, ValidateOpenAIGroupModel(strictEmpty, "gpt-5.6-terra"))
	require.Error(t, ValidateOpenAIGroupModel(strictEmpty, ""))

	require.NoError(t, ValidateOpenAIGroupModel(&Group{
		Platform:         PlatformOpenAI,
		ModelsListConfig: GroupModelsListConfig{Enforce: false},
	}, "gpt-5.4"))
	require.NoError(t, ValidateOpenAIGroupModel(&Group{
		Platform:         PlatformGrok,
		ModelsListConfig: GroupModelsListConfig{Enforce: true},
	}, "grok-4"))
}

func TestValidateOpenAIGroupModelPayloadRejectsDuplicateModelKeys(t *testing.T) {
	group := &Group{
		Platform: PlatformOpenAI,
		ModelsListConfig: GroupModelsListConfig{
			Enforce: true,
			Models:  []string{"gpt-5.6-terra"},
		},
	}

	require.False(t, HasDuplicateJSONPathField([]byte(`{"model":"gpt-5.6-terra"}`), "model"))
	require.True(t, HasDuplicateJSONPathField([]byte(`{"model":"gpt-5.6-terra","model":"gpt-5.4"}`), "model"))
	require.True(t, HasDuplicateJSONPathField([]byte(`{"session":{},"session":{"model":"gpt-5.4"}}`), "session.model"))
	require.True(t, HasDuplicateJSONPathField([]byte(`{"session":{"model":"gpt-5.6-terra","model":"gpt-5.4"}}`), "session.model"))

	err := ValidateOpenAIGroupModelPayload(
		group,
		[]byte(`{"model":"gpt-5.6-terra","model":"gpt-5.4"}`),
		"model",
		"gpt-5.6-terra",
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, ModelNotAllowedErrorCode, infraerrors.Reason(err))

	require.NoError(t, ValidateOpenAIGroupModelPayload(
		&Group{Platform: PlatformOpenAI},
		[]byte(`{"model":"gpt-5.6-terra","model":"gpt-5.4"}`),
		"model",
		"gpt-5.4",
	), "duplicate-key protection is only needed when strict enforcement is enabled")
}

func TestNormalizeGroupModelsListConfig_PreservesEnforceAndDeduplicatesCase(t *testing.T) {
	got := normalizeGroupModelsListConfig(GroupModelsListConfig{
		Enabled: true,
		Enforce: true,
		Models:  []string{" gpt-5.6-terra ", "GPT-5.6-TERRA", "gpt-5.4"},
	})
	require.True(t, got.Enabled)
	require.True(t, got.Enforce)
	require.Equal(t, []string{"gpt-5.6-terra", "gpt-5.4"}, got.Models)
}

func TestNormalizeGroupModelsListConfig_LegacyDisplayKeepsCaseDistinctEntries(t *testing.T) {
	got := normalizeGroupModelsListConfig(GroupModelsListConfig{
		Enabled: true,
		Enforce: false,
		Models:  []string{"gpt-5.6-terra", "GPT-5.6-TERRA"},
	})

	require.Equal(t, []string{"gpt-5.6-terra", "GPT-5.6-TERRA"}, got.Models)
}

func TestFilterModelsByStrictWhitelist(t *testing.T) {
	require.Equal(t,
		[]string{"gpt-5.4", "gpt-5.6-terra"},
		FilterModelsByStrictWhitelist(
			[]string{"gpt-5.4", "gpt-5.6-terra", "gpt-5.5"},
			[]string{"GPT-5.6-TERRA", "gpt-5.4", "missing"},
		),
	)
	require.Equal(t,
		[]string{"gpt-5.6-terra"},
		FilterModelsByStrictWhitelist([]string{"gpt-*"}, []string{"gpt-5.6-terra"}),
	)
	require.Empty(t,
		FilterModelsByStrictWhitelist([]string{"gpt-*"}, []string{"gpt-*"}),
		"selected wildcard IDs must not expand against discovery patterns",
	)
	require.Empty(t, FilterModelsByStrictWhitelist([]string{"gpt-5.6-terra"}, nil))
}

func TestPrioritizeModelCandidateMovesTerraToFront(t *testing.T) {
	require.Equal(
		t,
		[]string{"gpt-5.6-terra", "gpt-5.4", "gpt-5.5"},
		prioritizeModelCandidate([]string{"gpt-5.4", "gpt-5.5", "gpt-5.6-terra"}, "GPT-5.6-TERRA"),
	)
	require.Equal(
		t,
		[]string{"gpt-5.6-terra", "gpt-5.4"},
		prioritizeModelCandidate([]string{"gpt-5.4"}, "gpt-5.6-terra"),
	)
}
