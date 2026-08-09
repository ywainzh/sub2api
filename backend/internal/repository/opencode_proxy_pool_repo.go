package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
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
	id, name, COALESCE(url_ciphertext, ''), enabled, sync_interval_minutes,
	last_fetched_at, last_success_at, COALESCE(last_error, ''), node_count,
	COALESCE(last_format, ''), COALESCE(last_user_agent, ''), created_at, updated_at,
	COALESCE(source_type, 'url'), expires_at`

func scanProxySubscription(scanner interface{ Scan(...any) error }) (*service.ProxySubscription, error) {
	var item service.ProxySubscription
	if err := scanner.Scan(
		&item.ID, &item.Name, &item.URLCiphertext, &item.Enabled, &item.SyncIntervalMinutes,
		&item.LastFetchedAt, &item.LastSuccessAt, &item.LastError, &item.NodeCount,
		&item.LastFormat, &item.LastUserAgent, &item.CreatedAt, &item.UpdatedAt, &item.SourceType, &item.ExpiresAt,
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
		INSERT INTO proxy_subscriptions (name, url_ciphertext, enabled, sync_interval_minutes, source_type, expires_at)
		VALUES ($1,NULLIF($2,''),$3,$4,COALESCE(NULLIF($5,''),'url'),$6) RETURNING `+proxySubscriptionColumns,
		input.Name, input.URLCiphertext, input.Enabled, input.SyncIntervalMinutes, input.SourceType, input.ExpiresAt,
	))
}

