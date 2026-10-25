-- Post que o cliente já publicou vira anúncio como está, sem mandar o link: a Mia lista os posts mais
-- recentes do Instagram e da Página (post_choices do rascunho, na ordem mostrada) e o cliente escolhe pelo
-- número. Post da Página entra com origem facebook_own e o id do post (object_story_id do anúncio).
-- Idempotente: colunas com IF NOT EXISTS e a trava refeita com a origem nova.
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS fb_post_id text NOT NULL DEFAULT '';
ALTER TABLE campaign_drafts ADD COLUMN IF NOT EXISTS post_choices jsonb NOT NULL DEFAULT '{}';

ALTER TABLE creatives DROP CONSTRAINT IF EXISTS creatives_origin_check;
ALTER TABLE creatives ADD CONSTRAINT creatives_origin_check
  CHECK (origin IN ('upload', 'instagram_own', 'instagram_public', 'facebook_own'));
