CREATE TABLE IF NOT EXISTS proxy_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    url_ciphertext TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sync_interval_minutes INTEGER NOT NULL DEFAULT 360,
    last_fetched_at TIMESTAMPTZ NULL,
    last_success_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    node_count INTEGER NOT NULL DEFAULT 0,
    last_format VARCHAR(32) NULL,
    last_user_agent VARCHAR(160) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS proxy_subscriptions_enabled ON proxy_subscriptions (enabled);
CREATE INDEX IF NOT EXISTS proxy_subscriptions_deleted_at ON proxy_subscriptions (deleted_at);

CREATE TABLE IF NOT EXISTS managed_proxy_nodes (
    id BIGSERIAL PRIMARY KEY,
    subscription_id BIGINT NOT NULL REFERENCES proxy_subscriptions(id),
    proxy_id BIGINT NULL REFERENCES proxies(id),
    node_key VARCHAR(64) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    mihomo_name VARCHAR(255) NOT NULL,
    protocol VARCHAR(32) NOT NULL,
    config_ciphertext TEXT NOT NULL,
    listener_port INTEGER NOT NULL,
    sync_status VARCHAR(24) NOT NULL DEFAULT 'active',
    health_status VARCHAR(32) NOT NULL DEFAULT 'unprobed',
    last_seen_at TIMESTAMPTZ NULL,
    exit_ip VARCHAR(64) NULL,
    country VARCHAR(100) NULL,
    region VARCHAR(160) NULL,
    latency_ms BIGINT NULL,
    opencode_http_status INTEGER NULL,
    failure_type VARCHAR(32) NULL,
    failure_message TEXT NULL,
    last_probe_at TIMESTAMPTZ NULL,
    duplicate_of_node_id BIGINT NULL REFERENCES managed_proxy_nodes(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL,
    CONSTRAINT managed_proxy_nodes_subscription_node_key UNIQUE (subscription_id, node_key),
    CONSTRAINT managed_proxy_nodes_proxy_id_key UNIQUE (proxy_id),
    CONSTRAINT managed_proxy_nodes_listener_port_key UNIQUE (listener_port)
);

CREATE INDEX IF NOT EXISTS managed_proxy_nodes_health_status ON managed_proxy_nodes (health_status);
CREATE INDEX IF NOT EXISTS managed_proxy_nodes_exit_ip ON managed_proxy_nodes (exit_ip);
CREATE INDEX IF NOT EXISTS managed_proxy_nodes_deleted_at ON managed_proxy_nodes (deleted_at);

CREATE TABLE IF NOT EXISTS opencode_egress_leases (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    proxy_id BIGINT NULL REFERENCES proxies(id),
    egress_mode VARCHAR(24) NOT NULL,
    exit_ip VARCHAR(64) NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT opencode_egress_leases_account_id_key UNIQUE (account_id),
    CONSTRAINT opencode_egress_leases_exit_ip_key UNIQUE (exit_ip)
);

CREATE INDEX IF NOT EXISTS opencode_egress_leases_proxy_id ON opencode_egress_leases (proxy_id);
