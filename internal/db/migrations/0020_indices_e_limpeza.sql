-- Índices das consultas do relatório diário e da fila, e apoio à limpeza de eventos antigos.
CREATE INDEX IF NOT EXISTS messages_inbound_created_idx ON messages (created_at) WHERE direction = 'in';
CREATE INDEX IF NOT EXISTS whatsapp_identities_created_idx ON whatsapp_identities (created_at);
CREATE INDEX IF NOT EXISTS campaign_drafts_org_idx ON campaign_drafts (organization_id);
CREATE INDEX IF NOT EXISTS campaign_drafts_created_idx ON campaign_drafts (created_at);
CREATE INDEX IF NOT EXISTS llm_usage_created_idx ON llm_usage (created_at);
CREATE INDEX IF NOT EXISTS outbox_pending_conversation_idx ON outbox_jobs (kind, ((payload->>'conversation_id'))) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS inbox_processed_idx ON inbox_events (processed_at) WHERE status <> 'pending';
CREATE INDEX IF NOT EXISTS outbox_finished_idx ON outbox_jobs (updated_at) WHERE status <> 'pending';