func (r *openCodeProxyPoolRepository) UpdateSubscription(ctx context.Context, input service.ProxySubscription) (*service.ProxySubscription, error) {
	if !input.Enabled {
		var bound int
		if err := r.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM accounts a
			JOIN managed_proxy_nodes n ON n.proxy_id=a.proxy_id
			LEFT JOIN opencode_pool_workers w ON w.account_id=a.id
			WHERE n.subscription_id=$1 AND a.deleted_at IS NULL AND w.id IS NULL`, input.ID).Scan(&bound); err != nil {
			return nil, err
		}
		if bound > 0 {
			return nil, service.ErrProxySubscriptionInUse
		}
	}
	item, err := scanProxySubscription(r.db.QueryRowContext(ctx, `
		UPDATE proxy_subscriptions SET name=$2, url_ciphertext=NULLIF($3,''), enabled=$4,
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
		LEFT JOIN opencode_pool_workers w ON w.account_id=a.id
		WHERE n.subscription_id=$1 AND a.deleted_at IS NULL AND w.id IS NULL`, id).Scan(&bound); err != nil {
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
	n.protocol, n.config_ciphertext, COALESCE(n.listener_port, 0), COALESCE(n.transport_mode, 'mihomo_listener'), n.sync_status, n.health_status,
	n.last_seen_at, COALESCE(n.exit_ip, ''), COALESCE(n.country, ''), COALESCE(n.region, ''),
	n.latency_ms, n.opencode_http_status,
	COALESCE(n.failure_type, ''), COALESCE(n.failure_message, ''), n.last_probe_at,
	n.duplicate_of_node_id, n.unavailable_since, n.last_successful_probe_at,
	COALESCE(n.consecutive_failures, 0), n.retry_at, COALESCE(n.retry_count, 0),
	COALESCE(p.username, ''), COALESCE(p.password, ''), COALESCE(p.protocol, ''),
	COALESCE(p.host, ''), COALESCE(p.port, 0)`

func scanManagedNode(scanner interface{ Scan(...any) error }) (*service.ManagedProxyNode, error) {
	var item service.ManagedProxyNode
	err := scanner.Scan(
		&item.ID, &item.SubscriptionID, &item.ProxyID, &item.NodeKey, &item.DisplayName, &item.MihomoName,
		&item.Protocol, &item.ConfigCiphertext, &item.ListenerPort, &item.TransportMode, &item.SyncStatus, &item.HealthStatus,
		&item.LastSeenAt, &item.ExitIP, &item.Country, &item.Region, &item.LatencyMs, &item.OpenCodeHTTPStatus,
		&item.FailureType, &item.FailureMessage, &item.LastProbeAt, &item.DuplicateOfNodeID,
		&item.UnavailableSince, &item.LastSuccessfulProbeAt, &item.ConsecutiveFailures, &item.RetryAt, &item.RetryCount,
		&item.ListenerUsername, &item.ListenerPassword, &item.ProxyProtocol, &item.ProxyHost, &item.ProxyPort,
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
	rows, err := r.db.QueryContext(ctx, `SELECT listener_port FROM managed_proxy_nodes WHERE listener_port IS NOT NULL`)
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

func proxyConfigString(config map[string]any, key string) string {
	return strings.TrimSpace(fmt.Sprint(config[key]))
}

func proxyConfigPort(config map[string]any) (int, error) {
	port, err := strconv.Atoi(proxyConfigString(config, "port"))
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid proxy port")
	}
	return port, nil
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
		var listenerPort any
		if draft.TransportMode == "mihomo_listener" {
			listenerPort = draft.ListenerPort
		}
		err := tx.QueryRowContext(ctx, `
			INSERT INTO managed_proxy_nodes (
				subscription_id, proxy_id, node_key, display_name, mihomo_name, protocol,
				config_ciphertext, listener_port, transport_mode, sync_status, health_status, last_seen_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'active','unprobed',NOW())
			ON CONFLICT (subscription_id, node_key) DO UPDATE SET
				display_name=EXCLUDED.display_name, mihomo_name=EXCLUDED.mihomo_name,
				protocol=EXCLUDED.protocol, config_ciphertext=EXCLUDED.config_ciphertext,
				listener_port=EXCLUDED.listener_port, transport_mode=EXCLUDED.transport_mode,
				sync_status='active', last_seen_at=NOW(),
				deleted_at=NULL, updated_at=NOW()
			RETURNING id, proxy_id`,
			subscriptionID, draft.ProxyID, draft.NodeKey, draft.DisplayName, draft.MihomoName,
			draft.Protocol, draft.ConfigCiphertext, listenerPort, draft.TransportMode,
		).Scan(&nodeID, &proxyID)
		if err != nil {
			return err
		}
		proxyName := truncateRunes("OpenCode / "+draft.DisplayName, 100)
		proxyProtocol, proxyHost, proxyPort := "http", "opencode-mihomo", draft.ListenerPort
		proxyUsername, proxyPassword := draft.ListenerUsername, draft.ListenerPassword
		if draft.TransportMode == "direct_http" {
			proxyProtocol = strings.ToLower(proxyConfigString(draft.ProxyConfig, "type"))
			proxyHost = proxyConfigString(draft.ProxyConfig, "server")
			proxyPort, err = proxyConfigPort(draft.ProxyConfig)
			if err != nil || proxyHost == "" || (proxyProtocol != "http" && proxyProtocol != "https") {
				return fmt.Errorf("invalid direct HTTP proxy %q", draft.DisplayName)
			}
			proxyUsername = proxyConfigString(draft.ProxyConfig, "username")
			proxyPassword = proxyConfigString(draft.ProxyConfig, "password")
		}
		if !proxyID.Valid {
			err = tx.QueryRowContext(ctx, `
				INSERT INTO proxies (name, protocol, host, port, username, password, status, fallback_mode, expiry_warn_days)
				VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),'active','none',7) RETURNING id`,
				proxyName, proxyProtocol, proxyHost, proxyPort, proxyUsername, proxyPassword,
			).Scan(&proxyID.Int64)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE managed_proxy_nodes SET proxy_id=$2, updated_at=NOW() WHERE id=$1`, nodeID, proxyID.Int64); err != nil {
				return err
			}
		} else {
			_, err = tx.ExecContext(ctx, `
				UPDATE proxies SET name=$2, protocol=$3, host=$4, port=$5,
					username=NULLIF($6,''), password=NULLIF($7,''), status='active', fallback_mode='none', backup_proxy_id=NULL,
					updated_at=NOW(), deleted_at=NULL WHERE id=$1`,
				proxyID.Int64, proxyName, proxyProtocol, proxyHost, proxyPort, proxyUsername, proxyPassword,
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
		isHardFailure := isOpenCodeHardProbeFailure(result.FailureType)
		var retryAt any
		if result.RetryAt != nil {
			retryAt = *result.RetryAt
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE managed_proxy_nodes SET
				health_status=CASE
					WHEN $11 THEN 'healthy'
					WHEN $12 AND COALESCE(unavailable_since, NOW()) <= NOW() - INTERVAL '7 days' THEN 'quarantined'
					ELSE $2 END,
				exit_ip=NULLIF($3,''), country=NULLIF($4,''), region=NULLIF($5,''), latency_ms=$6,
				opencode_http_status=$7, failure_type=NULLIF($8,''), failure_message=NULLIF($9,''),
				last_probe_at=NOW(), duplicate_of_node_id=$10,
				last_successful_probe_at=CASE WHEN $11 THEN NOW() ELSE last_successful_probe_at END,
				unavailable_since=CASE WHEN $11 THEN NULL WHEN $12 THEN COALESCE(unavailable_since, NOW()) ELSE unavailable_since END,
				consecutive_failures=CASE WHEN $11 THEN 0 WHEN $12 THEN consecutive_failures + 1 ELSE consecutive_failures END,
				retry_count=CASE WHEN $11 THEN 0 ELSE retry_count + 1 END,
				retry_at=CASE WHEN $11 THEN NULL ELSE COALESCE($13, NOW() + CASE
					WHEN retry_count <= 0 THEN INTERVAL '5 minutes'
					WHEN retry_count = 1 THEN INTERVAL '15 minutes'
					WHEN retry_count = 2 THEN INTERVAL '1 hour'
					ELSE INTERVAL '6 hours' END) END,
				updated_at=NOW()
			WHERE id=$1 AND deleted_at IS NULL`,
			result.NodeID, result.HealthStatus, result.ExitIP, result.Country, result.Region, latency, status,
			result.FailureType, truncateRunes(result.FailureMessage, 2000), result.DuplicateOfNodeID,
			result.Success, isHardFailure, retryAt,
		)
		if err != nil {
			return err
		}
		proxyStatus := "disabled"
		if result.Success {
			proxyStatus = "active"
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE proxies SET status=$2, updated_at=NOW()
			WHERE id=(SELECT proxy_id FROM managed_proxy_nodes WHERE id=$1)`, result.NodeID, proxyStatus); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func isOpenCodeHardProbeFailure(failureType string) bool {
	switch failureType {
	case "tls", "dns", "timeout", "transport", "exit_probe", "auth":
		return true
	default:
		return false
	}
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

const openCodePoolColumns = `
	p.id, p.name, p.group_id, p.enabled,
	COALESCE(p.upstream_api_key_ciphertext, ''), p.include_server_direct,
	p.worker_concurrency, p.reconcile_status, COALESCE(p.reconcile_error, ''),
	p.last_reconciled_at, p.created_at, p.updated_at`

func scanOpenCodePool(scanner interface{ Scan(...any) error }) (*service.OpenCodePool, error) {
	var pool service.OpenCodePool
	if err := scanner.Scan(
		&pool.ID, &pool.Name, &pool.GroupID, &pool.Enabled,
		&pool.UpstreamKeyCiphertext, &pool.IncludeServerDirect,
		&pool.WorkerConcurrency, &pool.ReconcileStatus, &pool.ReconcileError,
		&pool.LastReconciledAt, &pool.CreatedAt, &pool.UpdatedAt,
	); err != nil {
		return nil, err
	}
	pool.UpstreamKeyConfigured = strings.TrimSpace(pool.UpstreamKeyCiphertext) != ""
	return &pool, nil
}

func (r *openCodeProxyPoolRepository) fillOpenCodePoolStats(ctx context.Context, pool *service.OpenCodePool) error {
	if pool == nil {
		return nil
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM opencode_pool_workers w WHERE w.pool_id=$1 AND w.status='active'),
			(SELECT COUNT(*) FROM managed_proxy_nodes n JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE WHERE n.deleted_at IS NULL AND n.sync_status='active' AND n.health_status='healthy' AND n.duplicate_of_node_id IS NULL),
			(SELECT COUNT(*) FROM managed_proxy_nodes n JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE WHERE n.deleted_at IS NULL AND n.sync_status='active' AND n.health_status='rate_limited'),
			(SELECT COUNT(*) FROM managed_proxy_nodes n JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE WHERE n.deleted_at IS NULL AND n.sync_status='active' AND (n.health_status='duplicate_exit' OR n.duplicate_of_node_id IS NOT NULL)),
			(SELECT COUNT(*) FROM managed_proxy_nodes n JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE WHERE n.deleted_at IS NULL AND n.sync_status='active' AND n.health_status NOT IN ('healthy','rate_limited','duplicate_exit')),
			COALESCE((SELECT w.status FROM opencode_pool_workers w WHERE w.pool_id=$1 AND w.egress_mode='server_direct' ORDER BY w.id LIMIT 1), '')`, pool.ID).Scan(
		&pool.ActiveWorkers, &pool.HealthyNodes, &pool.RateLimitedNodes,
		&pool.DuplicateNodes, &pool.FailedNodes, &pool.ServerDirectStatus,
	); err != nil {
		return err
	}
	return nil
}

func (r *openCodeProxyPoolRepository) EnsureDefaultPool(ctx context.Context) (*service.OpenCodePool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('opencode_default_pool_bootstrap'))`); err != nil {
		return nil, err
	}
	var poolID, groupID int64
	poolCreated := false
	err = tx.QueryRowContext(ctx, `SELECT id, group_id FROM opencode_pools WHERE singleton_key=TRUE`).Scan(&poolID, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM groups
			WHERE name='OpenCode Zen Pool' AND deleted_at IS NULL
			ORDER BY id LIMIT 1`).Scan(&groupID)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `
				INSERT INTO groups (
					name, description, platform, rate_multiplier, status,
					allow_messages_dispatch, models_list_config
				) VALUES (
					'OpenCode Zen Pool',
					'Read-only system group for automatically managed OpenCode Zen workers',
					'openai', 0, 'active', TRUE,
					'{"enabled":false,"enforce":false,"models":[]}'::jsonb
				) RETURNING id`).Scan(&groupID)
		}
		if err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO opencode_pools (singleton_key, name, group_id)
			VALUES (TRUE, 'OpenCode Zen Pool', $1)
			RETURNING id`, groupID).Scan(&poolID); err != nil {
			return nil, err
		}
		poolCreated = true
	} else if err != nil {
		return nil, err
	}
	normalizeResult, err := tx.ExecContext(ctx, `
		UPDATE groups SET
			name='OpenCode Zen Pool',
			description='Read-only system group for automatically managed OpenCode Zen workers',
			platform='openai', rate_multiplier=0, status='active',
			allow_messages_dispatch=TRUE,
			models_list_config='{"enabled":false,"enforce":false,"models":[]}'::jsonb,
			updated_at=NOW()
		WHERE id=$1 AND (
			name IS DISTINCT FROM 'OpenCode Zen Pool' OR
			description IS DISTINCT FROM 'Read-only system group for automatically managed OpenCode Zen workers' OR
			platform IS DISTINCT FROM 'openai' OR rate_multiplier IS DISTINCT FROM 0 OR
			status IS DISTINCT FROM 'active' OR allow_messages_dispatch IS DISTINCT FROM TRUE OR
			models_list_config IS DISTINCT FROM '{"enabled":false,"enforce":false,"models":[]}'::jsonb
		)`, groupID)
	if err != nil {
		return nil, err
	}
	if affected, _ := normalizeResult.RowsAffected(); poolCreated || affected > 0 {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetOpenCodePool(ctx)
}

func (r *openCodeProxyPoolRepository) GetOpenCodePool(ctx context.Context) (*service.OpenCodePool, error) {
	pool, err := scanOpenCodePool(r.db.QueryRowContext(ctx, `SELECT `+openCodePoolColumns+` FROM opencode_pools p WHERE p.singleton_key=TRUE`))
	if err != nil {
		return nil, err
	}
	if err := r.fillOpenCodePoolStats(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (r *openCodeProxyPoolRepository) UpdateOpenCodePool(ctx context.Context, input service.OpenCodePool) (*service.OpenCodePool, error) {
	pool, err := scanOpenCodePool(r.db.QueryRowContext(ctx, `
		UPDATE opencode_pools AS p SET
			enabled=$2, upstream_api_key_ciphertext=NULLIF($3,''),
			include_server_direct=$4, worker_concurrency=$5,
			reconcile_status=$6, reconcile_error=NULLIF($7,''),
			last_reconciled_at=$8, updated_at=NOW()
		WHERE id=$1
		RETURNING `+openCodePoolColumns,
		input.ID, input.Enabled, input.UpstreamKeyCiphertext,
		input.IncludeServerDirect, input.WorkerConcurrency,
		input.ReconcileStatus, input.ReconcileError, input.LastReconciledAt,
	))
	if err != nil {
		return nil, err
	}
	if err := r.fillOpenCodePoolStats(ctx, pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func scanOpenCodePoolWorker(scanner interface{ Scan(...any) error }) (*service.OpenCodePoolWorker, error) {
	var worker service.OpenCodePoolWorker
	if err := scanner.Scan(
		&worker.ID, &worker.PoolID, &worker.ManagedNodeID, &worker.AccountID,
		&worker.ProxyID, &worker.DisplayName, &worker.EgressMode, &worker.ExitIP,
		&worker.Status, &worker.ErrorReason, &worker.HealthStatus,
		&worker.OpenCodeHTTPStatus, &worker.LastProbeAt, &worker.RateLimitResetAt,
		&worker.CreatedAt, &worker.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &worker, nil
}

const openCodePoolWorkerColumns = `
	w.id, w.pool_id, w.managed_node_id, w.account_id,
	a.proxy_id, COALESCE(n.display_name, a.name), w.egress_mode,
	COALESCE(w.exit_ip, ''), w.status, COALESCE(w.error_reason, ''),
	COALESCE(n.health_status, ''), n.opencode_http_status, n.last_probe_at,
	a.rate_limit_reset_at, w.created_at, w.updated_at`

func (r *openCodeProxyPoolRepository) ListOpenCodePoolWorkers(ctx context.Context, poolID int64) ([]service.OpenCodePoolWorker, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+openCodePoolWorkerColumns+`
		FROM opencode_pool_workers w
		JOIN accounts a ON a.id=w.account_id
		LEFT JOIN managed_proxy_nodes n ON n.id=w.managed_node_id
		WHERE w.pool_id=$1
		ORDER BY w.egress_mode, w.id`, poolID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	workers := make([]service.OpenCodePoolWorker, 0)
	for rows.Next() {
		worker, scanErr := scanOpenCodePoolWorker(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		workers = append(workers, *worker)
	}
	return workers, rows.Err()
}

type openCodeEligibleNode struct {
	id        int64
	proxyID   int64
	name      string
	exitIP    string
	latencyMs int64
}

const (
	openCodeWorkerDefaultPriority = 50
	openCodeWorkerLatencyBucketMs = int64(1000)
)

func openCodeWorkerPriority(latencyMs int64) int {
	if latencyMs <= 0 {
		return openCodeWorkerDefaultPriority
	}
	// One-second buckets prefer faster egresses while keeping enough workers at
	// the same priority for the scheduler's load-aware and LRU balancing.
	priority := latencyMs / openCodeWorkerLatencyBucketMs
	if latencyMs%openCodeWorkerLatencyBucketMs != 0 {
		priority++
	}
	if priority < 1 {
		return 1
	}
	if priority > openCodeWorkerDefaultPriority {
		return openCodeWorkerDefaultPriority
	}
	return int(priority)
}

func openCodeWorkerCredentials(apiKey string) string {
	payload, _ := json.Marshal(map[string]any{"api_key": apiKey})
	return string(payload)
}

func openCodeWorkerExtra(poolID int64, mode string, nodeID *int64) string {
	payload := map[string]any{
		service.OpenAIProviderModeExtraKey:  service.OpenAIProviderModeOpenCodeZen,
		service.OpenCodeEgressModeExtraKey:  mode,
		openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
		"system_worker":                     "opencode_pool",
		"opencode_pool_id":                  poolID,
	}
	if nodeID != nil {
		payload["managed_node_id"] = *nodeID
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func acquireOpenCodeWorkerLease(
	ctx context.Context,
	tx *sql.Tx,
	accountID int64,
	proxyID *int64,
	mode, exitIP string,
) (bool, error) {
	normalizedExitIP := strings.ToLower(strings.TrimSpace(exitIP))
	leaseAcquired := false
	var existingExitIP string
	leaseErr := tx.QueryRowContext(ctx, `
		SELECT exit_ip FROM opencode_egress_leases
		WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&existingExitIP)
	switch {
	case leaseErr == nil && strings.EqualFold(strings.TrimSpace(existingExitIP), normalizedExitIP):
		// Keep the original row (and therefore its created_at ordering) when the
		// worker still owns the same real egress. Reconciliation must not create a
		// release/reacquire window in which a later account can steal the lease.
		if _, err := tx.ExecContext(ctx, `
			UPDATE opencode_egress_leases SET proxy_id=$2, egress_mode=$3,
				checked_at=NOW(), updated_at=NOW() WHERE account_id=$1`,
			accountID, proxyID, mode); err != nil {
			return false, err
		}
		leaseAcquired = true
	case leaseErr == nil:
		// The worker moved to a different egress. Its historical lease no longer
		// represents reality, so release it before competing for the new address.
		if _, err := tx.ExecContext(ctx, `DELETE FROM opencode_egress_leases WHERE account_id=$1`, accountID); err != nil {
			return false, err
		}
	case !errors.Is(leaseErr, sql.ErrNoRows):
		return false, leaseErr
	}
	if leaseAcquired {
		return true, nil
	}
	leaseResult, err := tx.ExecContext(ctx, `
		INSERT INTO opencode_egress_leases (account_id, proxy_id, egress_mode, exit_ip, checked_at)
		VALUES ($1,$2,$3,$4,NOW())
		ON CONFLICT DO NOTHING`, accountID, proxyID, mode, normalizedExitIP)
	if err != nil {
		return false, err
	}
	leaseInserted, err := leaseResult.RowsAffected()
	if err != nil {
		return false, err
	}
	return leaseInserted > 0, nil
}

