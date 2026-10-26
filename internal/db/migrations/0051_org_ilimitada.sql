-- Liberação por cliente (26/10): organização com unlimited = true não tem limite de campanhas por mês nem
-- de projetos ativos, seja qual for o plano. Só o Jonathan Ventura (org 13, "Método v3"). Idempotente.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS unlimited boolean NOT NULL DEFAULT false;

UPDATE organizations SET unlimited = true WHERE id = 13 AND NOT unlimited;
