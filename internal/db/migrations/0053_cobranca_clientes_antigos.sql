-- Pedido do usuário (31/10): não existe plano grátis. Quem entrou antes da cobrança com o plano ativado de
-- graça (sem provedor de pagamento) também passa pelo portão do plano e paga para usar. Org ilimitada fica
-- fora. Idempotente: só mexe em assinatura ativa sem provedor.
UPDATE subscriptions s
SET status = 'pending', updated_at = now()
FROM organizations o
WHERE o.id = s.organization_id
  AND NOT o.unlimited
  AND s.status IN ('active', 'trialing')
  AND coalesce(s.provider, '') IN ('', 'manual')
  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.subscription_id = s.id AND p.status = 'paid');
