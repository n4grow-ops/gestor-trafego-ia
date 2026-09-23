-- RLS ligado em todas as tabelas, sem políticas: papéis que não são donos (anon e authenticated
-- do Supabase) não leem nem gravam nada, mesmo que o schema seja exposto por engano.
-- O dono das tabelas (o usuário do app) não é afetado, porque não usamos FORCE.
ALTER TABLE schema_migrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE members ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE inbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaign_drafts ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE llm_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;
