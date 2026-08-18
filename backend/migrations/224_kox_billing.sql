-- Kox billing state is isolated from the user-facing api_keys table.  In
-- particular, the credential digest below is never usable as a bearer token.
CREATE TABLE IF NOT EXISTS kox_service_accounts (
    account_id UUID PRIMARY KEY,
    kox_company_id TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS kox_api_keys (
    api_key_id UUID PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES kox_service_accounts(account_id),
    kox_user_id TEXT NOT NULL,
    key_digest CHAR(64) NOT NULL UNIQUE,
    key_fingerprint VARCHAR(24) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'rotated')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    disabled_at TIMESTAMPTZ NULL
);
CREATE INDEX IF NOT EXISTS idx_kox_api_keys_account_user ON kox_api_keys(account_id, kox_user_id);

CREATE TABLE IF NOT EXISTS kox_usage_logs (
    usage_log_id UUID PRIMARY KEY,
    api_key_id UUID NOT NULL REFERENCES kox_api_keys(api_key_id),
    provider_request_id TEXT NOT NULL UNIQUE,
    request_id TEXT NOT NULL,
    reservation_id TEXT NOT NULL,
    business_code TEXT NOT NULL,
    model TEXT NOT NULL,
    billing_type TEXT NOT NULL DEFAULT 'token',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    actual_cost NUMERIC(20, 12) NOT NULL,
    currency CHAR(3) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed')),
    occurred_at TIMESTAMPTZ NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_kox_usage_request_id ON kox_usage_logs(request_id);
CREATE INDEX IF NOT EXISTS idx_kox_usage_key_occurred ON kox_usage_logs(api_key_id, occurred_at);

CREATE TABLE IF NOT EXISTS kox_billing_outbox (
    event_id UUID PRIMARY KEY,
    usage_log_id UUID NOT NULL REFERENCES kox_usage_logs(usage_log_id),
    revision INTEGER NOT NULL,
    payload JSONB NOT NULL,
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'pending' CHECK (delivery_status IN ('pending', 'delivered', 'dead_letter', 'blocked')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NULL,
    last_response TEXT NULL,
    first_attempted_at TIMESTAMPTZ NULL,
    last_attempted_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    claimed_at TIMESTAMPTZ NULL,
    claimed_by TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (usage_log_id, revision)
);
CREATE INDEX IF NOT EXISTS idx_kox_billing_outbox_pending ON kox_billing_outbox(next_attempt_at, created_at)
    WHERE delivery_status = 'pending';
