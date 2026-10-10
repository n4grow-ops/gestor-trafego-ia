-- Testador automático: enquanto o app da Meta está em desenvolvimento, só testadores conectam a conta.
-- O cliente manda o link do perfil, a equipe adiciona como testador e o pedido fecha quando a conexão
-- Meta é concluída. Só tabelas novas, tudo com IF NOT EXISTS: idempotente e sem tocar no que já existe.

-- Pedido de inclusão do cliente como testador do app. Um pedido aberto (requested ou added) por telefone.
CREATE TABLE IF NOT EXISTS tester_requests (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  identity_id bigint NOT NULL REFERENCES whatsapp_identities(id),
  conversation_id bigint REFERENCES conversations(id),
  profile_url text NOT NULL CHECK (char_length(profile_url) BETWEEN 1 AND 500),
  ref_kind text NOT NULL CHECK (ref_kind IN ('id', 'username')),
  ref_value text NOT NULL CHECK (char_length(ref_value) BETWEEN 1 AND 100),
  status text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'added', 'accepted', 'canceled')),
  requested_at timestamptz NOT NULL DEFAULT now(),
  added_at timestamptz,
  added_by text,
  accepted_at timestamptz,
  reminded_at timestamptz,
  CHECK (status <> 'added' OR added_at IS NOT NULL),
  CHECK (status <> 'accepted' OR accepted_at IS NOT NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS tester_requests_open_idx ON tester_requests (identity_id) WHERE status IN ('requested', 'added');
CREATE INDEX IF NOT EXISTS tester_requests_status_idx ON tester_requests (status, requested_at, id);
CREATE INDEX IF NOT EXISTS tester_requests_org_idx ON tester_requests (organization_id);
CREATE INDEX IF NOT EXISTS tester_requests_identity_idx ON tester_requests (identity_id);
CREATE INDEX IF NOT EXISTS tester_requests_conversation_idx ON tester_requests (conversation_id) WHERE conversation_id IS NOT NULL;
ALTER TABLE tester_requests ENABLE ROW LEVEL SECURITY;

-- Testadores já vistos em GET /{app}/roles (IDs do app, não casam com o ID do perfil).
-- Serve para perceber testador novo, isto é, convite aceito.
CREATE TABLE IF NOT EXISTS meta_app_testers (
  user_id text PRIMARY KEY CHECK (char_length(user_id) BETWEEN 1 AND 64),
  first_seen_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE meta_app_testers ENABLE ROW LEVEL SECURITY;
