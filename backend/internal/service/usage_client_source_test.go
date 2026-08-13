package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectUsageClientSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      UsageClientSource
	}{
		{name: "codex cli", userAgent: "codex_cli_rs/0.144.1 (Windows 11; x86_64)", want: UsageClientSourceCodex},
		{name: "codex tui", userAgent: "codex-tui/0.144.1 (Mac OS X 15.1; arm64)", want: UsageClientSourceCodex},
		{name: "codex vscode", userAgent: "codex_vscode/0.140.2 (Mac OS X 14.0; arm64)", want: UsageClientSourceCodex},
		{name: "codex app", userAgent: "codex_app/2.1.0", want: UsageClientSourceCodex},
		{name: "codex desktop family", userAgent: "Codex Desktop/1.2.3", want: UsageClientSourceCodex},
		{name: "claude cli", userAgent: "claude-cli/2.1.22 (external, cli)", want: UsageClientSourceClaude},
		{name: "claude cli case insensitive", userAgent: "  Claude-CLI/2.1.22  ", want: UsageClientSourceClaude},
		{name: "claude cli family prefix", userAgent: "claude-cli/dev", want: UsageClientSourceClaude},
		{name: "claude code codex plugin", userAgent: "Claude Code/0.5.0 (Macos 15.5; arm64) iTerm2.app (Claude Code; 1.0.4)", want: UsageClientSourceClaude},
		{name: "claude takes precedence over codex trailer", userAgent: "Claude Code/0.5.0 (Macos) (codex_cli_rs; 0.144.1)", want: UsageClientSourceClaude},
		{name: "pi anthropic js sdk", userAgent: "Anthropic/JS 0.91.1", want: UsageClientSourcePi},
		{name: "pi anthropic js sdk case insensitive", userAgent: "  anthropic/js 0.91.1  ", want: UsageClientSourcePi},
		{name: "empty", userAgent: "", want: UsageClientSourceUnknown},
		{name: "unrecognized", userAgent: "curl/8.0", want: UsageClientSourceUnknown},
		{name: "anthropic js token in middle is not pi", userAgent: "Mozilla/5.0 Anthropic/JS 0.91.1", want: UsageClientSourceUnknown},
		{name: "codex token in middle is not an official client", userAgent: "Mozilla/5.0 codex_cli_rs/0.144.1", want: UsageClientSourceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, DetectUsageClientSource(tt.userAgent))
		})
	}
}
