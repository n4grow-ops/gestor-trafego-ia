-- Pedido do usuário (02/11): a conta do Jonathan (org 13) deixa de ser ilimitada e passa pela cobrança,
-- para receber a mensagem de pagamento com os planos. Assinatura ativa sem pagamento pago vira pending.
-- Idempotente.
UPDATE organizations SET unlimited = false WHERE id = 13 AND unlimited;
UPDATE subscriptions s SET status = 'pending', updated_at = now()
WHERE s.organization_id = 13
  AND s.status IN ('active', 'trialing')
  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.subscription_id = s.id AND p.status = 'paid');
