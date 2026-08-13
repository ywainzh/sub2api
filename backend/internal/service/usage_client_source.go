package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

// UsageClientSource describes the inbound client family recorded for a usage log.
type UsageClientSource string

const (
	UsageClientSourceCodex   UsageClientSource = "codex"
	UsageClientSourceClaude  UsageClientSource = "claude"
	UsageClientSourcePi      UsageClientSource = "pi"
	UsageClientSourceUnknown UsageClientSource = "unknown"
)

// DetectUsageClientSource derives the inbound client family from the persisted User-Agent.
// Claude is checked first so Claude Code's Codex plugin remains attributed to its outer client.
func DetectUsageClientSource(userAgent string) UsageClientSource {
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		return UsageClientSourceUnknown
	}

	normalized := strings.ToLower(ua)
	// Pi's Anthropic API-key/custom-provider path inherits the official
	// Anthropic JavaScript SDK User-Agent instead of sending a Pi-specific one.
	if strings.HasPrefix(normalized, "anthropic/js ") {
		return UsageClientSourcePi
	}
	if strings.HasPrefix(normalized, "claude-cli/") || strings.HasPrefix(normalized, "claude code/") {
		return UsageClientSourceClaude
	}
	if openai.IsCodexOfficialClientRequestStrict(ua) {
		return UsageClientSourceCodex
	}
	return UsageClientSourceUnknown
}
