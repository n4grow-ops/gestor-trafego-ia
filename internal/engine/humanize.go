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

const ttsTimeout = 20 * time.Second

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
		if err := e.dispatch(ctx, ident, conv, strings.Join(texts, "\n"), last.Body, ids); err != nil {
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

// sendHuman envia um balão com presença; se houver mais, agenda o próximo depois de uma pausa.
func (e *Engine) sendHuman(ctx context.Context, s SendJob) error {
	if s.Voice {
		ok, err := e.sendVoice(ctx, s)
		if err != nil || ok {
			return err
		}
		s = splitJob(s) // sem voz: mesmo texto em balões
	}
	e.presence(ctx, s.ChatID, whatsapp.PresenceTyping)
	if err := e.sleep(ctx, humanize.TypingDelay(s.Text, e.rand())); err != nil {
		return err
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
	next.Text, next.Next = s.Next[0], s.Next[1:]
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobSend, Payload: next, MaxAttempts: sendAttempts, Delay: humanize.Gap(e.rand())})
}

// sendVoice devolve ok=false quando a voz não pôde ser gerada; o chamador manda em texto.
func (e *Engine) sendVoice(ctx context.Context, s SendJob) (bool, error) {
	if e.Voice == nil {
		return false, nil
	}
	e.presence(ctx, s.ChatID, whatsapp.PresenceRecording)
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, ttsTimeout)
	sp, err := e.Voice.Speak(tctx, e.TTSModel, e.TTSVoice, humanize.SpeechText(s.Text))
	cancel()
	if s.OrgID != 0 {
		e.recordUsage(ctx, s.OrgID, "tts", sp.Usage)
	}
	if err != nil {
		e.Log.Error("tts falhou, indo em texto", "conversation_id", s.ConversationID, "erro", err)
		e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
		return false, nil
	}
	if wait := humanize.TypingDelay(s.Text, e.rand()) - time.Since(start); wait > 0 {
		if err := e.sleep(ctx, wait); err != nil {
			return false, err
		}
	}
	if err := e.WA.SendVoice(ctx, s.ChatID, sp.Audio, sp.Mime); err != nil {
		return false, err
	}
	e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
	return true, e.Store.AddMessage(ctx, s.ConversationID, "out", "voice", s.Text)
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

func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
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
