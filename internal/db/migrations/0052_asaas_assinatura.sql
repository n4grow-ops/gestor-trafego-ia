-- Cobrança pelo Asaas (31/10): assinatura mensal por Pix ou cartão; o sistema libera quando o pagamento
-- confirma. pending = escolheu (ou ainda vai escolher) o plano e não pagou. past_due_since marca o início do
-- atraso: 3 dias depois, a Mia não cria campanha nova. Idempotente.
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check
  CHECK (status IN ('pending', 'active', 'trialing', 'past_due', 'canceled'));

ALTER TABLE subscriptions
  ADD COLUMN IF NOT EXISTS past_due_since timestamptz,
  -- Escolha em andamento no WhatsApp: plano, forma (pix ou card) e o link da primeira cobrança.
  ADD COLUMN IF NOT EXISTS pending_plan text REFERENCES plans(code),
  ADD COLUMN IF NOT EXISTS pending_method text CHECK (pending_method IS NULL OR pending_method IN ('pix', 'card')),
  ADD COLUMN IF NOT EXISTS checkout_url text;
-- O webhook acha a assinatura pelo id do Asaas (sub_...).
CREATE INDEX IF NOT EXISTS subscriptions_provider_ref_idx ON subscriptions (provider, provider_ref) WHERE provider_ref IS NOT NULL;

-- Cliente no Asaas (cus_...): criado uma vez por organização e reaproveitado.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS asaas_customer_id text;

-- Cobrança do Asaas reenviada (PAYMENT_CREATED, depois RECEIVED) atualiza a mesma linha.
CREATE UNIQUE INDEX IF NOT EXISTS payments_provider_ref_idx ON payments (provider, provider_ref) WHERE provider_ref IS NOT NULL;

-- Eventos do webhook já processados: o Asaas reenvia e o mesmo evento não pode valer duas vezes.
CREATE TABLE IF NOT EXISTS asaas_events (
  id text PRIMARY KEY,
  event text NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE asaas_events ENABLE ROW LEVEL SECURITY;
