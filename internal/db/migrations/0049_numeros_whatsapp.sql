-- Todos os números de WhatsApp achados na conexão (o da Página e os das contas de WhatsApp Business
-- do negócio). A Mia mostra estes ao cliente e nunca inventa um. Idempotente.
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS whatsapp_numbers text[] NOT NULL DEFAULT '{}';