func (r *openCodeProxyPoolRepository) upsertOpenCodeWorkerAccount(
	ctx context.Context,
	tx *sql.Tx,
	pool service.OpenCodePool,
	workerID *int64,
	accountID *int64,
	nodeID *int64,
	proxyID *int64,
	mode, name, exitIP, upstreamKey string,
	priority int,
) (int64, int64, error) {
	credentials := openCodeWorkerCredentials(upstreamKey)
	extra := openCodeWorkerExtra(pool.ID, mode, nodeID)
	resolvedAccountID := int64(0)
	if accountID != nil && *accountID > 0 {
		result, err := tx.ExecContext(ctx, `
			UPDATE accounts SET name=$2, platform='openai', type='apikey',
				credentials=$3::jsonb, extra=$4::jsonb, proxy_id=$5,
				concurrency=$6, priority=$7, rate_multiplier=0, status='active', schedulable=TRUE,
				error_message=NULL, auto_pause_on_expired=FALSE, updated_at=NOW()
			WHERE id=$1 AND deleted_at IS NULL`,
			*accountID, truncateRunes(name, 100), credentials, extra, proxyID, pool.WorkerConcurrency, priority)
		if err != nil {
			return 0, 0, err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			resolvedAccountID = *accountID
		}
	}
	if resolvedAccountID == 0 {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO accounts (
				name, platform, type, credentials, extra, proxy_id,
				concurrency, priority, rate_multiplier, status, schedulable,
				auto_pause_on_expired
			) VALUES ($1,'openai','apikey',$2::jsonb,$3::jsonb,$4,$5,$6,0,'active',TRUE,FALSE)
			RETURNING id`, truncateRunes(name, 100), credentials, extra, proxyID, pool.WorkerConcurrency, priority).Scan(&resolvedAccountID); err != nil {
			return 0, 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, resolvedAccountID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_groups (account_id, group_id, priority)
		VALUES ($1,$2,$3) ON CONFLICT (account_id, group_id) DO UPDATE SET priority=EXCLUDED.priority`,
		resolvedAccountID, pool.GroupID, priority); err != nil {
		return 0, 0, err
	}
	resolvedWorkerID := int64(0)
	if workerID != nil && *workerID > 0 {
		if err := tx.QueryRowContext(ctx, `
			UPDATE opencode_pool_workers SET account_id=$2, managed_node_id=$3,
				egress_mode=$4, exit_ip=$5, status='active', error_reason=NULL, updated_at=NOW()
			WHERE id=$1 RETURNING id`, *workerID, resolvedAccountID, nodeID, mode, exitIP).Scan(&resolvedWorkerID); err != nil {
			return 0, 0, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO opencode_pool_workers (
				pool_id, managed_node_id, account_id, egress_mode, exit_ip, status
			) VALUES ($1,$2,$3,$4,$5,'active')
			RETURNING id`, pool.ID, nodeID, resolvedAccountID, mode, exitIP).Scan(&resolvedWorkerID); err != nil {
			return 0, 0, err
		}
	}
	leaseAcquired, err := acquireOpenCodeWorkerLease(ctx, tx, resolvedAccountID, proxyID, mode, exitIP)
	if err != nil {
		return 0, 0, err
	}
	if !leaseAcquired {
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE accounts SET status='disabled', schedulable=FALSE,
				error_message='duplicate_exit_ip', updated_at=NOW() WHERE id=$1`, resolvedAccountID); updateErr != nil {
			return 0, 0, updateErr
		}
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE opencode_pool_workers SET status='inactive', error_reason='duplicate_exit_ip', updated_at=NOW()
			WHERE id=$1`, resolvedWorkerID); updateErr != nil {
			return 0, 0, updateErr
		}
		return resolvedWorkerID, resolvedAccountID, nil
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &resolvedAccountID, nil, buildSchedulerGroupPayload([]int64{pool.GroupID})); err != nil {
		return 0, 0, err
	}
	return resolvedWorkerID, resolvedAccountID, nil
}

func (r *openCodeProxyPoolRepository) ReconcileOpenCodePoolWorkers(
	ctx context.Context,
	pool service.OpenCodePool,
	upstreamKey string,
	directProbe *service.OpenCodeNodeProbeResult,
) ([]service.OpenCodePoolWorker, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('opencode_worker_pool_reconcile'))`); err != nil {
		return nil, err
	}

	type existingWorker struct {
		id, accountID    int64
		nodeID           sql.NullInt64
		mode             string
		rateLimitResetAt sql.NullTime
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT w.id, w.account_id, w.managed_node_id, w.egress_mode, a.rate_limit_reset_at
		FROM opencode_pool_workers w
		JOIN accounts a ON a.id=w.account_id
		WHERE w.pool_id=$1 FOR UPDATE OF w, a`, pool.ID)
	if err != nil {
		return nil, err
	}
	existingByNode := make(map[int64]existingWorker)
	var directWorker *existingWorker
	allExisting := make([]existingWorker, 0)
	for rows.Next() {
		var worker existingWorker
		if err := rows.Scan(&worker.id, &worker.accountID, &worker.nodeID, &worker.mode, &worker.rateLimitResetAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		allExisting = append(allExisting, worker)
		if worker.nodeID.Valid {
			existingByNode[worker.nodeID.Int64] = worker
		} else if worker.mode == service.OpenCodeEgressModeServerDirect {
			copyWorker := worker
			directWorker = &copyWorker
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	eligible := make([]openCodeEligibleNode, 0)
	if pool.Enabled {
		nodeRows, err := tx.QueryContext(ctx, `
			SELECT n.id, n.proxy_id, n.display_name, LOWER(n.exit_ip), COALESCE(n.latency_ms, 0)
			FROM managed_proxy_nodes n
			JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE
			WHERE n.deleted_at IS NULL AND n.sync_status='active' AND n.health_status='healthy'
				AND n.duplicate_of_node_id IS NULL AND NULLIF(BTRIM(n.exit_ip),'') IS NOT NULL
				AND n.proxy_id IS NOT NULL AND n.last_probe_at >= NOW() - INTERVAL '26 hours'
			ORDER BY n.latency_ms ASC NULLS LAST, n.id`)
		if err != nil {
			return nil, err
		}
		for nodeRows.Next() {
			var node openCodeEligibleNode
			if err := nodeRows.Scan(&node.id, &node.proxyID, &node.name, &node.exitIP, &node.latencyMs); err != nil {
				_ = nodeRows.Close()
				return nil, err
			}
			eligible = append(eligible, node)
		}
		if err := nodeRows.Close(); err != nil {
			return nil, err
		}
	}

	keptWorkers := make(map[int64]struct{})
	for _, node := range eligible {
		nodeID, proxyID := node.id, node.proxyID
		var workerID, accountID *int64
		if existing, ok := existingByNode[node.id]; ok {
			if existing.rateLimitResetAt.Valid && existing.rateLimitResetAt.Time.After(time.Now()) {
				if _, err := tx.ExecContext(ctx, `
					UPDATE opencode_pool_workers SET status='cooling', error_reason='rate_limited', updated_at=NOW()
					WHERE id=$1`, existing.id); err != nil {
					return nil, err
				}
				keptWorkers[existing.id] = struct{}{}
				continue
			}
			workerID, accountID = &existing.id, &existing.accountID
		}
		resolvedWorkerID, _, err := r.upsertOpenCodeWorkerAccount(
			ctx, tx, pool, workerID, accountID, &nodeID, &proxyID,
			service.OpenCodeEgressModeProxy, "OpenCode / "+node.name, node.exitIP, upstreamKey,
			openCodeWorkerPriority(node.latencyMs),
		)
		if err != nil {
			return nil, err
		}
		keptWorkers[resolvedWorkerID] = struct{}{}
	}

	if pool.Enabled && pool.IncludeServerDirect && directProbe != nil && directProbe.Success && strings.TrimSpace(directProbe.ExitIP) != "" {
		if directWorker != nil && directWorker.rateLimitResetAt.Valid && directWorker.rateLimitResetAt.Time.After(time.Now()) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE opencode_pool_workers SET status='cooling', error_reason='rate_limited', updated_at=NOW()
				WHERE id=$1`, directWorker.id); err != nil {
				return nil, err
			}
			keptWorkers[directWorker.id] = struct{}{}
		} else {
			var adoptedAccountID int64
			if directWorker != nil {
				adoptedAccountID = directWorker.accountID
			} else {
				err := tx.QueryRowContext(ctx, `
				SELECT a.id FROM accounts a
				LEFT JOIN opencode_pool_workers w ON w.account_id=a.id
				WHERE a.deleted_at IS NULL AND w.id IS NULL
					AND a.platform='openai' AND a.type='apikey'
					AND LOWER(COALESCE(a.extra->>'provider_mode',''))='opencode_zen'
					AND LOWER(COALESCE(a.extra->>'opencode_egress_mode',''))='server_direct'
				ORDER BY a.id LIMIT 1 FOR UPDATE OF a`).Scan(&adoptedAccountID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
			}
			if adoptedAccountID > 0 {
				var workerID *int64
				if directWorker != nil {
					workerID = &directWorker.id
				}
				resolvedWorkerID, _, err := r.upsertOpenCodeWorkerAccount(
					ctx, tx, pool, workerID, &adoptedAccountID, nil, nil,
					service.OpenCodeEgressModeServerDirect, "OpenCode / Server Direct",
					strings.ToLower(strings.TrimSpace(directProbe.ExitIP)), upstreamKey,
					openCodeWorkerPriority(directProbe.LatencyMs),
				)
				if err != nil {
					return nil, err
				}
				keptWorkers[resolvedWorkerID] = struct{}{}
			}
		}
	}

	for _, worker := range allExisting {
		if _, keep := keptWorkers[worker.id]; keep {
			continue
		}
		reason := "pool_disabled"
		if pool.Enabled {
			reason = "egress_unavailable"
			if worker.nodeID.Valid {
				_ = tx.QueryRowContext(ctx, `
					SELECT CASE
						WHEN deleted_at IS NOT NULL OR sync_status<>'active' THEN 'node_missing'
						WHEN health_status='rate_limited' THEN 'rate_limited'
						WHEN health_status='duplicate_exit' OR duplicate_of_node_id IS NOT NULL THEN 'duplicate_exit_ip'
						WHEN last_probe_at IS NULL OR last_probe_at < NOW() - INTERVAL '26 hours' THEN 'probe_stale'
						ELSE COALESCE(NULLIF(failure_type,''), 'transport_error') END
					FROM managed_proxy_nodes WHERE id=$1`, worker.nodeID.Int64).Scan(&reason)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE accounts SET status='disabled', schedulable=FALSE, error_message=$2, updated_at=NOW()
			WHERE id=$1`, worker.accountID, reason); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE opencode_pool_workers SET status='inactive', error_reason=$2, updated_at=NOW()
			WHERE id=$1`, worker.id, reason); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM opencode_egress_leases WHERE account_id=$1`, worker.accountID); err != nil {
			return nil, err
		}
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &worker.accountID, nil, buildSchedulerGroupPayload([]int64{pool.GroupID})); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	if _, err := tx.ExecContext(ctx, `
		UPDATE opencode_pools SET reconcile_status='ok', reconcile_error=NULL,
			last_reconciled_at=$2, updated_at=NOW() WHERE id=$1`, pool.ID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.ListOpenCodePoolWorkers(ctx, pool.ID)
}

func (r *openCodeProxyPoolRepository) BindAPIKeyToOpenCodePool(ctx context.Context, apiKeyID, poolID int64, boundBy *int64) (*service.OpenCodePoolBindingMutation, error) {
	var mutation service.OpenCodePoolBindingMutation
	err := r.db.QueryRowContext(ctx, `
		WITH target AS (
			SELECT id, user_id FROM api_keys WHERE id=$1 AND deleted_at IS NULL
		), bound AS (
			INSERT INTO api_key_opencode_bindings (api_key_id, pool_id, bound_by)
			SELECT id, $2, $3 FROM target
			ON CONFLICT (api_key_id) DO UPDATE SET pool_id=EXCLUDED.pool_id,
				bound_by=EXCLUDED.bound_by, updated_at=NOW()
			RETURNING api_key_id, pool_id
		)
		SELECT bound.api_key_id, bound.pool_id, target.user_id
		FROM bound JOIN target ON target.id=bound.api_key_id`, apiKeyID, poolID, boundBy).
		Scan(&mutation.APIKeyID, &mutation.PoolID, &mutation.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAPIKeyNotFound
	}
	return &mutation, err
}

func (r *openCodeProxyPoolRepository) UnbindAPIKeyFromOpenCodePool(ctx context.Context, apiKeyID int64) (*service.OpenCodePoolBindingMutation, error) {
	var mutation service.OpenCodePoolBindingMutation
	err := r.db.QueryRowContext(ctx, `
		WITH removed AS (
			DELETE FROM api_key_opencode_bindings WHERE api_key_id=$1
			RETURNING api_key_id, pool_id
		)
		SELECT removed.api_key_id, removed.pool_id, k.user_id
		FROM removed JOIN api_keys k ON k.id=removed.api_key_id`, apiKeyID).
		Scan(&mutation.APIKeyID, &mutation.PoolID, &mutation.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		var userID int64
		if lookupErr := r.db.QueryRowContext(ctx, `SELECT user_id FROM api_keys WHERE id=$1 AND deleted_at IS NULL`, apiKeyID).Scan(&userID); lookupErr != nil {
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, service.ErrAPIKeyNotFound
			}
			return nil, lookupErr
		}
		return &service.OpenCodePoolBindingMutation{APIKeyID: apiKeyID, UserID: userID}, nil
	}
	return &mutation, err
}

func (r *openCodeProxyPoolRepository) ListOpenCodeBoundUserIDs(ctx context.Context, poolID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT k.user_id
		FROM api_key_opencode_bindings b
		JOIN api_keys k ON k.id=b.api_key_id AND k.deleted_at IS NULL
		WHERE b.pool_id=$1`, poolID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *openCodeProxyPoolRepository) IsOpenCodeSystemWorkerAccount(ctx context.Context, accountID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM opencode_pool_workers WHERE account_id=$1)`, accountID).Scan(&exists)
	return exists, err
}

func (r *openCodeProxyPoolRepository) IsOpenCodeSystemGroup(ctx context.Context, groupID int64) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM opencode_pools WHERE group_id=$1)`, groupID).Scan(&exists)
	return exists, err
}
