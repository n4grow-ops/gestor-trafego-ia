-- Resultado diário de cada campanha (insights da Meta do dia, em BRT) e a última leitura do pagamento da
-- conta de anúncios (para o painel). Idempotente.
CREATE TABLE IF NOT EXISTS campaign_results (
  publication_id bigint NOT NULL REFERENCES meta_publications(id) ON DELETE CASCADE,
  day date NOT NULL,
  spend_cents bigint NOT NULL DEFAULT 0,
  impressions bigint NOT NULL DEFAULT 0,
  reach bigint NOT NULL DEFAULT 0,
  conversations bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (publication_id, day)
);
ALTER TABLE campaign_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS funding jsonb;
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS funding_checked_at timestamptz;
