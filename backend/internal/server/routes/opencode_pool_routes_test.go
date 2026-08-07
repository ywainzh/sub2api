package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routeOpenCodePoolRepoStub struct {
	service.OpenCodeProxyPoolRepository
	pool *service.OpenCodePool
}

func (s *routeOpenCodePoolRepoStub) EnsureDefaultPool(context.Context) (*service.OpenCodePool, error) {
	clone := *s.pool
	return &clone, nil
}

type routeOpenCodeGroupRepoStub struct {
	service.GroupRepository
	group *service.Group
}

func (s *routeOpenCodeGroupRepoStub) GetByID(context.Context, int64) (*service.Group, error) {
	clone := *s.group
	return &clone, nil
}

func newRouteOpenCodePoolService(t *testing.T, activeWorkers int) (*service.OpenCodeProxyPoolService, int64, int64) {
	t.Helper()
	poolID, groupID := int64(1), int64(9)
	svc := service.NewOpenCodeProxyPoolService(
		&routeOpenCodePoolRepoStub{pool: &service.OpenCodePool{
			ID: poolID, GroupID: groupID, Enabled: true, ActiveWorkers: activeWorkers,
		}},
		nil, nil, nil,
		&routeOpenCodeGroupRepoStub{group: &service.Group{
			ID: groupID, Name: "OpenCode Zen Pool", Platform: service.PlatformOpenAI, RateMultiplier: 0,
		}},
	)
	_, err := svc.BootstrapPool(context.Background())
	require.NoError(t, err)
	return svc, poolID, groupID
}

func TestIsOpenCodeHTTPPoolEndpoint(t *testing.T) {
	for _, path := range []string{
		"/v1/chat/completions",
		"/chat/completions",
		"/v1/messages",
		"/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		require.Truef(t, isOpenCodeHTTPPoolEndpoint(path), "expected %s to use the OpenCode pool", path)
	}

	for _, path := range []string{
		"/v1/embeddings",
		"/embeddings",
		"/v1/messages/count_tokens",
		"/images/generations",
		"/backend-api/codex/realtime/calls",
	} {
		require.Falsef(t, isOpenCodeHTTPPoolEndpoint(path), "expected %s to be rejected for OpenCode models", path)
	}
}

func TestOpenCodeEffectiveGroupMiddlewareSwitchesFreeChatModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, poolID, poolGroupID := newRouteOpenCodePoolService(t, 2)
	freeModels := pool.FreeModelIDs()
	require.NotEmpty(t, freeModels)
	originalGroupID := int64(5)
	apiKey := &service.APIKey{
		ID: 3, GroupID: &originalGroupID, OpenCodePoolID: &poolID,
		Group: &service.Group{ID: originalGroupID, Platform: service.PlatformOpenAI},
		User:  &service.User{ID: 7},
	}

	recorder := httptest.NewRecorder()
	reached := false
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Next()
	}, openCodeEffectiveGroupMiddleware(pool), func(c *gin.Context) {
		effective, ok := middleware.GetAPIKeyFromContext(c)
		require.True(t, ok)
		require.Equal(t, poolGroupID, *effective.GroupID)
		reached = true
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"`+freeModels[0]+`"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.True(t, reached)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestOpenCodeEffectiveGroupMiddlewareRejectsUnsupportedEndpointWithoutFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, poolID, _ := newRouteOpenCodePoolService(t, 1)
	freeModels := pool.FreeModelIDs()
	originalGroupID := int64(5)
	apiKey := &service.APIKey{
		GroupID: &originalGroupID, OpenCodePoolID: &poolID,
		Group: &service.Group{ID: originalGroupID, Platform: service.PlatformOpenAI},
		User:  &service.User{ID: 7},
	}

	recorder := httptest.NewRecorder()
	reached := false
	engine := gin.New()
	engine.POST("/v1/embeddings", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Next()
	}, openCodeEffectiveGroupMiddleware(pool), func(*gin.Context) { reached = true })
	request := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(`{"model":"`+freeModels[0]+`"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.False(t, reached)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "unsupported_endpoint")
}

func TestOpenCodeEffectiveGroupMiddlewareReturnsUnavailableInsteadOfOriginalGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, poolID, _ := newRouteOpenCodePoolService(t, 0)
	freeModels := pool.FreeModelIDs()
	originalGroupID := int64(5)
	apiKey := &service.APIKey{
		GroupID: &originalGroupID, OpenCodePoolID: &poolID,
		Group: &service.Group{ID: originalGroupID, Platform: service.PlatformOpenAI},
		User:  &service.User{ID: 7},
	}

	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Next()
	}, openCodeEffectiveGroupMiddleware(pool), func(*gin.Context) { t.Fatal("must not fall back to original group") })
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"`+freeModels[0]+`"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "opencode_pool_unavailable")
}
