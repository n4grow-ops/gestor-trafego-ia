-- Pedido do usuário (31/10): a conta da N4Grow (org 17) fica livre da cobrança para os testes, igual à
-- org 13. Ilimitada passa pelo portão do plano e não tem limite de campanhas nem de projetos. Idempotente.
UPDATE organizations SET unlimited = true WHERE id = 17 AND NOT unlimited;
UPDATE subscriptions SET status = 'active', updated_at = now() WHERE organization_id = 17 AND status = 'pending';
