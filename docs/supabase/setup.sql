-- Configuração inicial do banco no Supabase (rodar no SQL Editor do projeto).
--
-- O que faz:
--   1. cria o usuário do app (gestor_app) com senha aleatória, sem superusuário e sem BYPASSRLS
--      (se ele já existir, só gera uma senha nova);
--   2. cria o schema privado "app", fora do public, dono = gestor_app, sem acesso para anon/authenticated;
--   3. fixa search_path e limites de tempo no próprio usuário (funciona através do pooler);
--   4. mostra a senha gerada UMA vez, para colar na DATABASE_URL do Easypanel.
--
-- Pode ser rodado de novo sem estragar nada. As tabelas são criadas depois pelo próprio app
-- (migrations), com dono gestor_app e RLS ligado; tabela nova não tem acesso para mais ninguém.

DO $$
DECLARE
  pw text := encode(extensions.gen_random_bytes(24), 'hex');
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gestor_app') THEN
    -- Já existe: só troca a senha (atributos não mudam; ALTER de SUPERUSER/BYPASSRLS exige superusuário).
    EXECUTE format('ALTER ROLE gestor_app WITH PASSWORD %L', pw);
  ELSE
    EXECUTE format('CREATE ROLE gestor_app LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION CONNECTION LIMIT 20', pw);
  END IF;
  CREATE TEMP TABLE IF NOT EXISTS _gestor_app_senha (senha_do_app text);
  TRUNCATE _gestor_app_senha;
  INSERT INTO _gestor_app_senha VALUES (pw);
END $$;

-- Schema privado. O postgres vira membro por um instante só para dar a posse e fechar o acesso.
GRANT gestor_app TO postgres;
CREATE SCHEMA IF NOT EXISTS app AUTHORIZATION gestor_app;
REVOKE ALL ON SCHEMA app FROM PUBLIC;
REVOKE ALL ON SCHEMA app FROM anon, authenticated;
REVOKE gestor_app FROM postgres;

-- O app só enxerga o próprio schema e não cria nada no public.
REVOKE ALL ON SCHEMA public FROM gestor_app;
ALTER ROLE gestor_app SET search_path = app;
ALTER ROLE gestor_app SET statement_timeout = '60s';
ALTER ROLE gestor_app SET idle_in_transaction_session_timeout = '60s';

-- Senha gerada (aparece só agora).
SELECT senha_do_app FROM _gestor_app_senha;
