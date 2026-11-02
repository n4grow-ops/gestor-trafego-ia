-- Correção (02/11): trocar a forma de pagamento apagava a assinatura antiga no Asaas antes de criar a nova.
-- Quando a nova falhava (Pix sem CPF/CNPJ), o webhook SUBSCRIPTION_DELETED cancelava a assinatura e o cliente
-- ficava preso com um link morto. Volta para pending, sem cobrança, para escolher a forma de novo.
-- Só mexe em cancelada sem nenhum pagamento pago e sem período pago. Idempotente.
UPDATE subscriptions s
SET status = 'pending', provider_ref = NULL, checkout_url = NULL, pending_method = NULL, updated_at = now()
WHERE s.status = 'canceled'
  AND s.provider = 'asaas'
  AND s.current_period_end IS NULL
  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.subscription_id = s.id AND p.status = 'paid');
