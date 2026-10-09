-- Último estado da conexão do número do bot, avisado pelo provedor (webhook "connection").
CREATE TABLE whatsapp_status (
  provider text PRIMARY KEY,
  connected boolean NOT NULL,
  code int,
  reason text,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE whatsapp_status ENABLE ROW LEVEL SECURITY;
