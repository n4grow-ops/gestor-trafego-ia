-- VSL da página inicial (06/11): uma linha por abertura da página (sessão), para o admin ver quantos
-- deram play, até onde assistiram e quantos clicaram para começar no WhatsApp.
-- Sem dado pessoal: nada de IP, nome ou telefone. visitor_id é um aleatório guardado no navegador
-- (conta pessoas únicas), session_id é um aleatório por abertura da página.
-- watched_mask: 20 faixas de 5% do vídeo; o bit i acende quando a pessoa assistiu aquela faixa
-- (pular pra frente não acende as faixas puladas). max_s: o ponto mais longe a que chegou.
-- watched_s: segundos de vídeo que de fato passaram na tela (pulos não contam).
-- Idempotente.
CREATE TABLE IF NOT EXISTS vsl_views (
  id bigserial PRIMARY KEY,
  session_id uuid NOT NULL UNIQUE,
  visitor_id uuid NOT NULL,
  video_id text NOT NULL CHECK (video_id ~ '^[A-Za-z0-9_-]{6,20}$'),
  device text NOT NULL DEFAULT '' CHECK (device IN ('', 'mobile', 'tablet', 'desktop')),
  source text NOT NULL DEFAULT '' CHECK (length(source) <= 40),
  played_at timestamptz,
  duration_s int NOT NULL DEFAULT 0 CHECK (duration_s BETWEEN 0 AND 36000),
  max_s int NOT NULL DEFAULT 0 CHECK (max_s BETWEEN 0 AND 36000),
  watched_mask int NOT NULL DEFAULT 0 CHECK (watched_mask BETWEEN 0 AND 1048575),
  watched_s int NOT NULL DEFAULT 0 CHECK (watched_s BETWEEN 0 AND 36000),
  cta_at timestamptz,
  started_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS vsl_views_started_idx ON vsl_views (started_at DESC);
ALTER TABLE vsl_views ENABLE ROW LEVEL SECURITY;
