package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
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

	mock.ExpectQuery("SELECT listener_port FROM managed_proxy_nodes").
		WillReturnRows(sqlmock.NewRows([]string{"listener_port"}).AddRow(22000).AddRow(22021))

	repo := &openCodeProxyPoolRepository{db: db}
	ports, err := repo.UsedListenerPorts(context.Background())
	require.NoError(t, err)
	require.Contains(t, ports, 22000)
	require.Contains(t, ports, 22021)
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
