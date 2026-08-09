ALTER TABLE proxy_subscriptions
    ADD COLUMN IF NOT EXISTS source_type VARCHAR(24) NOT NULL DEFAULT 'url';

ALTER TABLE proxy_subscriptions ALTER COLUMN url_ciphertext DROP NOT NULL;

ALTER TABLE proxy_subscriptions DROP CONSTRAINT IF EXISTS proxy_subscriptions_source_type_check;
ALTER TABLE proxy_subscriptions ADD CONSTRAINT proxy_subscriptions_source_type_check CHECK (
    (source_type = 'url' AND url_ciphertext IS NOT NULL)
    OR (source_type = 'upload' AND url_ciphertext IS NULL)
);

ALTER TABLE managed_proxy_nodes
    ADD COLUMN IF NOT EXISTS transport_mode VARCHAR(24) NOT NULL DEFAULT 'mihomo_listener',
    ADD COLUMN IF NOT EXISTS unavailable_since TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS last_successful_probe_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS retry_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS retry_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE managed_proxy_nodes ALTER COLUMN listener_port DROP NOT NULL;
ALTER TABLE managed_proxy_nodes DROP CONSTRAINT IF EXISTS managed_proxy_nodes_transport_mode_check;
ALTER TABLE managed_proxy_nodes ADD CONSTRAINT managed_proxy_nodes_transport_mode_check CHECK (
    (transport_mode = 'mihomo_listener' AND listener_port IS NOT NULL)
    OR (transport_mode = 'direct_http' AND listener_port IS NULL AND protocol IN ('http', 'https'))
);

CREATE INDEX IF NOT EXISTS managed_proxy_nodes_retry_at
    ON managed_proxy_nodes (retry_at)
    WHERE deleted_at IS NULL AND retry_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS managed_proxy_nodes_unavailable_since
    ON managed_proxy_nodes (unavailable_since)
    WHERE deleted_at IS NULL AND unavailable_since IS NOT NULL;

CREATE TABLE IF NOT EXISTS opencode_node_tombstones (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES proxy_subscriptions(id) ON DELETE CASCADE,
    node_key VARCHAR(64) NOT NULL,
    reason VARCHAR(64) NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT opencode_node_tombstones_subscription_node_key UNIQUE (subscription_id, node_key)
);

CREATE TABLE IF NOT EXISTS opencode_maintenance_jobs (
    id BIGSERIAL PRIMARY KEY,
    job_type VARCHAR(24) NOT NULL,
    trigger_type VARCHAR(24) NOT NULL,
    schedule_key VARCHAR(64) NULL UNIQUE,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    source_name VARCHAR(100) NULL,
    subscription_id BIGINT NULL REFERENCES proxy_subscriptions(id) ON DELETE SET NULL,
    total_nodes INTEGER NOT NULL DEFAULT 0,
    processed_nodes INTEGER NOT NULL DEFAULT 0,
    healthy_nodes INTEGER NOT NULL DEFAULT 0,
    failed_nodes INTEGER NOT NULL DEFAULT 0,
    rate_limited_nodes INTEGER NOT NULL DEFAULT 0,
    duplicate_nodes INTEGER NOT NULL DEFAULT 0,
    deleted_nodes INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NULL,
    started_at TIMESTAMPTZ NULL,
    finished_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT opencode_maintenance_jobs_type_check CHECK (job_type IN ('import', 'probe')),
    CONSTRAINT opencode_maintenance_jobs_status_check CHECK (status IN ('pending', 'running', 'completed', 'failed'))
);

CREATE INDEX IF NOT EXISTS opencode_maintenance_jobs_created_at
    ON opencode_maintenance_jobs (created_at DESC);
