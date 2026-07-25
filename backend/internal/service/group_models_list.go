package service

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

const ModelNotAllowedErrorCode = "model_not_allowed"

func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{Enabled: cfg.Enabled, Enforce: cfg.Enforce}
	if len(cfg.Models) == 0 {
		return out
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	out.Models = make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := model
		if cfg.Enforce {
			key = strings.ToLower(model)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	return out
}

func (g *Group) CustomModelsListEnabled() bool {
	return g != nil && g.ModelsListConfig.Enabled && len(g.ModelsListConfig.Models) > 0
}

// ModelWhitelistEnabled reports whether this group enforces its selected
// model IDs for OpenAI gateway requests. The setting is deliberately a no-op
// for every non-OpenAI platform so old configurations remain harmless.
func (g *Group) ModelWhitelistEnabled() bool {
	return g != nil && g.Platform == PlatformOpenAI && g.ModelsListConfig.Enforce
}

// IsModelAllowed performs the strict model comparison used by request
// validation. Matching is exact after trimming whitespace and is
// case-insensitive; no wildcard expansion or model aliasing is performed.
func (g *Group) IsModelAllowed(model string) bool {
	if !g.ModelWhitelistEnabled() {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, allowed := range g.ModelsListConfig.Models {
		allowed = strings.TrimSpace(allowed)
		// Strict entries are concrete model IDs, never patterns. Keep wildcard
		// syntax useful only to the legacy display-list feature; it must not be
		// accepted even when a client sends the wildcard text literally.
		if allowed == "" || strings.Contains(allowed, "*") {
			continue
		}
		if strings.EqualFold(allowed, model) {
			return true
		}
	}
	return false
}

// ValidateOpenAIGroupModel rejects a model before any account routing or
// billing work begins. A nil/non-OpenAI group and groups with enforcement
// disabled intentionally pass through unchanged.
func ValidateOpenAIGroupModel(group *Group, requestedModel string) error {
	if group == nil || !group.ModelWhitelistEnabled() {
		return nil
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if group.IsModelAllowed(requestedModel) {
		return nil
	}
	if requestedModel == "" {
		return infraerrors.New(http.StatusBadRequest, ModelNotAllowedErrorCode, "model is not allowed for this group")
	}
	return infraerrors.Newf(http.StatusBadRequest, ModelNotAllowedErrorCode, "model %q is not allowed for this group", requestedModel)
}

// ValidateOpenAIGroupModelPayload applies the same strict model policy while
// also rejecting duplicate JSON object keys along the client-facing model
// path.  gjson intentionally returns the first duplicate key, whereas the
// standard JSON decoder (and many upstream servers) commonly retain the last
// one.  Without this guard a payload such as {"model":"allowed",
// "model":"forbidden"} could pass the local check and be interpreted as a
// different model later in the request pipeline.
//
// modelPath is a dotted object path such as "model" or "session.model".  The
// duplicate check is only needed for strict OpenAI groups; legacy groups keep
// their existing permissive behavior.
func ValidateOpenAIGroupModelPayload(group *Group, payload []byte, modelPath, requestedModel string) error {
	if group == nil || !group.ModelWhitelistEnabled() {
		return nil
	}
	if hasDuplicateJSONPathField(payload, modelPath) {
		return infraerrors.New(http.StatusBadRequest, ModelNotAllowedErrorCode, "duplicate model fields are not allowed for this group")
	}
	return ValidateOpenAIGroupModel(group, requestedModel)
}

// HasDuplicateJSONPathField reports whether any object on the requested path
// contains the same key more than once.  It is exported for WebSocket ingress
// code, which performs the same validation before model mapping.  The caller
// is expected to validate JSON syntax separately; malformed payloads simply
// return false and are handled by the normal protocol parser.
func HasDuplicateJSONPathField(payload []byte, path string) bool {
	return hasDuplicateJSONPathField(payload, path)
}

func hasDuplicateJSONPathField(payload []byte, path string) bool {
	path = strings.TrimSpace(path)
	if len(payload) == 0 || path == "" {
		return false
	}
	current := gjson.ParseBytes(payload)
	for _, segment := range strings.Split(path, ".") {
		if !current.IsObject() {
			return false
		}
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return false
		}
		count := 0
		var next gjson.Result
		current.ForEach(func(key, value gjson.Result) bool {
			if key.Type == gjson.String && key.String() == segment {
				count++
				if count == 1 {
					next = value
				}
			}
			return true
		})
		if count > 1 {
			return true
		}
		if count == 0 {
			return false
		}
		current = next
	}
	return false
}

// FilterModelsByStrictWhitelist intersects an existing discovery result with
// the configured exact model IDs. The discovery source order is retained, and
// wildcard entries in the source are only expanded to a configured concrete
// model ID; wildcard entries in the configured allowlist never match.
func FilterModelsByStrictWhitelist(available, selected []string) []string {
	if len(selected) == 0 || len(available) == 0 {
		return []string{}
	}
	selectedByKey := make(map[string]string, len(selected))
	selectedOrder := make([]string, 0, len(selected))
	for _, model := range selected {
		model = strings.TrimSpace(model)
		if model == "" || strings.Contains(model, "*") {
			continue
		}
		key := strings.ToLower(model)
		if _, exists := selectedByKey[key]; exists {
			continue
		}
		selectedByKey[key] = model
		selectedOrder = append(selectedOrder, model)
	}
	if len(selectedByKey) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(selectedByKey))
	filtered := make([]string, 0, len(selectedByKey))
	for _, model := range available {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.HasSuffix(model, "*") {
			prefix := strings.ToLower(strings.TrimSuffix(model, "*"))
			for _, selectedModel := range selectedOrder {
				key := strings.ToLower(selectedModel)
				if !strings.HasPrefix(key, prefix) {
					continue
				}
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				filtered = append(filtered, selectedModel)
			}
			continue
		}
		key := strings.ToLower(model)
		if _, ok := selectedByKey[key]; !ok {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, model)
	}
	return filtered
}
