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
	raw := openCodeWorkerExtra(7, "proxy", &nodeID, service.OpenCodeLaneKeyed)

	var extra map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &extra))
	require.Equal(t, string(openai_compat.ResponsesSupportModeForceChatCompletions), extra[openai_compat.ExtraKeyResponsesMode])
	require.Equal(t, "opencode_zen", extra["provider_mode"])
	require.Equal(t, float64(nodeID), extra["managed_node_id"])
	require.Equal(t, service.OpenCodeLaneKeyed, extra[service.OpenCodeLaneExtraKey])

	anon := openCodeWorkerExtra(7, "proxy", &nodeID, service.OpenCodeLaneAnonymous)
	require.NoError(t, json.Unmarshal([]byte(anon), &extra))
	require.Equal(t, service.OpenCodeLaneAnonymous, extra[service.OpenCodeLaneExtraKey])
}

// 灰度子集必须按 node.id 稳定截断：合格节点按 latency_ms 排序，延迟每轮探测都在变，
// 直接取前 N 会让匿名 worker 在节点间漂移，每漂一次就制造一次 duplicate_exit_ip。
func TestOpenCodeAnonymousLaneNodesIsStableUnderLatencyReordering(t *testing.T) {
	byLatency := []openCodeEligibleNode{{id: 40}, {id: 11}, {id: 27}, {id: 3}}
	reordered := []openCodeEligibleNode{{id: 3}, {id: 27}, {id: 40}, {id: 11}}

	ids := func(nodes []openCodeEligibleNode) []int64 {
		out := make([]int64, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, node.id)
		}
		return out
	}

	require.Equal(t, []int64{3, 11}, ids(openCodeAnonymousLaneNodes(byLatency, 2)))
	require.Equal(t, []int64{3, 11}, ids(openCodeAnonymousLaneNodes(reordered, 2)))
	// limit <= 0 表示不限量，仍然按 id 排序。
	require.Equal(t, []int64{3, 11, 27, 40}, ids(openCodeAnonymousLaneNodes(byLatency, 0)))
	require.Equal(t, []int64{3, 11, 27, 40}, ids(openCodeAnonymousLaneNodes(byLatency, 99)))
	// 不得原地改动调用方切片。
	require.Equal(t, []int64{40, 11, 27, 3}, ids(byLatency))
}

func TestOpenCodeLaneUpstreamKeyBlanksAnonymous(t *testing.T) {
	require.Equal(t, "", openCodeLaneUpstreamKey(service.OpenCodeLaneAnonymous, "pool-key"))
	require.Equal(t, "pool-key", openCodeLaneUpstreamKey(service.OpenCodeLaneKeyed, "pool-key"))
}

func TestOpenCodeReconcileLanesRequiresBothEnabledAndSwitch(t *testing.T) {
	keyedOnly := []string{service.OpenCodeLaneKeyed}
	require.Equal(t, keyedOnly, openCodeReconcileLanes(service.OpenCodePool{Enabled: true}))
	require.Equal(t, keyedOnly, openCodeReconcileLanes(service.OpenCodePool{AnonymousLaneEnabled: true}))
	require.Equal(t, keyedOnly, openCodeReconcileLanes(service.OpenCodePool{}))
	require.Equal(t,
		[]string{service.OpenCodeLaneKeyed, service.OpenCodeLaneAnonymous},
		openCodeReconcileLanes(service.OpenCodePool{Enabled: true, AnonymousLaneEnabled: true}))
}

func TestOpenCodeWorkerPriorityUsesStableLatencyBuckets(t *testing.T) {
	tests := []struct {
		name      string
		latencyMs int64
		want      int
	}{
		{name: "unknown", latencyMs: 0, want: 50},
		{name: "negative", latencyMs: -1, want: 50},
		{name: "first bucket", latencyMs: 1, want: 1},
		{name: "bucket boundary", latencyMs: 1000, want: 1},
		{name: "second bucket", latencyMs: 1738, want: 2},
		{name: "fourth bucket", latencyMs: 3132, want: 4},
		{name: "default priority cap", latencyMs: 50001, want: 50},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, openCodeWorkerPriority(test.latencyMs, service.OpenCodeLaneAnonymous))
			require.Equal(t, test.want+openCodeWorkerDefaultPriority, openCodeWorkerPriority(test.latencyMs, service.OpenCodeLaneKeyed))
		})
	}
}

