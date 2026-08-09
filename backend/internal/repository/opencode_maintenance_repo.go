package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const openCodeMaintenanceJobColumns = `
	id, job_type, trigger_type, status, COALESCE(source_name, ''), subscription_id,
	total_nodes, processed_nodes, healthy_nodes, failed_nodes, rate_limited_nodes,
	duplicate_nodes, deleted_nodes, COALESCE(error_message, ''), started_at, finished_at,
	created_at, updated_at`

func scanOpenCodeMaintenanceJob(scanner interface{ Scan(...any) error }) (*service.OpenCodeMaintenanceJob, error) {
	var item service.OpenCodeMaintenanceJob
	err := scanner.Scan(
		&item.ID, &item.JobType, &item.TriggerType, &item.Status, &item.SourceName, &item.SubscriptionID,
		&item.TotalNodes, &item.ProcessedNodes, &item.HealthyNodes, &item.FailedNodes,
		&item.RateLimitedNodes, &item.DuplicateNodes, &item.DeletedNodes, &item.ErrorMessage,
		&item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt,
	)
	return &item, err
}

func (r *openCodeProxyPoolRepository) CreateMaintenanceJob(ctx context.Context, input service.OpenCodeMaintenanceJob, scheduleKey string) (*service.OpenCodeMaintenanceJob, error) {
	var key any
	if scheduleKey != "" {
		key = scheduleKey
	}
	return scanOpenCodeMaintenanceJob(r.db.QueryRowContext(ctx, `
		INSERT INTO opencode_maintenance_jobs (
			job_type, trigger_type, schedule_key, status, source_name, subscription_id, total_nodes
		) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7)
		ON CONFLICT (schedule_key) DO UPDATE SET schedule_key=EXCLUDED.schedule_key
		RETURNING `+openCodeMaintenanceJobColumns,
		input.JobType, input.TriggerType, key, input.Status, input.SourceName, input.SubscriptionID, input.TotalNodes,
	))
}

func (r *openCodeProxyPoolRepository) ClaimMaintenanceJob(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE opencode_maintenance_jobs SET status='running', started_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r *openCodeProxyPoolRepository) UpdateMaintenanceJob(ctx context.Context, input service.OpenCodeMaintenanceJob) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE opencode_maintenance_jobs SET status=$2, subscription_id=$3, total_nodes=$4,
			processed_nodes=$5, healthy_nodes=$6, failed_nodes=$7, rate_limited_nodes=$8,
			duplicate_nodes=$9, deleted_nodes=$10, error_message=NULLIF($11,''),
			started_at=$12, finished_at=$13, updated_at=NOW()
		WHERE id=$1`, input.ID, input.Status, input.SubscriptionID, input.TotalNodes,
		input.ProcessedNodes, input.HealthyNodes, input.FailedNodes, input.RateLimitedNodes,
		input.DuplicateNodes, input.DeletedNodes, truncateRunes(input.ErrorMessage, 2000),
		input.StartedAt, input.FinishedAt)
	return err
}

func (r *openCodeProxyPoolRepository) GetMaintenanceJob(ctx context.Context, id int64) (*service.OpenCodeMaintenanceJob, error) {
	return scanOpenCodeMaintenanceJob(r.db.QueryRowContext(ctx, `SELECT `+openCodeMaintenanceJobColumns+` FROM opencode_maintenance_jobs WHERE id=$1`, id))
}

func (r *openCodeProxyPoolRepository) GetLatestMaintenanceJob(ctx context.Context) (*service.OpenCodeMaintenanceJob, error) {
	item, err := scanOpenCodeMaintenanceJob(r.db.QueryRowContext(ctx, `SELECT `+openCodeMaintenanceJobColumns+` FROM opencode_maintenance_jobs ORDER BY id DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return item, err
}

func (r *openCodeProxyPoolRepository) ListRetryableNodeIDs(ctx context.Context, now time.Time, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT n.id FROM managed_proxy_nodes n
		JOIN proxy_subscriptions s ON s.id=n.subscription_id AND s.deleted_at IS NULL AND s.enabled=TRUE
		WHERE n.deleted_at IS NULL AND n.sync_status='active' AND n.retry_at IS NOT NULL AND n.retry_at <= $1
		ORDER BY n.retry_at, n.id LIMIT $2`, now, limit)
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

func (r *openCodeProxyPoolRepository) CreateUploadedSubscription(ctx context.Context, name string) (*service.ProxySubscription, error) {
	return r.CreateSubscription(ctx, service.ProxySubscription{
		Name: name, Enabled: true, SourceType: "upload", SyncIntervalMinutes: 360,
	})
}

func (r *openCodeProxyPoolRepository) CommitUploadedNodes(ctx context.Context, subscriptionID int64, drafts []service.ManagedProxyNodeDraft) error {
	return r.CommitSubscriptionSync(ctx, subscriptionID, drafts, "uploaded_http", "")
}

func (r *openCodeProxyPoolRepository) ListTombstonedNodeKeys(ctx context.Context, subscriptionID int64) (map[string]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT node_key FROM opencode_node_tombstones WHERE subscription_id=$1`, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = struct{}{}
	}
	return keys, rows.Err()
}

func (r *openCodeProxyPoolRepository) DeleteManagedNode(ctx context.Context, nodeID int64, reason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var subscriptionID int64
	var nodeKey string
	var proxyID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT subscription_id, node_key, proxy_id FROM managed_proxy_nodes
		WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, nodeID).Scan(&subscriptionID, &nodeKey, &proxyID)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrManagedProxyNodeNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO opencode_node_tombstones (subscription_id, node_key, reason)
		VALUES ($1,$2,$3) ON CONFLICT (subscription_id, node_key) DO UPDATE SET reason=EXCLUDED.reason, deleted_at=NOW()`,
		subscriptionID, nodeKey, reason); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM opencode_pool_workers WHERE managed_node_id=$1 FOR UPDATE`, nodeID)
	if err != nil {
		return err
	}
	accountIDs := make([]int64, 0)
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM opencode_egress_leases WHERE account_id=ANY($1)`, pq.Array(accountIDs)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM opencode_pool_workers WHERE managed_node_id=$1`, nodeID); err != nil {
		return err
	}
	if len(accountIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE accounts SET status='disabled', schedulable=FALSE, proxy_id=NULL,
				error_message='managed_node_deleted', deleted_at=NOW(), updated_at=NOW()
			WHERE id=ANY($1)`, pq.Array(accountIDs)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE managed_proxy_nodes SET duplicate_of_node_id=NULL, health_status='unprobed', retry_at=NOW(), updated_at=NOW()
		WHERE duplicate_of_node_id=$1 AND deleted_at IS NULL`, nodeID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM managed_proxy_nodes WHERE id=$1`, nodeID); err != nil {
		return err
	}
	if proxyID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM proxies WHERE id=$1`, proxyID.Int64); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE proxy_subscriptions SET node_count=(SELECT COUNT(*) FROM managed_proxy_nodes WHERE subscription_id=$1 AND deleted_at IS NULL), updated_at=NOW()
		WHERE id=$1`, subscriptionID); err != nil {
		return err
	}
	return tx.Commit()
}
