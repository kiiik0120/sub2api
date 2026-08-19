-- Kox extension migration 001.
-- Kept outside backend/migrations so upstream Sub2API migration updates never
-- own or reorder this business-specific schema.
ALTER TABLE kox_api_keys
    ADD COLUMN IF NOT EXISTS gateway_api_key_id BIGINT REFERENCES api_keys(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_kox_api_keys_gateway_api_key_id
    ON kox_api_keys(gateway_api_key_id)
    WHERE gateway_api_key_id IS NOT NULL;
