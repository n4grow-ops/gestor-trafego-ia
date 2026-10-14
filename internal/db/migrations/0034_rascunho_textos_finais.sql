-- Textos que vão no ar, decididos antes da prévia (proposta reescrita com as referências do cliente):
-- prévia, publicação e "os textos que vão no ar" usam a mesma lista. Nulo = ainda não decididos. Idempotente.
ALTER TABLE campaign_drafts ADD COLUMN IF NOT EXISTS final_copies jsonb;
