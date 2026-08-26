-- Fork-local Kox mapping. Applied after upstream migrations so this file never
-- shares an ordering namespace with backend/migrations/.
ALTER TABLE kox_api_keys
    ADD COLUMN IF NOT EXISTS gateway_api_key_id BIGINT REFERENCES api_keys(id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_kox_api_keys_gateway_key
    ON kox_api_keys(gateway_api_key_id)
    WHERE gateway_api_key_id IS NOT NULL;
