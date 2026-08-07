package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type openCodeProxyPoolRepository struct {
	db *sql.DB
}

func NewOpenCodeProxyPoolRepository(db *sql.DB) service.OpenCodeProxyPoolRepository {
	return &openCodeProxyPoolRepository{db: db}
}

const proxySubscriptionColumns = `
	id, name, url_ciphertext, enabled, sync_interval_minutes,
	last_fetched_at, last_success_at, COALESCE(last_error, ''), node_count,
	COALESCE(last_format, ''), COALESCE(last_user_agent, ''), created_at, updated_at`

func scanProxySubscription(scanner interface{ Scan(...any) error }) (*service.ProxySubscription, error) {
	var item service.ProxySubscription
	if err := scanner.Scan(
		&item.ID, &item.Name, &item.URLCiphertext, &item.Enabled, &item.SyncIntervalMinutes,
		&item.LastFetchedAt, &item.LastSuccessAt, &item.LastError, &item.NodeCount,
		&item.LastFormat, &item.LastUserAgent, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.HasURL = item.URLCiphertext != ""
	item.URLMasked = "***"
	return &item, nil
}

func (r *openCodeProxyPoolRepository) ListSubscriptions(ctx context.Context) ([]service.ProxySubscription, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proxySubscriptionColumns+` FROM proxy_subscriptions WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ProxySubscription, 0)
	for rows.Next() {
		item, scanErr := scanProxySubscription(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *openCodeProxyPoolRepository) GetSubscription(ctx context.Context, id int64) (*service.ProxySubscription, error) {
	item, err := scanProxySubscription(r.db.QueryRowContext(ctx, `SELECT `+proxySubscriptionColumns+` FROM proxy_subscriptions WHERE id=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrProxySubscriptionNotFound
	}
	return item, err
}

func (r *openCodeProxyPoolRepository) CreateSubscription(ctx context.Context, input service.ProxySubscription) (*service.ProxySubscription, error) {
	return scanProxySubscription(r.db.QueryRowContext(ctx, `
		INSERT INTO proxy_subscriptions (name, url_ciphertext, enabled, sync_interval_minutes)
		VALUES ($1,$2,$3,$4) RETURNING `+proxySubscriptionColumns,
		input.Name, input.URLCiphertext, input.Enabled, input.SyncIntervalMinutes,
	))
}

func (r *openCodeProxyPoolRepository) UpdateSubscription(ctx context.Context, input service.ProxySubscription) (*service.ProxySubscription, error) {
	if !input.Enabled {
		var bound int
		if err := r.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM accounts a
			JOIN managed_proxy_nodes n ON n.proxy_id=a.proxy_id
			WHERE n.subscription_id=$1 AND a.deleted_at IS NULL`, input.ID).Scan(&bound); err != nil {
			return nil, err
		}
		if bound > 0 {
			return nil, service.ErrProxySubscriptionInUse
		}
	}
	item, err := scanProxySubscription(r.db.QueryRowContext(ctx, `
		UPDATE proxy_subscriptions SET name=$2, url_ciphertext=$3, enabled=$4,
			sync_interval_minutes=$5, updated_at=NOW()
		WHERE id=$1 AND deleted_at IS NULL RETURNING `+proxySubscriptionColumns,
		input.ID, input.Name, input.URLCiphertext, input.Enabled, input.SyncIntervalMinutes,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrProxySubscriptionNotFound
	}
	return item, err
}

func (r *openCodeProxyPoolRepository) DeleteSubscription(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var bound int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM accounts a
		JOIN managed_proxy_nodes n ON n.proxy_id=a.proxy_id
		WHERE n.subscription_id=$1 AND a.deleted_at IS NULL`, id).Scan(&bound); err != nil {
		return err
	}
	if bound > 0 {
		return service.ErrProxySubscriptionInUse
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE proxies SET status='disabled', updated_at=NOW()
		WHERE id IN (SELECT proxy_id FROM managed_proxy_nodes WHERE subscription_id=$1 AND proxy_id IS NOT NULL)`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE managed_proxy_nodes SET deleted_at=NOW(), sync_status='deleted', updated_at=NOW() WHERE subscription_id=$1 AND deleted_at IS NULL`, id); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE proxy_subscriptions SET deleted_at=NOW(), enabled=FALSE, updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrProxySubscriptionNotFound
	}
	return tx.Commit()
}

