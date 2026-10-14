-- Cliente conduz: todos os objetivos que o sistema publica ficam liberados por padrão
-- (conversas, contatos por formulário e visitas). O admin ainda pode restringir uma organização.
ALTER TABLE organizations
  ALTER COLUMN allowed_objectives SET DEFAULT ARRAY['OUTCOME_ENGAGEMENT','OUTCOME_LEADS','OUTCOME_TRAFFIC'];

UPDATE organizations
   SET allowed_objectives = ARRAY(SELECT DISTINCT unnest(allowed_objectives || ARRAY['OUTCOME_LEADS','OUTCOME_TRAFFIC']))
 WHERE NOT (allowed_objectives @> ARRAY['OUTCOME_LEADS','OUTCOME_TRAFFIC']);
