-- Conexão da conta de anúncios Meta do cliente (Facebook Login for Business).
CREATE TABLE meta_connect_links (
  id bigserial PRIMARY KEY,
  identity_id bigint NOT NULL REFERENCES whatsapp_identities(id),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX meta_connect_links_identity_idx ON meta_connect_links (identity_id, created_at DESC);
ALTER TABLE meta_connect_links ENABLE ROW LEVEL SECURITY;

CREATE TABLE meta_connections (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  connected_by bigint NOT NULL REFERENCES whatsapp_identities(id),
  link_id bigint NOT NULL REFERENCES meta_connect_links(id),
  meta_user_id text NOT NULL,
  business_id text,
  ad_account_id text,
  ad_account_name text,
  currency text,
  timezone_name text,
  account_status int,
  page_id text,
  page_name text,
  instagram_id text,
  instagram_username text,
  page_whatsapp boolean NOT NULL DEFAULT false,
  -- Token do cliente cifrado (AES-256-GCM, chave META_TOKEN_KEY). NULL depois de substituído ou revogado.
  token_ciphertext bytea,
  -- Contas e Páginas liberadas no login, para a escolha; limpo na conclusão.
  candidates jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'replaced', 'revoked')),
  deletion_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  connected_at timestamptz,
  revoked_at timestamptz
);
CREATE UNIQUE INDEX meta_connections_active_idx ON meta_connections (organization_id) WHERE status = 'active';
CREATE INDEX meta_connections_user_idx ON meta_connections (meta_user_id);
CREATE INDEX meta_connections_link_idx ON meta_connections (link_id, created_at DESC);
CREATE INDEX meta_connections_deletion_idx ON meta_connections (deletion_code) WHERE deletion_code IS NOT NULL;
ALTER TABLE meta_connections ENABLE ROW LEVEL SECURITY;
