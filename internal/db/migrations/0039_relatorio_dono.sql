-- Preferência do relatório falado do dono, por organização: texto sempre; áudio só depois que o cliente aceita.
-- offered_at marca que a Mia já fez o convite (uma vez só). Idempotente.
CREATE TABLE IF NOT EXISTS report_prefs (
  organization_id bigint PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  audio boolean NOT NULL DEFAULT false,
  offered_at timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE report_prefs ENABLE ROW LEVEL SECURITY;
