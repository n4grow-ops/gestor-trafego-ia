-- Número do WhatsApp vinculado à Página escolhida na conexão. Vai no promoted_object dos anúncios de
-- clique para o WhatsApp; vazio (conexões antigas ou sem permissão) = o número fica de fora.
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS page_whatsapp_number text NOT NULL DEFAULT '';
