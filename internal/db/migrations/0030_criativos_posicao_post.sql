-- Posição do item dentro do post do Instagram (0 = primeira mídia do carrossel). Com o link (ou o id do
-- post na Graph API), deixa a nova tentativa do job pular o que já foi guardado em vez de duplicar.
-- Upload pelo WhatsApp fica nulo. Idempotente.
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS source_position int;
CREATE INDEX IF NOT EXISTS creatives_draft_source_idx ON creatives (draft_id, source_position) WHERE source_position IS NOT NULL;
