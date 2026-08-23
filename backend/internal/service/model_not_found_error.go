package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

var upstreamModelNotFoundKeywords = []string{"model not found", "unknown model", "not found"}

func isUpstreamModelNotFoundError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" || !strings.Contains(normalized, "model") {
		return false
	}
	return containsModelNotFoundKeyword(normalized)
}

func isModelNotFoundError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) || statusCode == http.StatusNotFound
}

// openAICodexPlanGatedModelPhrase matches the deterministic Codex 400 returned
// when a ChatGPT OAuth account's plan cannot serve the requested model, e.g.
// {"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}
// The phrase is compared against the normalized body (lowercased, "_"/"-"
// folded to spaces), so it also matches the same message embedded in
// error.message-style payloads.
const openAICodexPlanGatedModelPhrase = "model is not supported when using codex"

// isOpenAICodexPlanGatedModelError reports whether the upstream response is the
// deterministic Codex rejection of a plan-gated model on a ChatGPT account.
// Unlike transient failures, retrying the same account cannot succeed until the
// account's plan changes, so callers should treat it like model-not-found and
// cool the (account, model) pair down instead of re-selecting the account.
func isOpenAICodexPlanGatedModelError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, openAICodexPlanGatedModelPhrase)
}

// openCodeZenModelErrorType 是 OpenCode Zen 在模型不可路由时返回的 error.type。
// Zen 用 401 同时表达三种含义：AuthError（凭据失效）、CreditsError（余额不足）、
// ModelError（模型不存在）。只有前两者该禁用账号。
const openCodeZenModelErrorType = "modelerror"

// isOpenCodeZenModelRejection 判定这个 401 是不是 OpenCode Zen 的「模型不支持」。
//
// 事故背景：注册表里混进了 Go tier 的 ox-alpha-free，Zen 侧返回
// 401 {"error":{"type":"ModelError","message":"Model ox-alpha-free is not supported"}}，
// 被 handleAuthError 当成凭据失效永久禁用账号，failover 循环逐个换号，33 秒烧掉 11 个。
// 按 error.type 区分后，这类 401 只冷却「账号×模型」，爆炸半径收敛到一对。
//
// 优先精确读 error.type；只有在字段缺失时才退回归一化子串匹配，避免对整个 body
// 做 Contains 把带有相似措辞的 AuthError 误判成模型问题。
func isOpenCodeZenModelRejection(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	if errorType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.type").String())); errorType != "" {
		return errorType == openCodeZenModelErrorType
	}
	normalized := normalizeModelNotFoundBody(body)
	return normalized != "" &&
		strings.Contains(normalized, "model") &&
		strings.Contains(normalized, "is not supported")
}

func containsModelNotFoundKeyword(normalizedBody string) bool {
	if normalizedBody == "" {
		return false
	}
	for _, keyword := range upstreamModelNotFoundKeywords {
		if strings.Contains(normalizedBody, keyword) {
			return true
		}
	}
	return false
}

func normalizeModelNotFoundBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	normalized := strings.ToLower(string(body))
	normalized = strings.NewReplacer("_", " ", "-", " ", "\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}
