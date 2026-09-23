# Gestor de Tráfego IA

SaaS que transforma conversas no WhatsApp em campanhas de Meta Ads, com dois agentes de IA:
**Atendente** (fala com o cliente) e **Gestor** (especialista em tráfego, nos bastidores).

- Backend: Go (`cmd/api`, `cmd/worker`, `cmd/admin`)
- Banco: PostgreSQL
- WhatsApp: open-wa (etapa 1) → Cloud API oficial (produção)
- LLM: OpenRouter
- Deploy: Easypanel (VPS Hostinger)

Spec da etapa 1: `docs/superpowers/specs/2026-09-22-duas-ias-whatsapp-easypanel-design.md`
