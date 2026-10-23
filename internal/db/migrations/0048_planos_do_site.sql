-- Planos do site (23/10): Básico R$ 147 (4 campanhas/mês), Intermediário R$ 397 (35) e Avançado R$ 997
-- (ilimitado, com laboratório de vídeo). Campanhas por mês: 0 = sem limite. Os três ficam disponíveis no
-- cadastro; a cobrança entra depois. Idempotente.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS max_campaigns_month int NOT NULL DEFAULT 0 CHECK (max_campaigns_month >= 0);

UPDATE plans SET name = 'Básico', price_cents = 14700, max_campaigns_month = 4, available = true, visible = true,
  sort_order = 1, updated_at = now() WHERE code = 'free';
UPDATE plans SET name = 'Intermediário', price_cents = 39700, max_campaigns_month = 35, available = true, visible = true,
  sort_order = 2, updated_at = now() WHERE code = 'starter';
UPDATE plans SET name = 'Avançado', price_cents = 99700, max_campaigns_month = 0, available = true, visible = true,
  sort_order = 3, updated_at = now() WHERE code = 'pro';
