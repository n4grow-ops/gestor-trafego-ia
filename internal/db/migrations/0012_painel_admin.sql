-- Painel da equipe (admin.meuanuncio.io): quem entra, o que cada um fez, atendimento humano
-- pelo painel e registro manual de pagamento.

-- Só entra no painel quem está aqui e ativo. O e-mail é o mesmo do login (código pelo Supabase Auth).
CREATE TABLE team_members (
  id bigserial PRIMARY KEY,
  email text NOT NULL CHECK (email = lower(btrim(email)) AND email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'),
  name text NOT NULL DEFAULT '' CHECK (char_length(name) <= 80),
  role text NOT NULL CHECK (role IN ('admin', 'staff')),
  active boolean NOT NULL DEFAULT true,
  added_by text,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS team_members_email_idx ON team_members (email);
ALTER TABLE team_members ENABLE ROW LEVEL SECURITY;

-- Toda ação feita pelo painel. actor_email fica mesmo depois de o membro sair da equipe.
CREATE TABLE admin_audit_log (
  id bigserial PRIMARY KEY,
  actor_id bigint REFERENCES team_members(id) ON DELETE SET NULL,
  actor_email text NOT NULL,
  action text NOT NULL,
  target_type text NOT NULL DEFAULT '',
  target_id text NOT NULL DEFAULT '',
  target_label text NOT NULL DEFAULT '',
  data jsonb NOT NULL DEFAULT '{}',
  ip text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS admin_audit_log_created_idx ON admin_audit_log (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS admin_audit_log_actor_idx ON admin_audit_log (actor_email, created_at DESC);
CREATE INDEX IF NOT EXISTS admin_audit_log_target_idx ON admin_audit_log (target_type, target_id, created_at DESC);
ALTER TABLE admin_audit_log ENABLE ROW LEVEL SECURITY;

-- Atendimento humano: quem assumiu, desde quando e o estado para onde a conversa volta.
-- human_by NULL com state = needs_human = o cliente pediu uma pessoa e ninguém assumiu ainda.
ALTER TABLE conversations
  ADD COLUMN human_by text,
  ADD COLUMN human_since timestamptz,
  ADD COLUMN human_prev_state text;
CREATE INDEX IF NOT EXISTS conversations_needs_human_idx ON conversations (updated_at) WHERE state = 'needs_human';

-- Mensagens escritas por alguém da equipe (kind = 'staff'): quem escreveu.
ALTER TABLE messages ADD COLUMN author text;
CREATE INDEX IF NOT EXISTS messages_created_idx ON messages (created_at);

-- Pagamento registrado à mão enquanto não há gateway (provider = 'manual').
ALTER TABLE payments
  ADD COLUMN note text CHECK (note IS NULL OR char_length(note) <= 200),
  ADD COLUMN recorded_by text;
CREATE INDEX IF NOT EXISTS payments_paid_idx ON payments (paid_at) WHERE status = 'paid';
CREATE INDEX IF NOT EXISTS payments_created_idx ON payments (created_at);

CREATE INDEX IF NOT EXISTS llm_usage_created_idx ON llm_usage (created_at);
CREATE INDEX IF NOT EXISTS outbox_failed_idx ON outbox_jobs (updated_at) WHERE status = 'failed';
