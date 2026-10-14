-- Diagnóstico grátis da conta de anúncios, entregue uma vez por conexão (a isca depois de conectar a Meta).
-- A linha existe = o diagnóstico já foi reivindicado/entregue; falha ao ler a Meta apaga a linha para tentar de novo. Idempotente.
CREATE TABLE IF NOT EXISTS account_diagnostics (
  connection_id bigint PRIMARY KEY REFERENCES meta_connections(id) ON DELETE CASCADE,
  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  summary jsonb
);
ALTER TABLE account_diagnostics ENABLE ROW LEVEL SECURITY;
