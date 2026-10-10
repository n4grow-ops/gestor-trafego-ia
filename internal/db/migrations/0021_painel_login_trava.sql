-- Trava do login do painel no banco (vale com mais de uma réplica e sobrevive a reinício):
-- falhas por e-mail, bloqueio de 15 min, teto diário de falhas e teto de códigos enviados por hora.

-- Estado atual de cada e-mail: falhas desde o último código, se o código atual ainda vale e até
-- quando está bloqueado. Com 5 falhas o código deixa de valer (precisa de um novo).
CREATE TABLE admin_login_state (
  email text PRIMARY KEY CHECK (email = lower(btrim(email)) AND char_length(email) <= 254),
  failures int NOT NULL DEFAULT 0 CHECK (failures >= 0),
  code_valid boolean NOT NULL DEFAULT false,
  locked_until timestamptz,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE admin_login_state ENABLE ROW LEVEL SECURITY;

-- Histórico curto (limpo depois de 2 dias): pedidos de código (code_request = e-mail fora da
-- equipe, nada enviado; code_sent = código enviado) e falhas de login.
CREATE TABLE admin_login_events (
  id bigserial PRIMARY KEY,
  email text NOT NULL CHECK (char_length(email) <= 254),
  kind text NOT NULL CHECK (kind IN ('code_request', 'code_sent', 'failure')),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS admin_login_events_email_idx ON admin_login_events (email, kind, created_at);
CREATE INDEX IF NOT EXISTS admin_login_events_sent_idx ON admin_login_events (created_at) WHERE kind = 'code_sent';
CREATE INDEX IF NOT EXISTS admin_login_events_created_idx ON admin_login_events (created_at);
ALTER TABLE admin_login_events ENABLE ROW LEVEL SECURITY;