const managedNodeColumns = `
	n.id, n.subscription_id, n.proxy_id, n.node_key, n.display_name, n.mihomo_name,
	n.protocol, n.config_ciphertext, n.listener_port, n.sync_status, n.health_status,
	n.last_seen_at, COALESCE(n.exit_ip, ''), COALESCE(n.country, ''), COALESCE(n.region, ''),
	n.latency_ms, n.opencode_http_status,
	COALESCE(n.failure_type, ''), COALESCE(n.failure_message, ''), n.last_probe_at,
	n.duplicate_of_node_id, COALESCE(p.username, ''), COALESCE(p.password, '')`

func scanManagedNode(scanner interface{ Scan(...any) error }) (*service.ManagedProxyNode, error) {
	var item service.ManagedProxyNode
	err := scanner.Scan(
		&item.ID, &item.SubscriptionID, &item.ProxyID, &item.NodeKey, &item.DisplayName, &item.MihomoName,
		&item.Protocol, &item.ConfigCiphertext, &item.ListenerPort, &item.SyncStatus, &item.HealthStatus,
		&item.LastSeenAt, &item.ExitIP, &item.Country, &item.Region, &item.LatencyMs, &item.OpenCodeHTTPStatus,
		&item.FailureType, &item.FailureMessage, &item.LastProbeAt, &item.DuplicateOfNodeID,
		&item.ListenerUsername, &item.ListenerPassword,
	)
	return &item, err
}

