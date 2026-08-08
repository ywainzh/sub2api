package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeChatCompletionsMigrationContract(t *testing.T) {
	raw, err := FS.ReadFile("196_force_opencode_chat_completions.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "update accounts")
	require.Contains(t, sql, "jsonb_set")
	require.Contains(t, sql, "coalesce(extra, '{}'::jsonb)")
	require.Contains(t, sql, "extra->>'provider_mode'")
	require.Contains(t, sql, "'opencode_zen'")
	require.Contains(t, sql, "'force_chat_completions'")
	require.Contains(t, sql, "is distinct from 'force_chat_completions'")
	require.Contains(t, sql, "deleted_at is null")
}
