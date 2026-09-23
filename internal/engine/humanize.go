package engine

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	ttsTimeout = 20 * time.Second
	// Espera de uma resposta nova enquanto ainda há balões da anterior na fila.
	replyWait = 2 * time.Second
)

type voiceKey struct{}

// withVoice marca que o cliente falou por áudio neste lote.
func withVoice(ctx context.Context) context.Context { return context.WithValue(ctx, voiceKey{}, true) }

func voiceWanted(ctx context.Context) bool {
	v, _ := ctx.Value(voiceKey{}).(bool)
	return v
}

// reply responde o lote de mensagens ainda sem resposta, se esta for a mensagem mais nova.
func (e *Engine) reply(ctx context.Context, r ReplyJob) error {
	unlock, err := e.Store.LockConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	latest, err := e.Store.LatestInboundID(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	if latest != r.MessageID {
		return nil // chegou mensagem nova: o job dela responde tudo
	}
	// Balões da resposta anterior ainda na fila: espera saírem, para não intercalar
	// e para a próxima resposta enxergá-los no histórico.
	busy, err := e.Store.HasPendingSends(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	if busy {
		return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobReply, Payload: r, Delay: replyWait})
	}
	msgs, err := e.Store.PendingInbound(ctx, r.ConversationID, r.MessageID, maxBatch)
	if err != nil || len(msgs) == 0 {
		return err
	}
	conv, err := e.Store.GetConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if ident.Status != "blocked" && conv.State != store.StateNeedsHuman {
		e.markSeen(ctx, conv.ChatID)
		texts := make([]string, len(msgs))
		ids := make([]int64, len(msgs))
		for i, m := range msgs {
			texts[i], ids[i] = m.Body, m.ID
		}
		last := msgs[len(msgs)-1]
		if last.Kind == string(whatsapp.KindAudio) {
			ctx = withVoice(ctx)
		}
		if err := e.dispatch(ctx, ident, conv, strings.Join(texts, "\n"), texts, ids); err != nil {
			return err
		}
	}
	return e.Store.MarkReplied(ctx, conv.ID, r.MessageID)
}

// humanJob monta o primeiro envio humano; os outros balões seguem em Next.
func humanJob(orgID int64, conv store.Conversation, text string, voice bool) store.JobSpec {
	s := SendJob{OrgID: orgID, ConversationID: conv.ID, ChatID: conv.ChatID, Text: text, Human: true, Voice: voice}
	if !voice {
		s = splitJob(s)
	}
	return store.JobSpec{Kind: JobSend, Payload: s, MaxAttempts: sendAttempts}
}

func splitJob(s SendJob) SendJob {
	parts := humanize.Split(s.Text)
	if len(parts) == 0 {
		parts = []string{s.Text}
	}
	s.Voice, s.Text, s.Next = false, parts[0], parts[1:]
	return s
}

// sendHuman envia um balão em duas etapas, sem prender o worker: primeiro mostra "digitando…"
// e agenda a entrega para depois do tempo de digitação; na entrega manda o texto e agenda o
// próximo balão depois de uma pausa.
func (e *Engine) sendHuman(ctx context.Context, s SendJob) error {
	conv, err := e.Store.GetConversation(ctx, s.ConversationID)
	if err != nil {
		return err
	}
	if conv.State == store.StateNeedsHuman {
		return nil // um humano assumiu: balões antigos não saem mais
	}
	if s.Voice {
		return e.sendVoice(ctx, s)
	}
	if !s.Typed {
		e.presence(ctx, s.ChatID, whatsapp.PresenceTyping)
		s.Typed = true
		return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobSend, Payload: s, MaxAttempts: sendAttempts,
			Delay: humanize.TypingDelay(s.Text, e.rand())})
	}
	if err := e.WA.SendText(ctx, s.ChatID, s.Text); err != nil {
		return err
	}
	e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
	if err := e.Store.AddMessage(ctx, s.ConversationID, "out", "text", s.Text); err != nil {
		return err
	}
	if len(s.Next) == 0 {
		return nil
	}
	next := s
	next.Text, next.Next, next.Typed = s.Next[0], s.Next[1:], false
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobSend, Payload: next, MaxAttempts: sendAttempts, Delay: humanize.Gap(e.rand())})
}

// sendVoice mostra "gravando áudio…" enquanto o TTS gera a voz (esse é o tempo de gravação) e envia.
// Se a voz não puder ser gerada ou o WhatsApp recusar, o mesmo texto segue em balões, sem gerar de novo.
func (e *Engine) sendVoice(ctx context.Context, s SendJob) error {
	if e.Voice == nil {
		return e.Store.Enqueue(ctx, textFallback(s))
	}
	e.presence(ctx, s.ChatID, whatsapp.PresenceRecording)
	tctx, cancel := context.WithTimeout(ctx, ttsTimeout)
	sp, err := e.Voice.Speak(tctx, e.TTSModel, e.TTSVoice, humanize.SpeechText(s.Text))
	cancel()
	if s.OrgID != 0 {
		e.recordUsage(ctx, s.OrgID, "tts", sp.Usage)
	}
	if err == nil {
		err = e.WA.SendVoice(ctx, s.ChatID, sp.Audio, sp.Mime)
	}
	e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
	if err != nil {
		e.Log.Error("voz falhou, indo em texto", "conversation_id", s.ConversationID, "erro", err)
		return e.Store.Enqueue(ctx, textFallback(s))
	}
	return e.Store.AddMessage(ctx, s.ConversationID, "out", "voice", s.Text)
}

func textFallback(s SendJob) store.JobSpec {
	return store.JobSpec{Kind: JobSend, Payload: splitJob(s), MaxAttempts: sendAttempts}
}

// presence e markSeen são cosméticos: falha só vai para o log.
func (e *Engine) presence(ctx context.Context, chatID string, p whatsapp.Presence) {
	if err := e.WA.SetPresence(ctx, chatID, p); err != nil {
		e.Log.Warn("falha na presença", "presenca", string(p), "erro", err)
	}
}

func (e *Engine) markSeen(ctx context.Context, chatID string) {
	if err := e.WA.MarkSeen(ctx, chatID); err != nil {
		e.Log.Warn("falha ao marcar visto", "erro", err)
	}
}

func (e *Engine) rand() float64 {
	if e.Rand != nil {
		return e.Rand()
	}
	return rand.Float64()
}

func (e *Engine) debounce() time.Duration {
	return max(e.Debounce, 0)
}
