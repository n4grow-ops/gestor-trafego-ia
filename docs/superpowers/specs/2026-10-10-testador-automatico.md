# Testador automático + avisos da equipe (2026-10-10)

Aprovado pelo dono. Enquanto o app da Meta está em desenvolvimento, só testadores conseguem conectar a conta.

## Fluxo
1. Cliente manda o link do perfil do Facebook em qualquer momento (normalmente quando a Mia pede para conectar a Meta,
   ou quando a Meta recusa o login). A Mia extrai o ID (`profile.php?id=N`) ou o nome de usuário (`facebook.com/nome`);
   link de compartilhamento (`facebook.com/share/...`, `fb.me/...`) é seguido por redirect; sem sucesso, a Mia pede o link
   da barra de endereço do navegador.
2. Grava `tester_requests` (status `requested`), responde ao cliente ("Recebi! Já pedi pra equipe te liberar, te aviso aqui")
   e avisa a equipe: "🧪 Cliente enviou ID, preciso que adicione como testador — Nome (Empresa), WhatsApp X. ID/usuário: Y.
   Link: Z. Adicionar em: https://developers.facebook.com/apps/{APP_ID}/roles/roles/".
3. Admin adiciona na Meta e aperta "Adicionei" no painel (fila Testadores) -> status `added`; a Mia avisa o cliente:
   "Te adicionei como testador! Aceita o convite no Facebook (Notificações ou https://developers.facebook.com/requests)
   e depois toca no link pra conectar: <link de conexão Meta>".
4. Confirmação real = conexão concluída (`CompleteMeta`) -> status `accepted`.
5. Reforço: job a cada 5 min lê `GET /{app}/roles` (token do app `APP_ID|SECRET`) e guarda os testadores conhecidos;
   se aparecer testador novo e houver exatamente UM pedido `added` sem conexão, a Mia manda o link de conexão de novo
   ("Vi que você aceitou ✅ ..."). A API NÃO mostra pendentes e devolve IDs internos do app (não casam com o ID do perfil).
6. Pedido `added` sem conexão há 24 h: a Mia lembra o cliente uma vez e a equipe é avisada.
7. Pedido de humano: o aviso à equipe passa a trazer o motivo (última mensagem do cliente, cortada em 200 caracteres,
   com segredos/links mascarados): "🙋 Cliente quer falar com humano — Nome (Empresa), WhatsApp X. Motivo: «...». Atender: https://admin.meuanuncio.io/".

## Canal da equipe
- O número oficial (Wame Cloud API) NÃO envia para grupo (erro #131000, exige OBA). Avisos vão no privado de
  `TEAM_PHONES` (lista separada por vírgula; padrão = OPERATOR_PHONE). Fora da janela de 24 h usa o template `aviso_equipe`.
- Opcional: grupo via instância Wame não-oficial (QR): `TEAM_GROUP_JID` + `TEAM_GROUP_WAME_KEY` + `TEAM_GROUP_WAME_TOKEN`.
  Só envio; mensagens de grupo continuam ignoradas. Sem essas variáveis, desligado.

## Contrato (stubs já commitados; ninguém muda assinaturas)
- `internal/meta/profilelink.go`: `ParseProfileLink(text) (ProfileRef, bool)`, `ResolveShareLink(ctx, *http.Client, url) (string, error)`,
  `(*Client) AppTesters(ctx) ([]string, error)`.
- `internal/store/tester.go`: tipos e funções do arquivo. Migração `0026_testadores.sql`.
- `internal/engine/team.go`: `TeamNotice` e `(e *Engine) teamJobs(ctx, TeamNotice) []store.JobSpec`.
