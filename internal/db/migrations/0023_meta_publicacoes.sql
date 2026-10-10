-- Publicação efetiva na Meta: a campanha nasce PAUSADA na conta do cliente e liga com ATIVAR.

-- Referências de headline/texto que o cliente manda junto com os criativos (acumula, até 2000 caracteres).
ALTER TABLE campaign_drafts ADD COLUMN IF NOT EXISTS client_copy text NOT NULL DEFAULT ''
  CHECK (char_length(client_copy) <= 2000);

-- Uma publicação por rascunho. Falha pode ser refeita: a mesma linha volta para publishing.
CREATE TABLE IF NOT EXISTS meta_publications (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  draft_id bigint NOT NULL REFERENCES campaign_drafts(id),
  project_id bigint REFERENCES projects(id),
  meta_connection_id bigint NOT NULL REFERENCES meta_connections(id),
  status text NOT NULL DEFAULT 'publishing' CHECK (status IN ('publishing', 'paused', 'active', 'failed')),
  campaign_id text NOT NULL DEFAULT '',
  adset_id text NOT NULL DEFAULT '',
  ad_ids text[] NOT NULL DEFAULT '{}',
  error text NOT NULL DEFAULT '',
  warnings text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  activated_at timestamptz,
  -- Pausada ou ativa só existe com a campanha criada.
  CHECK (status NOT IN ('paused', 'active') OR campaign_id <> '')
);
CREATE UNIQUE INDEX IF NOT EXISTS meta_publications_draft_idx ON meta_publications (draft_id);
CREATE INDEX IF NOT EXISTS meta_publications_org_idx ON meta_publications (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS meta_publications_project_idx ON meta_publications (project_id);
CREATE INDEX IF NOT EXISTS meta_publications_connection_idx ON meta_publications (meta_connection_id);
ALTER TABLE meta_publications ENABLE ROW LEVEL SECURITY;
