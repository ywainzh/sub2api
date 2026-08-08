package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func withOpenCodeFreeModels(t *testing.T, models ...string) {
	t.Helper()
	previous := defaultOpenCodeFreeModels.IDs()
	defaultOpenCodeFreeModels.Replace(models)
	t.Cleanup(func() { defaultOpenCodeFreeModels.Replace(previous) })
}

func TestHydrateOpenCodePoolExposesAutomaticProbeInterval(t *testing.T) {
	pool, err := (&OpenCodeProxyPoolService{}).hydrateOpenCodePool(t.Context(), &OpenCodePool{})
	require.NoError(t, err)
	require.Equal(t, 15, pool.ProbeIntervalMinutes)
}

func TestResolveEffectiveAPIKeyUsesOpenCodePoolForFreeModel(t *testing.T) {
	withOpenCodeFreeModels(t, "big-pickle")
	poolID, originalGroupID, poolGroupID := int64(1), int64(5), int64(9)
	rpmOverride := 88
	svc := &OpenCodeProxyPoolService{poolStatus: &OpenCodePool{
		ID: poolID, Enabled: true, ActiveWorkers: 2,
		Group: &Group{ID: poolGroupID, Name: "OpenCode Zen Pool", Platform: PlatformOpenAI, RateMultiplier: 0},
	}}
	key := &APIKey{
		ID: 3, UserID: 7, GroupID: &originalGroupID, OpenCodePoolID: &poolID,
		Group: &Group{ID: originalGroupID, Name: "Original", Platform: PlatformOpenAI, RateMultiplier: 1},
		User:  &User{ID: 7, UserGroupRPMOverride: &rpmOverride},
	}

	effective, matched, err := svc.ResolveEffectiveAPIKey(key, "big-pickle")
	require.NoError(t, err)
	require.True(t, matched)
	require.NotSame(t, key, effective)
	require.Equal(t, poolGroupID, *effective.GroupID)
	require.Equal(t, float64(0), effective.Group.RateMultiplier)
	require.Nil(t, effective.User.UserGroupRPMOverride)
	require.Equal(t, int64(3), effective.ID, "billing identity must remain the original API key")
	require.Equal(t, originalGroupID, *key.GroupID, "the authenticated key must not be mutated")
}

func TestResolveEffectiveAPIKeyDoesNotSwitchNonFreeModel(t *testing.T) {
	withOpenCodeFreeModels(t, "big-pickle")
	poolID := int64(1)
	key := &APIKey{OpenCodePoolID: &poolID}
	effective, matched, err := (&OpenCodeProxyPoolService{}).ResolveEffectiveAPIKey(key, "paid-model")
	require.NoError(t, err)
	require.False(t, matched)
	require.Same(t, key, effective)
}

func TestResolveEffectiveAPIKeyRejectsUnavailablePoolWithoutFallback(t *testing.T) {
	withOpenCodeFreeModels(t, "big-pickle")
	poolID := int64(1)
	svc := &OpenCodeProxyPoolService{poolStatus: &OpenCodePool{ID: poolID, Enabled: true, ActiveWorkers: 0}}

	effective, matched, err := svc.ResolveEffectiveAPIKey(&APIKey{OpenCodePoolID: &poolID}, "big-pickle")
	require.Nil(t, effective)
	require.True(t, matched)
	require.True(t, errors.Is(err, ErrOpenCodePoolUnavailable))
}

func TestAppendFreeModelsForBoundKeyReturnsDeduplicatedUnion(t *testing.T) {
	withOpenCodeFreeModels(t, "big-pickle", "deepseek-free")
	poolID := int64(1)
	svc := &OpenCodeProxyPoolService{poolStatus: &OpenCodePool{ID: poolID, Enabled: true}}

	models := svc.AppendFreeModelsForAPIKey(&APIKey{OpenCodePoolID: &poolID}, []string{"existing", "BIG-PICKLE"})
	require.Equal(t, []string{"existing", "BIG-PICKLE", "deepseek-free"}, models)
}
