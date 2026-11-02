-- Checkout próprio (02/11): a Mia manda /pagar/{token} em vez da página do Asaas. Um link por cobrança
-- (pay_...), amarrado à organização e à assinatura. Só o SHA-256 do token fica no banco; o token cru vai
-- na URL e em lugar nenhum mais. Nada de dado de cartão aqui: número, CVV e validade só passam em memória.
-- method: aba que abre primeiro. failed_attempts/locked_until: bloqueio depois de N cartões recusados seguidos (teste de cartão roubado).
-- Idempotente.
CREATE TABLE IF NOT EXISTS checkout_links (
  id bigserial PRIMARY KEY,
  token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  subscription_id bigint NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  asaas_subscription text NOT NULL,
  asaas_payment text NOT NULL,
  plan_name text NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  due_date date NOT NULL,
  expires_at timestamptz NOT NULL,
  failed_attempts int NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
  locked_until timestamptz,
  card_paid_at timestamptz, -- cartão aprovado aqui; a liberação continua sendo pelo webhook
  -- Forma escolhida no WhatsApp: a página abre nessa aba (dá para trocar lá dentro).
  method text NOT NULL DEFAULT 'pix' CHECK (method IN ('pix', 'card')),
  -- Cobrança de cartão trocada para Pix na página: pago o Pix, a assinatura passa para Pix (a recorrência
  -- segue a forma que o cliente usou).
  pix_switched_at timestamptz,
  -- Cobrança no cartão em andamento (trava entre réplicas): dois envios juntos não cobram duas vezes.
  charging_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS checkout_links_org_idx ON checkout_links (organization_id);
CREATE INDEX IF NOT EXISTS checkout_links_payment_idx ON checkout_links (asaas_payment);
ALTER TABLE checkout_links ENABLE ROW LEVEL SECURITY;
