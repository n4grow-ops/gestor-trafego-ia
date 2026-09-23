-- Etapa 2: memória do negócio, limites por cliente, cadastro automático e projetos.

ALTER TABLE organizations
  ADD COLUMN profile jsonb NOT NULL DEFAULT '{}',
  ADD COLUMN status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
  ADD COLUMN daily_message_limit int NOT NULL DEFAULT 60 CHECK (daily_message_limit > 0),
  ADD COLUMN signup_source text NOT NULL DEFAULT 'admin' CHECK (signup_source IN ('admin', 'auto'));

CREATE TABLE projects (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 60),
  stage text NOT NULL DEFAULT 'collecting'
    CHECK (stage IN ('collecting', 'awaiting_proposal', 'awaiting_approval', 'approved', 'archived')),
  fields jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX projects_org_name_idx ON projects (organization_id, lower(name));
ALTER TABLE projects ENABLE ROW LEVEL SECURITY;

ALTER TABLE conversations ADD COLUMN active_project_id bigint REFERENCES projects(id);
CREATE INDEX conversations_active_project_idx ON conversations (active_project_id);
ALTER TABLE conversations DROP CONSTRAINT conversations_state_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_state_check
  CHECK (state IN ('idle', 'onboarding', 'collecting', 'awaiting_proposal', 'awaiting_approval', 'needs_human'));

ALTER TABLE campaign_drafts ADD COLUMN project_id bigint REFERENCES projects(id);
CREATE INDEX campaign_drafts_project_idx ON campaign_drafts (project_id);

-- Conversão: cada organização com conversa em andamento ganha o projeto "Campanha 1".
INSERT INTO projects (organization_id, name, stage, fields)
SELECT DISTINCT ON (m.organization_id) m.organization_id, 'Campanha 1',
  CASE WHEN c.state IN ('collecting', 'awaiting_proposal', 'awaiting_approval') THEN c.state ELSE 'collecting' END,
  c.fields
FROM conversations c
JOIN whatsapp_identities w ON w.id = c.identity_id
JOIN members m ON m.id = w.member_id
WHERE c.fields <> '{}'::jsonb
ORDER BY m.organization_id, c.updated_at DESC;

UPDATE conversations c SET active_project_id = p.id
FROM whatsapp_identities w, members m, projects p
WHERE w.id = c.identity_id AND m.id = w.member_id
  AND p.organization_id = m.organization_id AND lower(p.name) = 'campanha 1'
  AND c.fields <> '{}'::jsonb;

UPDATE campaign_drafts d SET project_id = c.active_project_id
FROM conversations c WHERE c.id = d.conversation_id AND c.active_project_id IS NOT NULL;
