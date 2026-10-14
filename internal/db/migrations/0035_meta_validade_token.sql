-- Validade do acesso do cliente à Meta (debug_token: o menor entre expires_at e data_access_expires_at)
-- e quando ele foi avisado de que está vencendo. Nulo em token_expires_at = não vence ou não lido. Idempotente.
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS token_expires_at timestamptz;
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS token_warned_at timestamptz;
