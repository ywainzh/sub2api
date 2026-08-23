package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrOpenCodeModelNotAllowed = errors.New("opencode model is not in the free model registry")

var openCodeKnownFreeModels = []string{
	"big-pickle",
	"deepseek-v4-flash-free",
	"laguna-s-2.1-free",
	"ling-3.0-flash-free",
	"longcat-2.0-free",
	"mimo-v2.5-free",
	"nemotron-3-ultra-free",
	"north-mini-code-free",
}

type openCodeFreeModelRegistry struct {
	mu  sync.RWMutex
	ids map[string]struct{}
}

var defaultOpenCodeFreeModels = newOpenCodeFreeModelRegistry(openCodeKnownFreeModels)

func newOpenCodeFreeModelRegistry(ids []string) *openCodeFreeModelRegistry {
	r := &openCodeFreeModelRegistry{}
	r.Replace(ids)
	return r
}

func normalizeOpenCodeModelID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, ch := range value {
		isAlphaNum := ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.'
		if isAlphaNum {
			_, _ = b.WriteRune(ch)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			_ = b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (r *openCodeFreeModelRegistry) Replace(ids []string) {
	next := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if normalized := normalizeOpenCodeModelID(id); normalized != "" {
			next[normalized] = struct{}{}
		}
	}
	if len(next) == 0 {
		return
	}
	r.mu.Lock()
	r.ids = next
	r.mu.Unlock()
}

// ReplaceStrict 无条件安装新集合，并返回相对旧集合的增删差异。
// 与 Replace 不同，空输入会清空注册表而非静默保留旧值，因此只能在两个上游
// 目录来源都确认成功后调用；来源失败时应保留旧集合，不要走这里。
func (r *openCodeFreeModelRegistry) ReplaceStrict(ids []string) (added, removed []string) {
	next := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if normalized := normalizeOpenCodeModelID(id); normalized != "" {
			next[normalized] = struct{}{}
		}
	}
	r.mu.Lock()
	for id := range next {
		if _, ok := r.ids[id]; !ok {
			added = append(added, id)
		}
	}
	for id := range r.ids {
		if _, ok := next[id]; !ok {
			removed = append(removed, id)
		}
	}
	r.ids = next
	r.mu.Unlock()
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func (r *openCodeFreeModelRegistry) Has(id string) bool {
	r.mu.RLock()
	_, ok := r.ids[normalizeOpenCodeModelID(id)]
	r.mu.RUnlock()
	return ok
}

func (r *openCodeFreeModelRegistry) IDs() []string {
	r.mu.RLock()
	ids := make([]string, 0, len(r.ids))
	for id := range r.ids {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

var openCodeThinkingModelPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)deepseek`),
	regexp.MustCompile(`(?i)\bkimi\b`),
	regexp.MustCompile(`(?i)\bk2\b`),
	regexp.MustCompile(`(?i)\bminimax\b`),
	regexp.MustCompile(`(?i)\bmimo\b`),
}

var openCodeEffortAliases = map[string]struct {
	model  string
	effort string
}{
	"deepseek-v4-pro-low":    {"deepseek-v4-pro", "low"},
	"deepseek-v4-pro-medium": {"deepseek-v4-pro", "medium"},
	"deepseek-v4-pro-high":   {"deepseek-v4-pro", "high"},
	"deepseek-v4-pro-max":    {"deepseek-v4-pro", "max"},
	"deepseek-v4-flash-high": {"deepseek-v4-flash", "high"},
	"deepseek-v4-flash-max":  {"deepseek-v4-flash", "max"},
	"glm-5.2-high":           {"glm-5.2", "high"},
	"glm-5.2-max":            {"glm-5.2", "max"},
	"mimo-v2.5-high":         {"mimo-v2.5", "high"},
	"mimo-v2.5-max":          {"mimo-v2.5", "max"},
}

// deadOpenCodeEffortAliases 返回基础模型已不在免费注册表里的推理强度别名。
// 这类别名会在 transformOpenCodeZenChatBody 里以 ErrOpenCodeModelNotAllowed 失败关闭
// （403，不是烧号的 401），所以不做特例放行；只在启动时打一条日志，
// 让运维能看见别名表与上游目录已经脱节，而不是等用户来报 403。
func deadOpenCodeEffortAliases() []string {
	dead := make([]string, 0, len(openCodeEffortAliases))
	for alias, target := range openCodeEffortAliases {
		if !defaultOpenCodeFreeModels.Has(target.model) {
			dead = append(dead, alias+"->"+target.model)
		}
	}
	sort.Strings(dead)
	return dead
}

func transformOpenCodeZenChatBody(body []byte, model string, stream bool) ([]byte, error) {
	model = normalizeOpenCodeModelID(model)
	alias, isAlias := openCodeEffortAliases[model]
	registryModel := model
	if isAlias {
		registryModel = alias.model
	}
	if !defaultOpenCodeFreeModels.Has(registryModel) {
		return nil, ErrOpenCodeModelNotAllowed
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = model
	payload["stream"] = stream
	delete(payload, "client_metadata")
	if tools, ok := payload["tools"].([]any); ok && len(tools) > 128 {
		payload["tools"] = tools[:128]
	}
	if isAlias {
		payload["model"] = alias.model
		if _, exists := payload["reasoning_effort"]; !exists {
			payload["reasoning_effort"] = alias.effort
		}
		model = alias.model
	}
	thinking := false
	for _, pattern := range openCodeThinkingModelPatterns {
		if pattern.MatchString(model) {
			thinking = true
			break
		}
	}
	if thinking {
		if messages, ok := payload["messages"].([]any); ok {
			for _, raw := range messages {
				message, ok := raw.(map[string]any)
				if !ok || message["role"] != "assistant" {
					continue
				}
				if reasoning, ok := message["reasoning_content"].(string); !ok || strings.TrimSpace(reasoning) == "" {
					message["reasoning_content"] = " "
				}
			}
		}
	}
	return json.Marshal(payload)
}

func normalizeOpenCodeResponsesStringInput(body []byte) ([]byte, bool, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false, err
	}
	inputRaw, exists := payload["input"]
	if !exists {
		return body, false, nil
	}
	var inputText string
	if err := json.Unmarshal(inputRaw, &inputText); err != nil {
		return body, false, nil
	}
	normalizedInput, err := json.Marshal([]map[string]any{{
		"role":    "user",
		"content": inputText,
	}})
	if err != nil {
		return body, false, err
	}
	payload["input"] = normalizedInput
	normalizedBody, err := json.Marshal(payload)
	if err != nil {
		return body, false, err
	}
	return normalizedBody, true, nil
}

func applyOpenCodeZenHeaders(headers http.Header, clientHeaders http.Header) {
	for _, key := range []string{"x-opencode-session", "x-opencode-request", "x-opencode-project", "x-opencode-client", "x-session-id", "x-title"} {
		for clientKey, values := range clientHeaders {
			if strings.EqualFold(clientKey, key) && len(values) > 0 && strings.TrimSpace(values[0]) != "" {
				headers.Set(key, values[0])
				break
			}
		}
	}
	if headers.Get("x-opencode-session") == "" {
		headers.Set("x-opencode-session", uuid.NewString())
	}
	if headers.Get("x-opencode-request") == "" {
		headers.Set("x-opencode-request", uuid.NewString())
	}
	if headers.Get("x-opencode-client") == "" {
		headers.Set("x-opencode-client", "cli")
	}
	if headers.Get("x-opencode-project") == "" {
		headers.Set("x-opencode-project", "default")
	}
}

func parseOpenCode429ResetAt(headers http.Header, body []byte, now time.Time) *time.Time {
	if resetAt, ok := parseOpenCodeResetValue(headers.Get("Retry-After"), now, true); ok {
		return &resetAt
	}

	var latest time.Time
	for _, key := range []string{"x-ratelimit-reset", "x-ratelimit-reset-requests", "x-ratelimit-reset-tokens", "ratelimit-reset"} {
		if resetAt, ok := parseOpenCodeResetValue(headers.Get(key), now, false); ok && resetAt.After(latest) {
			latest = resetAt
		}
	}
	if !latest.IsZero() {
		return &latest
	}

	var payload any
	if json.Unmarshal(body, &payload) != nil {
		return nil
	}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				normalized := strings.ToLower(strings.TrimSpace(key))
				relative := normalized == "retry_after" || normalized == "retry_after_seconds" || normalized == "resets_in_seconds"
				if relative || normalized == "reset" || normalized == "reset_at" || normalized == "resets_at" {
					if resetAt, ok := parseOpenCodeResetValue(strings.TrimSpace(toOpenCodeResetString(nested)), now, relative); ok && resetAt.After(latest) {
						latest = resetAt
					}
				}
				walk(nested)
			}
		case []any:
			for _, nested := range typed {
				walk(nested)
			}
		}
	}
	walk(payload)
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func toOpenCodeResetString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func parseOpenCodeResetValue(raw string, now time.Time, relative bool) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := http.ParseTime(raw); err == nil && parsed.After(now) {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil && parsed.After(now) {
		return parsed, true
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return now.Add(duration), true
	}
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil || number <= 0 {
		return time.Time{}, false
	}
	if relative || number < float64(now.Unix()) {
		return now.Add(time.Duration(number * float64(time.Second))), true
	}
	if number > 1e12 {
		number /= 1000
	}
	resetAt := time.Unix(int64(number), 0)
	return resetAt, resetAt.After(now)
}
