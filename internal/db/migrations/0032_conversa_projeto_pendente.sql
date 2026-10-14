-- Pedido de projeto novo esperando o cliente escolher qual projeto arquivar (plano no limite).
-- Guarda o nome, os dados já informados e a lista mostrada ao cliente. Nulo = nada pendente. Idempotente.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS pending_project jsonb;
