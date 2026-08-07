package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

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
	items, err := pool.SyncSubscription(c.Request.Context(), id)
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
	items, err := pool.ProbeNodes(c.Request.Context(), req.NodeIDs)
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
