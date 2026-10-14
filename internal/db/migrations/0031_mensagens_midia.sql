-- Referência da mídia (foto ou vídeo) que o cliente mandou: o id da mensagem no provedor do WhatsApp.
-- O painel baixa a mídia por ela sob demanda. Nulo em texto e em mensagens antigas. Idempotente.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_ref text;
