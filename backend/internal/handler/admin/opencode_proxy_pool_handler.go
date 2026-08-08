package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const openCodeAdminOperationTimeout = 10 * time.Minute

// Long-running subscription and probe operations must finish persisting their
// results even if the browser or a reverse proxy closes the HTTP request. The
// bounded detached context retains request values but does not inherit client
// cancellation.
func openCodeAdminOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), openCodeAdminOperationTimeout)
}

type createProxySubscriptionRequest struct {
	Name                string `json:"name" binding:"required"`
	URL                 string `json:"url" binding:"required"`
	Enabled             *bool  `json:"enabled"`
	SyncIntervalMinutes int    `json:"sync_interval_minutes" binding:"omitempty,min=5,max=43200"`
}

type updateProxySubscriptionRequest struct {
	Name                string  `json:"name" binding:"required"`
	URL                 *string `json:"url"`
	Enabled             bool    `json:"enabled"`
	SyncIntervalMinutes int     `json:"sync_interval_minutes" binding:"omitempty,min=5,max=43200"`
}

type probeOpenCodeNodesRequest struct {
	NodeIDs []int64 `json:"node_ids"`
}

type updateOpenCodePoolRequest struct {
	Enabled             *bool   `json:"enabled"`
	IncludeServerDirect *bool   `json:"include_server_direct"`
	WorkerConcurrency   *int    `json:"worker_concurrency" binding:"omitempty,min=1,max=100"`
	UpstreamAPIKey      *string `json:"upstream_api_key"`
	ClearUpstreamAPIKey bool    `json:"clear_upstream_api_key"`
}

func (h *ProxyHandler) requireOpenCodeProxyPool(c *gin.Context) *service.OpenCodeProxyPoolService {
	if h.openCodeProxyPool == nil {
		response.Error(c, http.StatusServiceUnavailable, "OpenCode proxy pool is not configured")
		return nil
	}
	return h.openCodeProxyPool
}

func writeOpenCodeProxyPoolError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrProxySubscriptionNotFound):
		response.NotFound(c, err.Error())
	case errors.Is(err, service.ErrProxySubscriptionInUse), errors.Is(err, service.ErrOpenCodeDuplicateExitIP):
		response.Error(c, http.StatusConflict, err.Error())
	default:
		response.ErrorFrom(c, err)
	}
}

func (h *ProxyHandler) ListProxySubscriptions(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	items, err := pool.ListSubscriptions(c.Request.Context())
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *ProxyHandler) CreateProxySubscription(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	var req createProxySubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item, err := pool.CreateSubscription(c.Request.Context(), req.Name, req.URL, enabled, req.SyncIntervalMinutes)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	item.URLMasked = "***"
	item.HasURL = true
	response.Created(c, item)
}

func (h *ProxyHandler) UpdateProxySubscription(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription ID")
		return
	}
	var req updateProxySubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	item, err := pool.UpdateSubscription(c.Request.Context(), id, req.Name, req.URL, req.Enabled, req.SyncIntervalMinutes)
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	item.URLMasked = "***"
	item.HasURL = true
	response.Success(c, item)
}

func (h *ProxyHandler) DeleteProxySubscription(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription ID")
		return
	}
	if err := pool.DeleteSubscription(c.Request.Context(), id); err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Proxy subscription deleted"})
}

func (h *ProxyHandler) SyncProxySubscription(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription ID")
		return
	}
	operationCtx, cancel := openCodeAdminOperationContext(c.Request.Context())
	defer cancel()
	items, err := pool.SyncSubscription(operationCtx, id)
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *ProxyHandler) ListProxySubscriptionNodes(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid subscription ID")
		return
	}
	items, err := pool.ListNodes(c.Request.Context(), &id)
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *ProxyHandler) ProbeOpenCodeProxies(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	var req probeOpenCodeNodesRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	operationCtx, cancel := openCodeAdminOperationContext(c.Request.Context())
	defer cancel()
	items, err := pool.ProbeNodes(operationCtx, req.NodeIDs)
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, items)
}

func (h *ProxyHandler) GetOpenCodeModels(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	response.Success(c, pool.ModelStatus())
}

func (h *ProxyHandler) RefreshOpenCodeModels(c *gin.Context) {
	pool := h.requireOpenCodeProxyPool(c)
	if pool == nil {
		return
	}
	status, err := pool.RefreshModels(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusBadGateway, strings.TrimSpace(err.Error()))
		return
	}
	response.Success(c, status)
}

func (h *ProxyHandler) GetOpenCodePool(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	pool, err := poolService.GetPool(c.Request.Context())
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *ProxyHandler) UpdateOpenCodePool(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	var req updateOpenCodePoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := poolService.UpdatePool(c.Request.Context(), service.OpenCodePoolUpdate{
		Enabled:             req.Enabled,
		IncludeServerDirect: req.IncludeServerDirect,
		WorkerConcurrency:   req.WorkerConcurrency,
		UpstreamAPIKey:      req.UpstreamAPIKey,
		ClearUpstreamAPIKey: req.ClearUpstreamAPIKey,
	})
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *ProxyHandler) ListOpenCodePoolWorkers(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	workers, err := poolService.ListWorkers(c.Request.Context())
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, workers)
}

func (h *ProxyHandler) ReconcileOpenCodePool(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	workers, err := poolService.ReconcileWorkers(c.Request.Context())
	if err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, workers)
}

func (h *ProxyHandler) BindAPIKeyToOpenCodePool(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	apiKeyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || apiKeyID <= 0 {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	var boundBy *int64
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		value := subject.UserID
		boundBy = &value
	}
	if err := poolService.BindAPIKey(c.Request.Context(), apiKeyID, boundBy); err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, gin.H{"opencode_bound": true})
}

func (h *ProxyHandler) UnbindAPIKeyFromOpenCodePool(c *gin.Context) {
	poolService := h.requireOpenCodeProxyPool(c)
	if poolService == nil {
		return
	}
	apiKeyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || apiKeyID <= 0 {
		response.BadRequest(c, "Invalid API key ID")
		return
	}
	if err := poolService.UnbindAPIKey(c.Request.Context(), apiKeyID); err != nil {
		writeOpenCodeProxyPoolError(c, err)
		return
	}
	response.Success(c, gin.H{"opencode_bound": false})
}
