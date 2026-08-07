package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeWorkerPoolMigrationContract(t *testing.T) {
	raw, err := FS.ReadFile("195_opencode_worker_pool.sql")
	require.NoError(t, err)
	sql := strings.ToLower(string(raw))

	require.Contains(t, sql, "create table if not exists opencode_pools")
	require.Contains(t, sql, "create table if not exists opencode_pool_workers")
	require.Contains(t, sql, "create table if not exists api_key_opencode_bindings")
	require.Contains(t, sql, "unique (singleton_key)")
	require.Contains(t, sql, "unique (account_id)")
	require.Contains(t, sql, "opencode_pool_workers_single_direct")
	require.Contains(t, sql, "opencode_pool_workers_node_mode_check")
	require.Contains(t, sql, "primary key references api_keys(id) on delete cascade")
	require.Contains(t, sql, "upstream_api_key_ciphertext")
	require.NotContains(t, sql, "upstream_api_key text")
	require.Contains(t, sql, "trg_sync_opencode_worker_account_cooldown")
}
