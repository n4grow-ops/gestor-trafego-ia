-- Pedido do usuário (02/11): plano secreto para testar um pagamento real no Asaas com o valor mínimo (R$ 5).
-- Oculto (visible = false): não aparece na lista; vale só quem digitar o código exato no WhatsApp.
-- Limites iguais aos do Básico. Para tirar depois: UPDATE plans SET available = false WHERE code = 'teste5n4g'.
INSERT INTO plans (code, name, price_cents, currency, billing_interval, daily_message_limit, max_daily_budget_brl,
		max_projects, max_campaigns_month, available, sort_order, visible)
VALUES ('teste5n4g', 'Teste', 500, 'BRL', 'month', 60, 250.00, 3, 4, true, 99, false)
ON CONFLICT (code) DO NOTHING;
