package migrations

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeHTTPImportMaintenanceMigration(t *testing.T) {
	contents, err := os.ReadFile("197_opencode_http_import_maintenance.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(contents))
	require.Contains(t, sql, "transport_mode")
	require.Contains(t, sql, "direct_http")
	require.Contains(t, sql, "opencode_node_tombstones")
	require.Contains(t, sql, "opencode_maintenance_jobs")
	require.Contains(t, sql, "unavailable_since")
	require.NotContains(t, sql, "delete from managed_proxy_nodes")
}
