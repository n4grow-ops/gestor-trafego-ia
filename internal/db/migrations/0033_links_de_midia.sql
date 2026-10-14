-- Arquivos guardados no Storage privado com link assinado renovado: mídia da conversa, anúncios e prévias.
-- O worker (que tem a chave) sobe e assina; a api só lê a url e repassa ao painel. Idempotente.
CREATE TABLE IF NOT EXISTS media_links (
  id bigserial PRIMARY KEY,
  kind text NOT NULL CHECK (kind IN ('message', 'creative', 'preview')),
  ref_id bigint NOT NULL,
  bucket text NOT NULL,
  path text NOT NULL,
  mime text NOT NULL DEFAULT '',
  url text NOT NULL,
  url_until timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (bucket, path)
);
CREATE INDEX IF NOT EXISTS media_links_ref_idx ON media_links (kind, ref_id);
CREATE INDEX IF NOT EXISTS media_links_until_idx ON media_links (url_until);
ALTER TABLE media_links ENABLE ROW LEVEL SECURITY;
