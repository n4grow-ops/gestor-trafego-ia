# Atendente mais humana — design

Data: 2026-09-23. Branch: `feat/atendente-humana` (a partir de `feat/etapa-2`).

## Objetivo

O cliente deve sentir que conversa com uma pessoa no WhatsApp: vê o visto azul, vê "digitando…" ou
"gravando áudio…", recebe respostas curtas em balões separados, recebe áudio quando mandou áudio e não
leva três respostas quando manda três mensagens seguidas.

Pedido do usuário: "responda mais humana com naturalidade; se escrever, aparecer o ícone de escrita; se
for áudio, aparecer gravando áudio; e o que mais deixar ela humana". Aprovado: tudo como recomendado,
incluindo áudio responde com áudio (opção A).

Fora de escopo: trocar de modelo de IA, mudar regras de negócio (limites, aprovação, código), Cloud API
oficial.

## Regras que não mudam

- Valores, códigos e o resumo da proposta (`campaign.Summary`) nunca saem da IA e sempre vão em **texto**.
- Mensagens fixas continuam no Go (`internal/engine/messages.go`); só o texto delas muda de tom.
- Aviso ao operador (`ConversationID == 0`) sai direto, sem presença nem atraso.
- Tudo continua passando pelo outbox (retentativa, `OnJobFailed`).

## Comportamento

1. **Juntar mensagens seguidas (debounce).** Mensagem nova é gravada e agenda um job
   `conversation.reply` com `run_after = agora + REPLY_DEBOUNCE` (padrão 4 s). Quando o job roda, se já
   existe mensagem de entrada mais nova que a que o agendou, ele sai sem fazer nada (o job da mais nova
   responde). O job que responde junta todas as mensagens de entrada desde a última resposta em um texto
   só para a Atendente.
2. **Visto azul.** O job de resposta marca o chat como lido (`sendSeen`) antes de pensar. Com o debounce,
   o visto chega uns 4 s depois da última mensagem.
3. **Balões curtos.** A Atendente pode separar a resposta em até 3 partes com linha em branco. O Go
   corta em no máximo 3 balões (partes a mais são unidas na última) e envia um por vez.
4. **"Digitando…" proporcional.** Antes de cada balão: presença `typing`, espera
   `clamp(1,2 s + 35 ms × caracteres, 1,5 s, 7 s)` com variação de ±15 %, envia, presença `paused`.
   Entre balões, pausa curta (0,6–1,4 s).
5. **Áudio responde com áudio.** Se a última mensagem do lote foi áudio, a resposta é conversa da
   Atendente (não mensagem fixa) e tem até 600 caracteres: presença `recording`, gera a voz por TTS
   (OpenRouter `/audio/speech`, MP3), envia com `sendVoice` e `convert: true` (WAHA converte para
   OGG/Opus), presença `paused`. Tudo num balão só. Se o TTS falhar, cai para texto com "digitando…".
6. **Tom.** Prompt da Atendente ganha regras de conversa: chamar pelo nome quando souber, espelhar o jeito
   do cliente (formal/informal), frases curtas, no máximo 1 emoji e só se o cliente usar, sem frases de
   robô ("Registrei sua solicitação", "Como posso ajudar?"), sem repetir a mesma abertura. Quando a
   resposta for virar áudio, a entrada avisa a Atendente e ela responde como fala: sem listas, sem emoji,
   sem asterisco.
