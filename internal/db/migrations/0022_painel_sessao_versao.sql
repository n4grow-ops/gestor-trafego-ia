-- Versão da sessão do painel: vai no cookie e é conferida a cada requisição. O logout sobe a versão
-- e derruba todas as sessões daquela pessoa (inclusive um cookie copiado).
ALTER TABLE team_members ADD COLUMN session_version int NOT NULL DEFAULT 1 CHECK (session_version > 0);
ALTER TABLE team_members ENABLE ROW LEVEL SECURITY;