func (r *openCodeProxyPoolRepository) ListNodes(ctx context.Context, subscriptionID *int64) ([]service.ManagedProxyNode, error) {
	query := `SELECT ` + managedNodeColumns + ` FROM managed_proxy_nodes n LEFT JOIN proxies p ON p.id=n.proxy_id WHERE n.deleted_at IS NULL`
	args := []any{}
	if subscriptionID != nil {
		query += ` AND n.subscription_id=$1`
		args = append(args, *subscriptionID)
	}
	query += ` ORDER BY n.subscription_id, n.id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ManagedProxyNode, 0)
	for rows.Next() {
		item, scanErr := scanManagedNode(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *openCodeProxyPoolRepository) UsedListenerPorts(ctx context.Context) (map[int]struct{}, error) {
	// Listener ports are globally unique, including for soft-deleted nodes. Keep
	// historical reservations out of circulation so a later subscription sync
	// cannot collide with the database's non-partial unique constraint.
	rows, err := r.db.QueryContext(ctx, `SELECT listener_port FROM managed_proxy_nodes`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ports := make(map[int]struct{})
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, err
		}
		ports[port] = struct{}{}
	}
	return ports, rows.Err()
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

func (r *openCodeProxyPoolRepository) CommitSubscriptionSync(ctx context.Context, subscriptionID int64, drafts []service.ManagedProxyNodeDraft, format, userAgent string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE proxies SET status='disabled', updated_at=NOW()
		WHERE id IN (SELECT proxy_id FROM managed_proxy_nodes WHERE subscription_id=$1 AND deleted_at IS NULL)`, subscriptionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE managed_proxy_nodes SET sync_status='missing', health_status='missing', updated_at=NOW()
		WHERE subscription_id=$1 AND deleted_at IS NULL`, subscriptionID); err != nil {
		return err
	}

	for _, draft := range drafts {
		var nodeID int64
		var proxyID sql.NullInt64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO managed_proxy_nodes (
				subscription_id, proxy_id, node_key, display_name, mihomo_name, protocol,
				config_ciphertext, listener_port, sync_status, health_status, last_seen_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active','unprobed',NOW())
			ON CONFLICT (subscription_id, node_key) DO UPDATE SET
				display_name=EXCLUDED.display_name, mihomo_name=EXCLUDED.mihomo_name,
				protocol=EXCLUDED.protocol, config_ciphertext=EXCLUDED.config_ciphertext,
				listener_port=EXCLUDED.listener_port, sync_status='active', last_seen_at=NOW(),
				deleted_at=NULL, updated_at=NOW()
			RETURNING id, proxy_id`,
			subscriptionID, draft.ProxyID, draft.NodeKey, draft.DisplayName, draft.MihomoName,
			draft.Protocol, draft.ConfigCiphertext, draft.ListenerPort,
		).Scan(&nodeID, &proxyID)
		if err != nil {
			return err
		}
		proxyName := truncateRunes("OpenCode / "+draft.DisplayName, 100)
		if !proxyID.Valid {
			err = tx.QueryRowContext(ctx, `
				INSERT INTO proxies (name, protocol, host, port, username, password, status, fallback_mode, expiry_warn_days)
				VALUES ($1,'http','opencode-mihomo',$2,$3,$4,'active','none',7) RETURNING id`,
				proxyName, draft.ListenerPort, draft.ListenerUsername, draft.ListenerPassword,
			).Scan(&proxyID.Int64)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE managed_proxy_nodes SET proxy_id=$2, updated_at=NOW() WHERE id=$1`, nodeID, proxyID.Int64); err != nil {
				return err
			}
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE proxies SET name=$2, protocol='http', host='opencode-mihomo', port=$3,
					username=$4, password=$5, status='active', fallback_mode='none', backup_proxy_id=NULL,
					updated_at=NOW(), deleted_at=NULL WHERE id=$1`,
				proxyID.Int64, proxyName, draft.ListenerPort, draft.ListenerUsername, draft.ListenerPassword,
			)
			if err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE proxy_subscriptions SET last_fetched_at=NOW(), last_success_at=NOW(), last_error=NULL,
			node_count=$2, last_format=$3, last_user_agent=$4, updated_at=NOW()
		WHERE id=$1 AND deleted_at IS NULL`, subscriptionID, len(drafts), format, userAgent)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrProxySubscriptionNotFound
	}
	return tx.Commit()
}

func (r *openCodeProxyPoolRepository) UpdateSubscriptionError(ctx context.Context, id int64, message string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_subscriptions SET last_fetched_at=NOW(), last_error=$2, updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id, truncateRunes(message, 2000))
	return err
}

func (r *openCodeProxyPoolRepository) UpdateNodeProbeResults(ctx context.Context, results []service.OpenCodeNodeProbeResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, result := range results {
		var latency any
		if result.LatencyMs > 0 {
			latency = result.LatencyMs
		}
		var status any
		if result.OpenCodeHTTPStatus > 0 {
			status = result.OpenCodeHTTPStatus
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE managed_proxy_nodes SET health_status=$2, exit_ip=NULLIF($3,''),
				country=NULLIF($4,''), region=NULLIF($5,''), latency_ms=$6,
				opencode_http_status=$7, failure_type=NULLIF($8,''), failure_message=NULLIF($9,''),
				last_probe_at=NOW(), duplicate_of_node_id=$10, updated_at=NOW()
			WHERE id=$1 AND deleted_at IS NULL`,
			result.NodeID, result.HealthStatus, result.ExitIP, result.Country, result.Region, latency, status,
			result.FailureType, truncateRunes(result.FailureMessage, 2000), result.DuplicateOfNodeID,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *openCodeProxyPoolRepository) IsManagedProxy(ctx context.Context, proxyID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM managed_proxy_nodes WHERE proxy_id=$1 AND deleted_at IS NULL)`, proxyID).Scan(&exists)
	return exists, err
}

func (r *openCodeProxyPoolRepository) ReconcileEgressLeases(ctx context.Context) ([]int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	type leaseState struct {
		accountID int64
		leaseIP   string
		exitIP    sql.NullString
		health    sql.NullString
		duplicate sql.NullInt64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT l.account_id, l.exit_ip, n.exit_ip, n.health_status, n.duplicate_of_node_id
		FROM opencode_egress_leases l
		JOIN accounts a ON a.id=l.account_id AND a.deleted_at IS NULL
		LEFT JOIN managed_proxy_nodes n ON n.proxy_id=a.proxy_id AND n.deleted_at IS NULL
		WHERE l.egress_mode='proxy'
		ORDER BY l.created_at, l.id
		FOR UPDATE OF l`)
	if err != nil {
		return nil, err
	}
	states := make([]leaseState, 0)
	for rows.Next() {
		var state leaseState
		if err := rows.Scan(&state.accountID, &state.leaseIP, &state.exitIP, &state.health, &state.duplicate); err != nil {
			_ = rows.Close()
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	occupied := make(map[string]int64)
	directRows, err := tx.QueryContext(ctx, `SELECT account_id, exit_ip FROM opencode_egress_leases WHERE egress_mode='server_direct'`)
	if err != nil {
		return nil, err
	}
	for directRows.Next() {
		var accountID int64
		var exitIP string
		if err := directRows.Scan(&accountID, &exitIP); err != nil {
			_ = directRows.Close()
			return nil, err
		}
		occupied[exitIP] = accountID
	}
	if err := directRows.Close(); err != nil {
		return nil, err
	}
	stopped := make([]int64, 0)
	for _, state := range states {
		desired := strings.ToLower(strings.TrimSpace(state.exitIP.String))
		valid := state.exitIP.Valid && desired != "" && state.health.String == "healthy" && !state.duplicate.Valid
		if owner, claimed := occupied[desired]; valid && claimed && owner != state.accountID {
			valid = false
		}
		if !valid {
			if _, err := tx.ExecContext(ctx, `DELETE FROM opencode_egress_leases WHERE account_id=$1`, state.accountID); err != nil {
				return nil, err
			}
			stopped = append(stopped, state.accountID)
			continue
		}
		occupied[desired] = state.accountID
		if desired != state.leaseIP {
			if _, err := tx.ExecContext(ctx, `UPDATE opencode_egress_leases SET exit_ip=$2, checked_at=NOW(), updated_at=NOW() WHERE account_id=$1`, state.accountID, desired); err != nil {
				if isOpenCodeUniqueViolation(err) {
					return nil, fmt.Errorf("%w: %s", service.ErrOpenCodeDuplicateExitIP, desired)
				}
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stopped, nil
}

func isOpenCodeUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr != nil && pqErr.Code == "23505"
}

func (r *openCodeProxyPoolRepository) AcquireEgressLease(ctx context.Context, accountID int64, proxyID *int64, mode, exitIP string, checkedAt time.Time) error {
	exitIP = strings.ToLower(strings.TrimSpace(exitIP))
	if exitIP == "" {
		return errors.New("exit IP is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM opencode_egress_leases WHERE exit_ip=$1 FOR UPDATE`, exitIP).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && owner != accountID {
		return fmt.Errorf("%w: %s", service.ErrOpenCodeDuplicateExitIP, exitIP)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO opencode_egress_leases (account_id, proxy_id, egress_mode, exit_ip, checked_at)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (account_id) DO UPDATE SET proxy_id=EXCLUDED.proxy_id,
			egress_mode=EXCLUDED.egress_mode, exit_ip=EXCLUDED.exit_ip,
			checked_at=EXCLUDED.checked_at, updated_at=NOW()`,
		accountID, proxyID, mode, exitIP, checkedAt,
	)
	if isOpenCodeUniqueViolation(err) {
		return fmt.Errorf("%w: %s", service.ErrOpenCodeDuplicateExitIP, exitIP)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *openCodeProxyPoolRepository) ReleaseEgressLease(ctx context.Context, accountID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM opencode_egress_leases WHERE account_id=$1`, accountID)
	return err
}