func TestOpenCodeWorkerPriorityRanksEveryAnonymousAheadOfEveryKeyed(t *testing.T) {
	slowestAnonymous := openCodeWorkerPriority(0, service.OpenCodeLaneAnonymous)
	fastestKeyed := openCodeWorkerPriority(1, service.OpenCodeLaneKeyed)
	require.Less(t, slowestAnonymous, fastestKeyed)

	// An unmarked lane must fall back to keyed, never to the preferred band.
	require.Equal(t, openCodeWorkerPriority(1738, service.OpenCodeLaneKeyed), openCodeWorkerPriority(1738, ""))
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
	mock.ExpectExec(`UPDATE opencode_egress_leases SET proxy_id=\$2, egress_mode=\$3, lane=\$4,\s+checked_at=NOW\(\), updated_at=NOW\(\) WHERE account_id=\$1`).
		WithArgs(int64(26), nil, "server_direct", service.OpenCodeLaneKeyed).
		WillReturnResult(sqlmock.NewResult(0, 1))

	acquired, err := acquireOpenCodeWorkerLease(context.Background(), tx, 26, nil, "server_direct", "2001:db8::1", service.OpenCodeLaneKeyed)
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
		WithArgs(int64(27), int64(123), "proxy", "203.0.113.20", service.OpenCodeLaneKeyed).
		WillReturnResult(sqlmock.NewResult(0, 0))

	proxyID := int64(123)
	acquired, err := acquireOpenCodeWorkerLease(context.Background(), tx, 27, &proxyID, "proxy", "203.0.113.20", service.OpenCodeLaneKeyed)
	require.NoError(t, err)
	require.False(t, acquired)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// 双物化的第二个卡点：租约漂移巡检的抢占判定必须按 (lane, exit_ip)。
// 只按 exit_ip 去重时，同一节点上后到的那个通道会在每轮探测后被删租约并置为
// 不可调度——匿名 worker 会被这条链路系统性清空，而迁移 225 的
// UNIQUE(lane, exit_ip) 恰恰是为了允许它们共存。
func TestReconcileEgressLeasesLetsBothLanesShareOneExitIP(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const sharedExitIP = "203.0.113.7"
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT l.account_id, l.exit_ip, l.lane, n.exit_ip, n.health_status, n.duplicate_of_node_id`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "lease_exit_ip", "lane", "node_exit_ip", "health_status", "duplicate_of_node_id"}).
			AddRow(101, sharedExitIP, service.OpenCodeLaneKeyed, sharedExitIP, "healthy", nil).
			AddRow(102, sharedExitIP, service.OpenCodeLaneAnonymous, sharedExitIP, "healthy", nil))
	mock.ExpectQuery(`SELECT account_id, exit_ip, lane FROM opencode_egress_leases WHERE egress_mode='server_direct'`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "exit_ip", "lane"}))
	mock.ExpectCommit()

	repo := &openCodeProxyPoolRepository{db: db}
	stopped, err := repo.ReconcileEgressLeases(context.Background())
	require.NoError(t, err)
	require.Empty(t, stopped, "两个通道共用一个出口 IP 是合法的，不该有租约被回收")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 反向对照：同一通道内抢占同一个出口 IP 仍然必须被回收。
func TestReconcileEgressLeasesStillEvictsSameLaneDuplicate(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const sharedExitIP = "203.0.113.8"
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT l.account_id, l.exit_ip, l.lane, n.exit_ip, n.health_status, n.duplicate_of_node_id`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "lease_exit_ip", "lane", "node_exit_ip", "health_status", "duplicate_of_node_id"}).
			AddRow(201, sharedExitIP, service.OpenCodeLaneKeyed, sharedExitIP, "healthy", nil).
			AddRow(202, sharedExitIP, service.OpenCodeLaneKeyed, sharedExitIP, "healthy", nil))
	mock.ExpectQuery(`SELECT account_id, exit_ip, lane FROM opencode_egress_leases WHERE egress_mode='server_direct'`).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "exit_ip", "lane"}))
	mock.ExpectExec(`DELETE FROM opencode_egress_leases WHERE account_id=\$1`).
		WithArgs(int64(202)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &openCodeProxyPoolRepository{db: db}
	stopped, err := repo.ReconcileEgressLeases(context.Background())
	require.NoError(t, err)
	require.Equal(t, []int64{202}, stopped)
	require.NoError(t, mock.ExpectationsWereMet())
}

// AcquireEgressLease 的抢占探测同样必须带 lane，否则同伴通道的租约会被
// 误判成抢占者，让合法的租约申请直接失败。
func TestAcquireEgressLeaseScopesOwnerLookupToLane(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	checkedAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT account_id FROM opencode_egress_leases WHERE exit_ip=\$1 AND lane=\$2 FOR UPDATE`).
		WithArgs("203.0.113.9", service.OpenCodeLaneAnonymous).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO opencode_egress_leases \(account_id, proxy_id, egress_mode, exit_ip, lane, checked_at\)`).
		WithArgs(int64(303), int64(77), "proxy", "203.0.113.9", service.OpenCodeLaneAnonymous, checkedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	proxyID := int64(77)
	repo := &openCodeProxyPoolRepository{db: db}
	require.NoError(t, repo.AcquireEgressLease(context.Background(), 303, &proxyID, "proxy",
		"203.0.113.9", service.OpenCodeLaneAnonymous, checkedAt))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 空 lane 必须落回 keyed，而不是写进一个违反 CHECK 约束的空串。
func TestAcquireEgressLeaseDefaultsBlankLaneToKeyed(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	checkedAt := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT account_id FROM opencode_egress_leases WHERE exit_ip=\$1 AND lane=\$2 FOR UPDATE`).
		WithArgs("203.0.113.10", service.OpenCodeLaneKeyed).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO opencode_egress_leases`).
		WithArgs(int64(304), nil, "server_direct", "203.0.113.10", service.OpenCodeLaneKeyed, checkedAt).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := &openCodeProxyPoolRepository{db: db}
	require.NoError(t, repo.AcquireEgressLease(context.Background(), 304, nil, "server_direct",
		"203.0.113.10", "", checkedAt))
	require.NoError(t, mock.ExpectationsWereMet())
}
