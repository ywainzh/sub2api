package migrations

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeImportExpirationMigration(t *testing.T) {
	contents, err := os.ReadFile("198_opencode_import_expiration.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(contents))
	require.Contains(t, sql, "expires_at timestamptz")
	require.Contains(t, sql, "source_type = 'upload'")
	require.Contains(t, sql, "proxy_subscriptions_upload_expiration")
	require.NotContains(t, sql, "delete from")
}
