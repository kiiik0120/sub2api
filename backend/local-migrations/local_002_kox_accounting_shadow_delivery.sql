-- Independent shadow delivery state for the unified accounting service.
-- Existing rows are disabled deliberately: enabling the new destination must
-- not replay historical production usage or alter legacy webhook delivery.
ALTER TABLE kox_billing_outbox
    ADD COLUMN IF NOT EXISTS accounting_delivery_status VARCHAR(16) NOT NULL DEFAULT 'disabled'
        CHECK (accounting_delivery_status IN ('disabled', 'pending', 'delivered', 'dead_letter', 'blocked')),
    ADD COLUMN IF NOT EXISTS accounting_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS accounting_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS accounting_last_error TEXT NULL,
    ADD COLUMN IF NOT EXISTS accounting_last_response TEXT NULL,
    ADD COLUMN IF NOT EXISTS accounting_first_attempted_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS accounting_last_attempted_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS accounting_completed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS accounting_claimed_at TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS accounting_claimed_by TEXT NULL;

CREATE INDEX IF NOT EXISTS idx_kox_billing_outbox_accounting_pending
    ON kox_billing_outbox(accounting_next_attempt_at, created_at)
    WHERE accounting_delivery_status = 'pending';
