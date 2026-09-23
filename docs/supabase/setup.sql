-- Configuração inicial do banco no Supabase (rodar UMA vez no SQL Editor do projeto).
--
-- O que faz:
--   1. cria o usuário do app (gestor_app) com senha aleatória, sem superusuário e sem BYPASSRLS;
--   2. cria o schema privado "app", fora do public, dono = gestor_app, sem acesso para anon/authenticated;
--   3. fixa search_path e limites de tempo no próprio usuário (funciona através do pooler);
--   4. mostra a senha gerada UMA vez, para colar na DATABASE_URL do Easypanel.
--
-- As tabelas são criadas depois pelo próprio app (migrations), já com RLS ligado.
-- A senha não aparece em nenhum outro lugar: copie o resultado e guarde só no Easypanel.

DO $$
DECLARE
  pw text := encode(extensions.gen_random_bytes(24), 'hex');
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'gestor_app') THEN
    RAISE EXCEPTION 'o usuário gestor_app já existe; este script só roda uma vez';
  END IF;
  EXECUTE format(
    'CREATE ROLE gestor_app LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION CONNECTION LIMIT 20',
    pw);
  CREATE TEMP TABLE _gestor_app_senha AS SELECT pw AS senha_do_app;
END $$;

-- Schema privado. O postgres precisa ser membro por um instante para dar a posse.
GRANT gestor_app TO postgres;
CREATE SCHEMA app AUTHORIZATION gestor_app;
REVOKE gestor_app FROM postgres;

REVOKE ALL ON SCHEMA app FROM PUBLIC;
REVOKE ALL ON SCHEMA app FROM anon, authenticated;
ALTER DEFAULT PRIVILEGES FOR ROLE gestor_app IN SCHEMA app REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE gestor_app IN SCHEMA app REVOKE ALL ON SEQUENCES FROM PUBLIC;

-- O app só enxerga o próprio schema e não cria nada no public.
REVOKE ALL ON SCHEMA public FROM gestor_app;
ALTER ROLE gestor_app SET search_path = app;
ALTER ROLE gestor_app SET statement_timeout = '60s';
ALTER ROLE gestor_app SET idle_in_transaction_session_timeout = '60s';

-- Senha gerada (aparece só agora).
SELECT senha_do_app FROM _gestor_app_senha;
