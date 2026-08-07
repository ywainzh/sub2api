CREATE TABLE IF NOT EXISTS opencode_pools (
    id BIGSERIAL PRIMARY KEY,
    singleton_key BOOLEAN NOT NULL DEFAULT TRUE,
    name VARCHAR(100) NOT NULL DEFAULT 'OpenCode Zen Pool',
    group_id BIGINT NOT NULL REFERENCES groups(id),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    upstream_api_key_ciphertext TEXT NULL,
    include_server_direct BOOLEAN NOT NULL DEFAULT TRUE,
    worker_concurrency INTEGER NOT NULL DEFAULT 1,
    reconcile_status VARCHAR(24) NOT NULL DEFAULT 'pending',
    reconcile_error TEXT NULL,
    last_reconciled_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT opencode_pools_singleton_key_key UNIQUE (singleton_key),
    CONSTRAINT opencode_pools_group_id_key UNIQUE (group_id),
    CONSTRAINT opencode_pools_worker_concurrency_check CHECK (worker_concurrency BETWEEN 1 AND 100),
    CONSTRAINT opencode_pools_singleton_check CHECK (singleton_key)
);

CREATE TABLE IF NOT EXISTS opencode_pool_workers (
    id BIGSERIAL PRIMARY KEY,
    pool_id BIGINT NOT NULL REFERENCES opencode_pools(id) ON DELETE CASCADE,
    managed_node_id BIGINT NULL REFERENCES managed_proxy_nodes(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    egress_mode VARCHAR(24) NOT NULL,
    exit_ip VARCHAR(64) NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'inactive',
    error_reason VARCHAR(64) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT opencode_pool_workers_account_id_key UNIQUE (account_id),
    CONSTRAINT opencode_pool_workers_pool_node_key UNIQUE (pool_id, managed_node_id),
    CONSTRAINT opencode_pool_workers_egress_mode_check CHECK (egress_mode IN ('proxy', 'server_direct')),
    CONSTRAINT opencode_pool_workers_node_mode_check CHECK (
        (egress_mode = 'proxy' AND managed_node_id IS NOT NULL)
        OR (egress_mode = 'server_direct' AND managed_node_id IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS opencode_pool_workers_pool_status
    ON opencode_pool_workers (pool_id, status);
CREATE INDEX IF NOT EXISTS opencode_pool_workers_managed_node_id
    ON opencode_pool_workers (managed_node_id);
CREATE UNIQUE INDEX IF NOT EXISTS opencode_pool_workers_single_direct
    ON opencode_pool_workers (pool_id)
    WHERE egress_mode = 'server_direct';

CREATE TABLE IF NOT EXISTS api_key_opencode_bindings (
    api_key_id BIGINT PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    pool_id BIGINT NOT NULL REFERENCES opencode_pools(id) ON DELETE CASCADE,
    bound_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS api_key_opencode_bindings_pool_id
    ON api_key_opencode_bindings (pool_id);

-- A request-time 429 already persists account cooldown through the normal
-- gateway path. Reflect that state in the worker table and immediately release
-- the egress lease so another recovered worker can claim the address. The
-- periodic reconciler will restore the worker after the cooldown expires.
CREATE OR REPLACE FUNCTION sync_opencode_worker_account_cooldown()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.rate_limit_reset_at IS NOT NULL
       AND NEW.rate_limit_reset_at > NOW()
       AND (OLD.rate_limit_reset_at IS DISTINCT FROM NEW.rate_limit_reset_at) THEN
        UPDATE opencode_pool_workers
        SET status = 'cooling', error_reason = 'rate_limited', updated_at = NOW()
        WHERE account_id = NEW.id;
        DELETE FROM opencode_egress_leases
        WHERE account_id = NEW.id
          AND EXISTS (SELECT 1 FROM opencode_pool_workers w WHERE w.account_id = NEW.id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_opencode_worker_account_cooldown ON accounts;
CREATE TRIGGER trg_sync_opencode_worker_account_cooldown
AFTER UPDATE OF rate_limit_reset_at ON accounts
FOR EACH ROW EXECUTE FUNCTION sync_opencode_worker_account_cooldown();
