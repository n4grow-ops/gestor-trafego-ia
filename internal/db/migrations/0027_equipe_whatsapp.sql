-- WhatsApp de cada pessoa da equipe e se ela recebe os avisos (atendimento humano, testador,
-- publicação e alertas). Os números passam a ser definidos no painel, não mais só pelo TEAM_PHONES.
-- Idempotente: colunas com IF NOT EXISTS e a trava só é criada se ainda não existir.
ALTER TABLE team_members ADD COLUMN IF NOT EXISTS whatsapp text NOT NULL DEFAULT '';
ALTER TABLE team_members ADD COLUMN IF NOT EXISTS receives_alerts boolean NOT NULL DEFAULT false;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_members_whatsapp_check'
                 AND conrelid = 'team_members'::regclass) THEN
    -- Só dígitos (10 a 15) ou vazio; quem recebe aviso precisa ter número.
    ALTER TABLE team_members ADD CONSTRAINT team_members_whatsapp_check
      CHECK ((whatsapp = '' OR whatsapp ~ '^[0-9]{10,15}$') AND (NOT receives_alerts OR whatsapp <> ''));
  END IF;
END $$;
