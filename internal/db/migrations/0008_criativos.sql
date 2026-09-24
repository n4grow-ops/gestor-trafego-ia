-- Criativos (imagens e vídeos) enviados pelo cliente depois de aprovar a proposta.
CREATE TABLE creatives (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  draft_id bigint NOT NULL REFERENCES campaign_drafts(id),
  project_id bigint REFERENCES projects(id),
  kind text NOT NULL CHECK (kind IN ('image', 'video')),
  storage_path text NOT NULL,
  mime text NOT NULL,
  size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX creatives_draft_idx ON creatives (draft_id);
CREATE INDEX creatives_org_idx ON creatives (organization_id, created_at);
ALTER TABLE creatives ENABLE ROW LEVEL SECURITY;

ALTER TABLE conversations DROP CONSTRAINT conversations_state_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_state_check
  CHECK (state IN ('idle', 'onboarding', 'collecting', 'awaiting_proposal', 'awaiting_approval', 'awaiting_creatives', 'needs_human'));
