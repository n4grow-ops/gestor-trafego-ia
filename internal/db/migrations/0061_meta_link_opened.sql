-- Link de conexão Meta aberto sem conexão (11/11): enquanto o app está em desenvolvimento na Meta, quem
-- não é testador abre o link, o Facebook mostra "app indisponível" e nada volta para nós. opened_at marca
-- a primeira abertura; followup_at marca que a Mia já pediu o link do perfil (uma vez por link).
-- Idempotente.
ALTER TABLE meta_connect_links ADD COLUMN IF NOT EXISTS opened_at timestamptz;
ALTER TABLE meta_connect_links ADD COLUMN IF NOT EXISTS followup_at timestamptz;
