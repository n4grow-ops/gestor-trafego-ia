-- Testador automático, correções da revisão. Só colunas e índice novos, tudo com IF NOT EXISTS:
-- idempotente, sem mexer em dado existente nem nas políticas de RLS.

-- notified_at: último aviso de pedido novo à equipe (no máximo um a cada 30 min por pedido).
ALTER TABLE tester_requests ADD COLUMN IF NOT EXISTS notified_at timestamptz;
-- accept_notified_at: "Vi que você aceitou" já mandado (uma vez só por pedido; a marca é a trava).
ALTER TABLE tester_requests ADD COLUMN IF NOT EXISTS accept_notified_at timestamptz;

-- Janela de 24 h medida pelo chat do destino (avisos aos números da equipe).
CREATE INDEX IF NOT EXISTS conversations_chat_idx ON conversations (chat_id) WHERE chat_id <> '';
