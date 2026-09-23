CREATE TABLE organizations (
  id bigserial PRIMARY KEY,
  name text NOT NULL,
  niche text NOT NULL DEFAULT '',
  max_daily_budget_brl numeric(12,2) NOT NULL CHECK (max_daily_budget_brl > 0),
  allowed_objectives text[] NOT NULL DEFAULT ARRAY['OUTCOME_ENGAGEMENT']
    CHECK (cardinality(allowed_objectives) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL,
  role text NOT NULL CHECK (role IN ('owner', 'approver', 'viewer')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX members_org_idx ON members (organization_id);

CREATE TABLE whatsapp_identities (
  id bigserial PRIMARY KEY,
  provider text NOT NULL,
  phone_e164 text NOT NULL,
  member_id bigint NOT NULL REFERENCES members(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, phone_e164)
);
CREATE INDEX whatsapp_identities_member_idx ON whatsapp_identities (member_id);

CREATE TABLE conversations (
  id bigserial PRIMARY KEY,
  identity_id bigint NOT NULL UNIQUE REFERENCES whatsapp_identities(id),
  chat_id text NOT NULL DEFAULT '',
  state text NOT NULL DEFAULT 'idle'
    CHECK (state IN ('idle', 'collecting', 'awaiting_proposal', 'awaiting_approval', 'needs_human')),
  fields jsonb NOT NULL DEFAULT '{}',
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE messages (
  id bigserial PRIMARY KEY,
  conversation_id bigint NOT NULL REFERENCES conversations(id),
  direction text NOT NULL CHECK (direction IN ('in', 'out')),
  kind text NOT NULL,
  body text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX messages_conversation_idx ON messages (conversation_id, id);

CREATE TABLE inbox_events (
  id bigserial PRIMARY KEY,
  provider text NOT NULL,
  external_id text NOT NULL,
  payload jsonb NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
  attempts int NOT NULL DEFAULT 0,
  locked_until timestamptz,
  last_error text,
  received_at timestamptz NOT NULL DEFAULT now(),
  processed_at timestamptz,
  UNIQUE (provider, external_id)
);
CREATE INDEX inbox_pending_idx ON inbox_events (id) WHERE status = 'pending';

CREATE TABLE outbox_jobs (
  id bigserial PRIMARY KEY,
  kind text NOT NULL,
  payload jsonb NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
  attempts int NOT NULL DEFAULT 0,
  run_after timestamptz NOT NULL DEFAULT now(),
  locked_until timestamptz,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_pending_idx ON outbox_jobs (run_after) WHERE status = 'pending';

CREATE TABLE campaign_drafts (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  conversation_id bigint NOT NULL REFERENCES conversations(id),
  briefing jsonb NOT NULL,
  proposal jsonb NOT NULL,
  proposal_hash text NOT NULL,
  status text NOT NULL DEFAULT 'proposed'
    CHECK (status IN ('proposed', 'approved', 'rejected', 'expired', 'cancelled')),
  approved_by bigint REFERENCES members(id),
  approved_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (status <> 'approved' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL))
);
CREATE INDEX campaign_drafts_conversation_idx ON campaign_drafts (conversation_id);

CREATE TABLE approval_codes (
  id bigserial PRIMARY KEY,
  draft_id bigint NOT NULL REFERENCES campaign_drafts(id),
  code_hash text NOT NULL,
  proposal_hash text NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  attempts int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_codes_draft_idx ON approval_codes (draft_id);

CREATE TABLE llm_usage (
  id bigserial PRIMARY KEY,
  organization_id bigint REFERENCES organizations(id),
  agent text NOT NULL CHECK (agent IN ('attendant', 'manager', 'transcriber')),
  model text NOT NULL,
  input_tokens int NOT NULL,
  output_tokens int NOT NULL,
  cost_usd numeric(12,6) NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_usage_org_idx ON llm_usage (organization_id, created_at);

CREATE TABLE audit_events (
  id bigserial PRIMARY KEY,
  organization_id bigint REFERENCES organizations(id),
  actor text NOT NULL,
  action text NOT NULL,
  data jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_org_idx ON audit_events (organization_id, created_at);
