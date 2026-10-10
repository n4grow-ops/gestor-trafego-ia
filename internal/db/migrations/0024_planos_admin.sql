-- Aba Planos do painel: a equipe edita os planos sem mexer no banco.
-- visible = false tira o plano do cadastro (quem já assina continua com ele);
-- updated_at serve de trava contra duas pessoas salvando o mesmo plano ao mesmo tempo.
-- Só colunas novas com valor padrão: idempotente e sem reescrever nada que já existe.
ALTER TABLE plans ADD COLUMN IF NOT EXISTS visible boolean NOT NULL DEFAULT true;
ALTER TABLE plans ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE plans ENABLE ROW LEVEL SECURITY;
