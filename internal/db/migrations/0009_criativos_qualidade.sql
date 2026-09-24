-- Qualidade e formato de cada anúncio: define em que posicionamento ele entra (feed ou Stories/Reels).
ALTER TABLE creatives
  ADD COLUMN width integer CHECK (width IS NULL OR width > 0),
  ADD COLUMN height integer CHECK (height IS NULL OR height > 0),
  ADD COLUMN duration_ms integer CHECK (duration_ms IS NULL OR duration_ms >= 0),
  ADD COLUMN format text NOT NULL DEFAULT 'other'
    CHECK (format IN ('square', 'portrait', 'vertical', 'landscape', 'other')),
  ADD COLUMN placement text NOT NULL DEFAULT 'feed' CHECK (placement IN ('feed', 'stories'));

-- Na etapa dos anúncios, o aviso de posicionamento faltando é dado uma vez só por rascunho.
ALTER TABLE campaign_drafts ADD COLUMN creatives_nudged boolean NOT NULL DEFAULT false;
