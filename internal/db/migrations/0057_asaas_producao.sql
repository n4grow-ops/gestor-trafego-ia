-- Troca do Asaas de sandbox para produção (02/11): clientes, assinaturas e cobranças criados no sandbox não
-- existem na produção. Limpa as referências para a próxima escolha de forma gerar tudo de novo na produção.
-- Só mexe no que não foi pago (nenhum pagamento pago existe). Idempotente.
UPDATE organizations SET asaas_customer_id = NULL WHERE asaas_customer_id IS NOT NULL;
UPDATE subscriptions SET provider_ref = NULL, checkout_url = NULL, pending_method = NULL, updated_at = now()
WHERE provider = 'asaas' AND status = 'pending';
DELETE FROM payments p WHERE p.provider = 'asaas' AND p.status = 'pending'
  AND NOT EXISTS (SELECT 1 FROM payments x WHERE x.subscription_id = p.subscription_id AND x.status = 'paid');
