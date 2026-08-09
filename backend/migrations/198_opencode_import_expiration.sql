ALTER TABLE proxy_subscriptions
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NULL;

ALTER TABLE proxy_subscriptions DROP CONSTRAINT IF EXISTS proxy_subscriptions_expiration_source_check;
ALTER TABLE proxy_subscriptions ADD CONSTRAINT proxy_subscriptions_expiration_source_check CHECK (
    expires_at IS NULL OR source_type = 'upload'
);

CREATE INDEX IF NOT EXISTS proxy_subscriptions_upload_expiration
    ON proxy_subscriptions (expires_at, id)
    WHERE source_type = 'upload' AND expires_at IS NOT NULL AND deleted_at IS NULL;