7. **Mensagens fixas reescritas** no mesmo tom (ex.: `msgBusy` = "Tô montando sua proposta com o
   especialista, já já chega aqui 👍"). A proposta com código não muda de formato.

## Componentes

- `whatsapp.Provider` ganha:
  - `MarkSeen(ctx, chatID) error`
  - `SetPresence(ctx, chatID string, p Presence) error` com `PresenceTyping`, `PresenceRecording`,
    `PresencePaused`
  - `SendVoice(ctx, chatID string, audio []byte, mime string) error`
  - Falha de presença ou visto é só registrada em log; nunca impede o envio.
- `internal/whatsapp/waha`: implementa os três com `POST /api/sendSeen`, `POST /api/{session}/presence`
  e `POST /api/sendVoice` (`file.data` em base64, `convert: true`).
- `internal/llm`: `Speak(ctx, model, voice, text) (audio []byte, mime string, Response, error)` no
  OpenRouter (`POST /audio/speech`, `response_format: mp3`); `Fake` ganha `Speech`.
- `internal/humanize` (novo, puro, sem I/O): `Split(reply) []string`, `TypingDelay(text, rnd)`,
  `Gap(rnd)`, `ShouldVoice(lastKind, text, fixed bool) bool`. Aleatoriedade injetada para testes.
- `internal/engine`:
  - `HandleInbound` para no ponto em que hoje chama `converse`/`approve`: grava a mensagem e agenda
    `conversation.reply` com o id dela. Limite diário, cliente bloqueado e falha de áudio continuam como
    estão (antes do agendamento).
  - Novo `reply(job)`: trava a conversa, confere se é a mensagem mais nova, marca visto, junta o lote,
    decide o caminho que hoje é decidido em `HandleInbound` (needs_human, awaiting_proposal,
    aprovação, `converse`).
  - `SendJob` ganha `Voice bool`, `Human bool` e `Next []string` (balões restantes). O job envia o
    balão atual com presença e, se houver `Next`, enfileira o próximo balão; assim a ordem é garantida e
    uma retentativa nunca reenvia balão já entregue.
  - `store.JobSpec` ganha `Delay time.Duration` (usa a coluna `run_after`, que já existe).
  - Migration `0005`: `conversations.replied_through` marca até qual mensagem de entrada já houve
    resposta, para o lote seguinte não repetir mensagens enquanto os balões anteriores estão na fila.
- Configuração (`internal/config`): `HUMANIZE` (padrão `true`), `REPLY_DEBOUNCE_SECONDS` (4),
  `VOICE_REPLIES` (`true`), `TTS_MODEL`, `TTS_VOICE`. Sem `TTS_MODEL`, áudio fica desligado. Com
  `HUMANIZE=false`, tudo volta ao comportamento atual (sem debounce, sem presença, um balão).
- Uso do TTS vai para `recordUsage` com papel `tts`.

## Erros

- Presença/visto falhou: log e segue.
- TTS falhou ou passou do tempo (20 s): envia o mesmo texto como balões com "digitando…".
- `sendVoice` falhou: o job tenta de novo como hoje; ao esgotar, cai no `OnJobFailed` existente.
- Job de resposta obsoleto (mensagem mais nova chegou): termina sem erro.
- O job de resposta tem prazo fixo como hoje; espera de digitação conta no prazo, por isso o teto de 7 s
  por balão.

## Testes (TDD)

- `humanize`: corte em balões (1, 3, 5 partes, texto sem quebra), atraso dentro dos limites,
  `ShouldVoice` (áudio + conversa curta = sim; mensagem fixa, resumo com código ou texto longo = não).
- `waha`: corpo e rota de `sendSeen`, `presence`, `sendVoice` com `httptest`.
- `llm/openrouter`: `Speak` envia modelo/voz/texto e devolve bytes e mime; erro HTTP vira erro.
- `engine` (banco de teste):
  - três mensagens em sequência geram uma resposta só, com as três no lote;
  - job obsoleto não chama a Atendente;
  - resposta em 2 balões sai em ordem, com presença `typing` antes de cada um;
  - áudio de entrada gera `recording` + `SendVoice`; TTS com erro gera texto;
  - resumo da proposta sai em texto mesmo quando a conversa veio de áudio;
  - `HUMANIZE=false` mantém os testes atuais verdes.

## Riscos e pré-requisitos

- WAHA precisa ser `2026.6.1` ou mais novo (envio de mídia no Core). Conferir a versão no Easypanel
  antes do deploy.
- WAHA tem "auto presence" próprio; se estiver ligado, desligar para não brigar com a nossa presença.
- TTS custa por resposta em áudio; limitado pelo teto de mensagens diárias que já existe.
- Modelo e voz do TTS: escolher na lista de TTS do OpenRouter na implementação (voz feminina/neutra em
  pt-BR), validar ouvindo um áudio real antes de ligar em produção.
- A chave `OPENROUTER_API_KEY` continua pendente de rotação (exposta antes no chat).
