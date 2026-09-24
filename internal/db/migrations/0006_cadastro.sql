-- Cadastro do cliente: dados da pessoa e do negócio, link de cadastro, aceite de termos,
-- planos, assinatura e pagamentos (estrutura pronta; cobrança entra depois).

ALTER TABLE members
  ADD COLUMN full_name text,
  ADD COLUMN email text,
  ADD COLUMN email_verified_at timestamptz,
  ADD COLUMN auth_user_id uuid,
  ADD COLUMN cpf text CHECK (cpf ~ '^[0-9]{11}$');
CREATE INDEX members_auth_user_idx ON members (auth_user_id);
CREATE INDEX members_email_idx ON members (lower(email));

-- Organizações do cadastro automático ficam pendentes e completam o cadastro na próxima mensagem;
-- as criadas pelo admin (clientes trazidos à mão) já contam como cadastradas.
ALTER TABLE organizations
  ADD COLUMN registration_status text NOT NULL DEFAULT 'pending' CHECK (registration_status IN ('pending', 'complete')),
  ADD COLUMN registered_at timestamptz,
  ADD COLUMN legal_name text,
  ADD COLUMN trade_name text,
  ADD COLUMN cnpj text CHECK (cnpj ~ '^[0-9]{14}$'),
  ADD COLUMN segment text,
  ADD COLUMN city text,
  ADD COLUMN uf text CHECK (uf ~ '^[A-Z]{2}$'),
  ADD COLUMN website text,
  ADD COLUMN instagram text,
  ADD COLUMN ads_objective text,
  ADD COLUMN monthly_budget_range text,
  ADD COLUMN ads_experience text,
  ADD COLUMN has_meta_ad_account text,
  ADD COLUMN approver text,
  ADD COLUMN referral_source text;
UPDATE organizations SET registration_status = 'complete', registered_at = now() WHERE signup_source = 'admin';

CREATE TABLE signup_links (
  id bigserial PRIMARY KEY,
  identity_id bigint NOT NULL REFERENCES whatsapp_identities(id),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signup_links_identity_idx ON signup_links (identity_id, created_at DESC);
ALTER TABLE signup_links ENABLE ROW LEVEL SECURITY;

CREATE TABLE consents (
  id bigserial PRIMARY KEY,
  member_id bigint NOT NULL REFERENCES members(id),
  kind text NOT NULL CHECK (kind IN ('terms', 'privacy')),
  version text NOT NULL,
  accepted_at timestamptz NOT NULL DEFAULT now(),
  ip text,
  user_agent text
);
CREATE INDEX consents_member_idx ON consents (member_id);
ALTER TABLE consents ENABLE ROW LEVEL SECURITY;

CREATE TABLE plans (
  code text PRIMARY KEY,
  name text NOT NULL,
  price_cents int NOT NULL CHECK (price_cents >= 0),
  currency text NOT NULL DEFAULT 'BRL',
  billing_interval text NOT NULL DEFAULT 'month' CHECK (billing_interval IN ('month', 'year')),
  daily_message_limit int NOT NULL CHECK (daily_message_limit > 0),
  max_daily_budget_brl numeric(12,2) NOT NULL CHECK (max_daily_budget_brl > 0),
  max_projects int NOT NULL CHECK (max_projects > 0),
  available boolean NOT NULL DEFAULT false,
  sort_order int NOT NULL DEFAULT 0
);
ALTER TABLE plans ENABLE ROW LEVEL SECURITY;

-- Starter e Pro com valores provisórios; só o Free está disponível.
INSERT INTO plans (code, name, price_cents, daily_message_limit, max_daily_budget_brl, max_projects, available, sort_order) VALUES
  ('free', 'Free', 0, 60, 50, 3, true, 1),
  ('starter', 'Starter', 9700, 200, 300, 10, false, 2),
  ('pro', 'Pro', 29700, 1000, 2000, 50, false, 3);

CREATE TABLE subscriptions (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL UNIQUE REFERENCES organizations(id),
  plan_code text NOT NULL REFERENCES plans(code),
  status text NOT NULL CHECK (status IN ('active', 'trialing', 'past_due', 'canceled')),
  started_at timestamptz NOT NULL DEFAULT now(),
  current_period_end timestamptz,
  provider text,
  provider_ref text,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX subscriptions_plan_idx ON subscriptions (plan_code);
ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;

CREATE TABLE payments (
  id bigserial PRIMARY KEY,
  subscription_id bigint NOT NULL REFERENCES subscriptions(id),
  amount_cents int NOT NULL CHECK (amount_cents >= 0),
  currency text NOT NULL DEFAULT 'BRL',
  method text NOT NULL CHECK (method IN ('card', 'pix', 'boleto')),
  status text NOT NULL CHECK (status IN ('pending', 'paid', 'failed', 'refunded')),
  provider text NOT NULL,
  provider_ref text,
  paid_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payments_subscription_idx ON payments (subscription_id);
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
