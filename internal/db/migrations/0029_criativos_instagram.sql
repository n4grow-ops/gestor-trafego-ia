-- Anúncios vindos de post do Instagram: origem (upload pelo WhatsApp, post da conta do cliente ou post
-- público), id do post na Graph API, link e legenda do post. Post público só vai para a Meta depois que o
-- cliente confirma que o conteúdo é dele ou que tem autorização (rights_confirmed); uploads já valem.
-- Idempotente: colunas com IF NOT EXISTS e a trava só é criada se ainda não existir.
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'upload';
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS ig_media_id text NOT NULL DEFAULT '';
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS rights_confirmed boolean NOT NULL DEFAULT true;
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS source_url text NOT NULL DEFAULT '';
ALTER TABLE creatives ADD COLUMN IF NOT EXISTS source_caption text NOT NULL DEFAULT '';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'creatives_origin_check'
                 AND conrelid = 'creatives'::regclass) THEN
    ALTER TABLE creatives ADD CONSTRAINT creatives_origin_check
      CHECK (origin IN ('upload', 'instagram_own', 'instagram_public'));
  END IF;
END $$;
