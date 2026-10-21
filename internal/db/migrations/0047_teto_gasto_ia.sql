-- Teto de gasto da IA: quanto cada chave do OpenRouter já gastou neste sistema. A chave entra só pela
-- impressão (sha256 curto), nunca o valor. Chave nova começa do zero. Idempotente.
CREATE TABLE IF NOT EXISTS llm_budget (
  key_id text PRIMARY KEY,
  spent_usd numeric(12, 6) NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE llm_budget ENABLE ROW LEVEL SECURITY;
