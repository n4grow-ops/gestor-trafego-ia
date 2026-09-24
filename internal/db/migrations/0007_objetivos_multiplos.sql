-- Cadastro: o cliente pode escolher mais de um objetivo para os anúncios.
ALTER TABLE organizations ALTER COLUMN ads_objective TYPE text[]
  USING CASE WHEN ads_objective IS NULL THEN NULL ELSE ARRAY[ads_objective] END;
