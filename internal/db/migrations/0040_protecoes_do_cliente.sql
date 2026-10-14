-- Proteções escolhidas pelo cliente: teto de gasto diário, custo máximo por conversa e pausa automática.
-- Sem linha (ou sem limite preenchido) nada é ligado: o padrão é desligado. Idempotente.
CREATE TABLE IF NOT EXISTS guardrail_rules (
  organization_id bigint PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  daily_cap_cents bigint CHECK (daily_cap_cents IS NULL OR daily_cap_cents > 0),
  max_cost_per_conv_cents bigint CHECK (max_cost_per_conv_cents IS NULL OR max_cost_per_conv_cents > 0),
  auto_pause boolean NOT NULL DEFAULT true,
  offered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE guardrail_rules ENABLE ROW LEVEL SECURITY;

-- Cada vez que uma regra do cliente é violada com dados suficientes: o motivo, os números e o que foi feito.
-- A chave (campanha, regra, início da janela) garante um evento só por violação: nada pausa nem avisa duas vezes.
CREATE TABLE IF NOT EXISTS guardrail_events (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  publication_id bigint NOT NULL REFERENCES meta_publications(id) ON DELETE CASCADE,
  rule text NOT NULL CHECK (rule IN ('daily_cap', 'max_cost')),
  window_start timestamptz NOT NULL,
  threshold_cents bigint NOT NULL,
  observed_cents bigint NOT NULL,
  spend_cents bigint NOT NULL DEFAULT 0,
  conversations bigint NOT NULL DEFAULT 0,
  reason text NOT NULL DEFAULT '',
  auto_pause boolean NOT NULL DEFAULT true,
  action text NOT NULL DEFAULT '',          -- paused | notified | cancelled | skipped (vazio = em andamento)
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (publication_id, rule, window_start)
);
CREATE INDEX IF NOT EXISTS guardrail_events_org_idx ON guardrail_events (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS guardrail_events_open_idx ON guardrail_events (id) WHERE completed_at IS NULL;
ALTER TABLE guardrail_events ENABLE ROW LEVEL SECURITY;
