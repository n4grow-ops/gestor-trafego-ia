# Criativos, lembrete de cadastro e relatório diário — design

Data: 2026-09-24. Branch: `feat/cadastro`.

Pedido do usuário: (1) depois que o cliente aprova a proposta definitiva, pedir os anúncios em imagem ou vídeo;
(2) lembrar quem não concluiu o cadastro; (3) relatório diário no WhatsApp do operador.

## 1. Criativos depois da aprovação

- `SIM <código>` válido: rascunho aprovado (como hoje) e a conversa vai para o estado novo `awaiting_creatives`.
  Mensagem: "Campanha aprovada! Agora me manda as imagens ou vídeos do anúncio (até 5)… Quando terminar, escreve
  PRONTO."
- Em `awaiting_creatives`:
  - imagem ou vídeo: o worker baixa do WAHA, sobe para o Supabase Storage (bucket privado `creatives`, caminho
    `org/<org>/draft/<draft>/<id>.<ext>`) e grava em `creatives` (org, draft, projeto, tipo, caminho, mime, tamanho).
    Resposta (uma por lote, com o debounce): "Recebi! Até agora: N arquivo(s)…";
  - "pronto" (ou "é isso", "só isso", "terminei"): com ≥ 1 criativo, conversa volta a `idle` e a mensagem final diz que
    a campanha fica pronta para publicar assim que a conexão com a Meta estiver ativa; com 0, pede pelo menos um;
  - 5 criativos: conclui sozinho;
  - outro texto: lembra o que está esperando;
  - falha ao baixar/subir: "não consegui salvar esse arquivo, manda de novo?".
- Tipos aceitos: imagem (`image/*`) e vídeo (`video/*`, novo `whatsapp.KindVideo`), até 32 MB.
- Migration 0008: tabela `creatives` (RLS), estado `awaiting_creatives` no CHECK de `conversations`.
- Config do worker: `SUPABASE_URL` (já existe) e `SUPABASE_SERVICE_ROLE_KEY` (nova; só no worker). Sem a chave, a
  aprovação volta ao comportamento de hoje (sem pedir criativos). O bucket é criado no início do worker se não existir.

## 2. Lembrete de cadastro abandonado

- Ao enviar o primeiro link de uma identidade, agenda o job `signup.remind` (tentativa 1) para daqui a 3 h.
- O job: se o cadastro já foi concluído, a organização está bloqueada ou a conversa está com humano, não faz nada.
  Senão gera um link novo (o antigo continua valendo) e manda: "Oi! Faltou pouquinho pro seu cadastro… <link>".
  Tentativa 1 agenda a 2 para 21 h depois (24 h do primeiro link). Máximo 2 lembretes.
- Horário: nunca entre 21 h e 8 h (horário de Brasília); cai para as 9 h do dia seguinte.
- Não duplica: só agenda se não houver lembrete pendente para a identidade.

## 3. Relatório diário

- Todo dia às 8 h (Brasília), para `REPORT_PHONE` (padrão: `OPERATOR_PHONE`). Sem número, não roda.
- Últimas 24 h: números novos, cadastros concluídos, cadastros pendentes com link enviado, mensagens recebidas,
  propostas geradas, propostas aprovadas, criativos recebidos, custo de IA (US$).
- Agendamento pelo próprio outbox: ao iniciar, o worker garante um job `report.daily` pendente; ao rodar, agenda o
  próximo.

## Testes

Store (criativos, contagens do relatório, lembrete pendente), engine (fluxo de criativos com fake de storage,
pronto/limite/erro, lembretes com relógio, horário silencioso, relatório), storage (httptest), waha (vídeo).
