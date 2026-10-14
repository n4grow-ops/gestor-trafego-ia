-- Anúncio reprovado pela Meta: a publicação fica 'rejected' (com o motivo em error) e pode ser refeita com
-- anúncios novos do mesmo rascunho aprovado. Idempotente.
ALTER TABLE meta_publications DROP CONSTRAINT IF EXISTS meta_publications_status_check;
ALTER TABLE meta_publications ADD CONSTRAINT meta_publications_status_check
  CHECK (status IN ('publishing', 'paused', 'active', 'failed', 'rejected'));
