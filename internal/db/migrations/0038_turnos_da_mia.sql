-- O que a Mia entendeu de cada mensagem do cliente e o que o sistema fez (painel: "por que respondeu assim").
-- Guardado 30 dias. Idempotente.
CREATE TABLE IF NOT EXISTS mia_turns (
  id bigserial PRIMARY KEY,
  conversation_id bigint NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  message_id bigint,
  intent text NOT NULL DEFAULT '',
  project text NOT NULL DEFAULT '',
  fields jsonb,
  profile jsonb,
  reply text NOT NULL DEFAULT '',
  action text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS mia_turns_conv_idx ON mia_turns (conversation_id, message_id);
CREATE INDEX IF NOT EXISTS mia_turns_created_idx ON mia_turns (created_at);
ALTER TABLE mia_turns ENABLE ROW LEVEL SECURITY;
