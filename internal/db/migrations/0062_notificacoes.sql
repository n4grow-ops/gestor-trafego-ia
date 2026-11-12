-- Notificações (12/11): avisos para apps de automação por webhook (Pushcut no iPhone, MacroDroid no
-- Android ou um webhook qualquer) quando entra venda, cliente novo, cadastro ou mensagem.
-- notify_destinations: um destino por linha. A URL é segredo (o painel só mostra o final) e só aceita
-- https; a checagem de rede (SSRF) fica no Go, na hora de salvar e de novo na hora de conectar.
-- notify_deliveries: cada tentativa de entrega, sem os dados do evento; o worker guarda as últimas
-- 100 por destino. notify_sale_token: o token do POST /api/hooks/venda, só o hash (linha única).
-- Idempotente.
CREATE TABLE IF NOT EXISTS notify_destinations (
  id bigserial PRIMARY KEY,
  name text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  kind text NOT NULL CHECK (kind IN ('pushcut', 'macrodroid', 'webhook')),
  url text NOT NULL CHECK (url LIKE 'https://%' AND length(url) <= 2000),
  events text[] NOT NULL DEFAULT '{}',
  active boolean NOT NULL DEFAULT true,
  title_tpl text NOT NULL DEFAULT '' CHECK (length(title_tpl) <= 200),
  text_tpl text NOT NULL DEFAULT '' CHECK (length(text_tpl) <= 1000),
  created_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notify_destinations_events_idx ON notify_destinations USING gin (events) WHERE active;
ALTER TABLE notify_destinations ENABLE ROW LEVEL SECURITY;

CREATE TABLE IF NOT EXISTS notify_deliveries (
  id bigserial PRIMARY KEY,
  destination_id bigint NOT NULL REFERENCES notify_destinations (id) ON DELETE CASCADE,
  event text NOT NULL,
  attempt int NOT NULL DEFAULT 1,
  ok boolean NOT NULL,
  status_code int NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT '' CHECK (length(error) <= 300),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notify_deliveries_dest_idx ON notify_deliveries (destination_id, id DESC);
ALTER TABLE notify_deliveries ENABLE ROW LEVEL SECURITY;

CREATE TABLE IF NOT EXISTS notify_sale_token (
  id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
  last4 text NOT NULL CHECK (length(last4) = 4),
  created_by text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  used_at timestamptz
);
ALTER TABLE notify_sale_token ENABLE ROW LEVEL SECURITY;
