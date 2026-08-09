package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeWorkerExtraForcesChatCompletions(t *testing.T) {
	nodeID := int64(42)
	raw := openCodeWorkerExtra(7, "proxy", &nodeID)

	var extra map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &extra))
	require.Equal(t, string(openai_compat.ResponsesSupportModeForceChatCompletions), extra[openai_compat.ExtraKeyResponsesMode])
	require.Equal(t, "opencode_zen", extra["provider_mode"])
	require.Equal(t, float64(nodeID), extra["managed_node_id"])
}

func TestUsedListenerPortsIncludesSoftDeletedReservations(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery("SELECT listener_port FROM managed_proxy_nodes WHERE listener_port IS NOT NULL").
		WillReturnRows(sqlmock.NewRows([]string{"listener_port"}).AddRow(22000).AddRow(22021))

	repo := &openCodeProxyPoolRepository{db: db}
	ports, err := repo.UsedListenerPorts(context.Background())
	require.NoError(t, err)
	require.Contains(t, ports, 22000)
	require.Contains(t, ports, 22021)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenCodeHardProbeFailureClassification(t *testing.T) {
	for _, failureType := range []string{"tls", "dns", "timeout", "transport", "exit_probe", "auth"} {
		require.True(t, isOpenCodeHardProbeFailure(failureType), failureType)
	}
	for _, failureType := range []string{"rate_limited", "upstream_error", "duplicate_exit", "probe_infrastructure"} {
		require.False(t, isOpenCodeHardProbeFailure(failureType), failureType)
	}
}

func TestUpdateOpenCodeRateLimitDoesNotAdvanceHardFailureWindow(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	retryAt := time.Date(2026, time.August, 9, 9, 15, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE managed_proxy_nodes SET`).
		WithArgs(int64(42), "rate_limited", "", "", "", nil, http.StatusTooManyRequests,
			"rate_limited", "", nil, false, false, retryAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE proxies SET status=\$2`).
		WithArgs(int64(42), "disabled").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	repo := &openCodeProxyPoolRepository{db: db}
	err = repo.UpdateNodeProbeResults(context.Background(), []service.OpenCodeNodeProbeResult{{
		NodeID: 42, HealthStatus: "rate_limited", OpenCodeHTTPStatus: http.StatusTooManyRequests,
		FailureType: "rate_limited", RetryAt: &retryAt,
	}})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteManagedNodeCreatesTombstoneAndPhysicallyRemovesProxy(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT subscription_id, node_key, proxy_id FROM managed_proxy_nodes`).
		WithArgs(int64(28)).
		WillReturnRows(sqlmock.NewRows([]string{"subscription_id", "node_key", "proxy_id"}).AddRow(2, "node-key", 40))
	mock.ExpectExec(`INSERT INTO opencode_node_tombstones`).
		WithArgs(int64(2), "node-key", "manual").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(`SELECT account_id FROM opencode_pool_workers`).
		WithArgs(int64(28)).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}))
	mock.ExpectExec(`DELETE FROM opencode_egress_leases`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM opencode_pool_workers`).
		WithArgs(int64(28)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE managed_proxy_nodes SET duplicate_of_node_id=NULL`).
		WithArgs(int64(28)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM managed_proxy_nodes`).
		WithArgs(int64(28)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM proxies`).
		WithArgs(int64(40)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE proxy_subscriptions SET node_count=`).
		WithArgs(int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	repo := &openCodeProxyPoolRepository{db: db}
	require.NoError(t, repo.DeleteManagedNode(context.Background(), 28, "manual"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteExpiredUploadedSubscriptionsCleansWorkersLeasesAndProxies(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectQuery(`SELECT id FROM proxy_subscriptions`).
		WithArgs(now).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery(`SELECT id, proxy_id FROM managed_proxy_nodes`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "proxy_id"}).AddRow(28, 40))
	mock.ExpectQuery(`SELECT account_id FROM opencode_pool_workers`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(70))
	mock.ExpectQuery(`SELECT id FROM accounts WHERE proxy_id=ANY`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(70))
	mock.ExpectExec(`DELETE FROM opencode_egress_leases WHERE account_id=ANY`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE accounts SET status='disabled'`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM opencode_pool_workers WHERE managed_node_id=ANY`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM opencode_egress_leases WHERE proxy_id=ANY`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE accounts SET deleted_at=COALESCE`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO scheduler_outbox`).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE managed_proxy_nodes SET duplicate_of_node_id=NULL`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`DELETE FROM managed_proxy_nodes WHERE id=ANY`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM proxies WHERE id=ANY`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE proxy_subscriptions SET enabled=FALSE`).
		WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &openCodeProxyPoolRepository{db: db}
	result, err := repo.DeleteExpiredUploadedSubscriptions(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, service.ExpiredProxySourceCleanupResult{Subscriptions: 1, Nodes: 1, Accounts: 1}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteExpiredUploadedSubscriptionsSkipsWhenAnotherInstanceOwnsLock(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectCommit()

	repo := &openCodeProxyPoolRepository{db: db}
	result, err := repo.DeleteExpiredUploadedSubscriptions(context.Background(), time.Now())
	require.NoError(t, err)
	require.Zero(t, result.Subscriptions)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAcquireOpenCodeWorkerLeasePreservesExistingLeaseOrdering(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT exit_ip FROM opencode_egress_leases\s+WHERE account_id=\$1 FOR UPDATE`).
		WithArgs(int64(26)).
		WillReturnRows(sqlmock.NewRows([]string{"exit_ip"}).AddRow("2001:DB8::1"))
	mock.ExpectExec(`UPDATE opencode_egress_leases SET proxy_id=\$2, egress_mode=\$3,\s+checked_at=NOW\(\), updated_at=NOW\(\) WHERE account_id=\$1`).
		WithArgs(int64(26), nil, "server_direct").
		WillReturnResult(sqlmock.NewResult(0, 1))

	acquired, err := acquireOpenCodeWorkerLease(context.Background(), tx, 26, nil, "server_direct", "2001:db8::1")
	require.NoError(t, err)
	require.True(t, acquired)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAcquireOpenCodeWorkerLeaseChangedExitLosesToEarlierOwner(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{})
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT exit_ip FROM opencode_egress_leases\s+WHERE account_id=\$1 FOR UPDATE`).
		WithArgs(int64(27)).
		WillReturnRows(sqlmock.NewRows([]string{"exit_ip"}).AddRow("198.51.100.10"))
	mock.ExpectExec(`DELETE FROM opencode_egress_leases WHERE account_id=\$1`).
		WithArgs(int64(27)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO opencode_egress_leases`).
		WithArgs(int64(27), int64(123), "proxy", "203.0.113.20").
		WillReturnResult(sqlmock.NewResult(0, 0))

	proxyID := int64(123)
	acquired, err := acquireOpenCodeWorkerLease(context.Background(), tx, 27, &proxyID, "proxy", "203.0.113.20")
	require.NoError(t, err)
	require.False(t, acquired)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
