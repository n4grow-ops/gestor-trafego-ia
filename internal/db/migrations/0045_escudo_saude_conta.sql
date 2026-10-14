-- Escudo anti-bloqueio: último estado de saúde da conta de anúncios de cada conexão ativa. O job periódico
-- compara o estado de agora com o último avisado e só avisa o cliente quando muda (uma vez por mudança).
-- Idempotente.
CREATE TABLE IF NOT EXISTS shield_account_state (
  connection_id bigint PRIMARY KEY REFERENCES meta_connections(id) ON DELETE CASCADE,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  state text NOT NULL DEFAULT 'ok',
  account_status int NOT NULL DEFAULT 0,
  disable_reason int NOT NULL DEFAULT 0,
  -- Último estado de que o cliente foi avisado ('ok' = nada pendente). Diferente de state = aviso devido.
  notified_state text NOT NULL DEFAULT 'ok',
  notified_at timestamptz,
  checked_at timestamptz NOT NULL DEFAULT now(),
  changed_at timestamptz NOT NULL DEFAULT now(),
  -- Última checagem completa (itens com mensagem e passo a passo), para o painel.
  report jsonb
);
CREATE INDEX IF NOT EXISTS shield_account_state_org_idx ON shield_account_state (organization_id);
ALTER TABLE shield_account_state ENABLE ROW LEVEL SECURITY;
