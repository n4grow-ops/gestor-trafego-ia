-- Configuração da Mia editada no painel (aba Mia): um documento por versão, nunca alterado.
-- A versão em vigor é a maior; "desfazer" e "restaurar padrão" gravam uma versão nova.
-- Textos, campos, persona, segmentos, funil e voz ficam no mesmo documento para o histórico
-- e o desfazer valerem para tudo de uma vez.
CREATE TABLE mia_settings (
  version int PRIMARY KEY CHECK (version > 0),
  config jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object'),
  note text CHECK (note IS NULL OR char_length(note) <= 200),
  created_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE mia_settings ENABLE ROW LEVEL SECURITY;
