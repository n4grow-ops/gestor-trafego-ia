package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	JobSignupRemind = "signup.remind"

	firstReminderAfter  = 3 * time.Hour
	secondReminderAfter = 21 * time.Hour // 24 h depois do primeiro link
)

// Brasília não tem horário de verão desde 2019.
var brt = time.FixedZone("BRT", -3*3600)

// RemindJob lembra quem recebeu o link de cadastro e não concluiu. Attempt vai de 1 a 2.
type RemindJob struct {
	IdentityID     int64 `json:"identity_id"`
	ConversationID int64 `json:"conversation_id"`
	Attempt        int   `json:"attempt"`
}

// ReminderAt é quando lembrar: daqui a d, mas nunca entre 21 h e 8 h de Brasília (vai para as 9 h).
func ReminderAt(now time.Time, d time.Duration) time.Time {
	t := now.Add(d).In(brt)
	y, m, day := t.Date()
	switch {
	case t.Hour() >= 21:
		return time.Date(y, m, day+1, 9, 0, 0, 0, brt)
	case t.Hour() < 8:
		return time.Date(y, m, day, 9, 0, 0, 0, brt)
	}
	return t
}

// scheduleFirstReminder agenda o 1º lembrete, se a identidade ainda não tiver um pendente.
func (e *Engine) scheduleFirstReminder(ctx context.Context, identityID, convID int64) error {
	pending, err := e.Store.HasPendingJob(ctx, JobSignupRemind, "identity_id", identityID)
	if err != nil || pending {
		return err
	}
	return e.enqueueReminder(ctx, RemindJob{IdentityID: identityID, ConversationID: convID, Attempt: 1}, firstReminderAfter)
}

func (e *Engine) enqueueReminder(ctx context.Context, r RemindJob, after time.Duration) error {
	now := e.now()
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobSignupRemind, Payload: r, Delay: ReminderAt(now, after).Sub(now)})
}

// remind manda um link novo para quem ainda não concluiu o cadastro.
func (e *Engine) remind(ctx context.Context, r RemindJob) error {
	ident, err := e.Store.GetIdentity(ctx, r.IdentityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ident.RegistrationStatus == "complete" || ident.Status == "blocked" || e.SignupBaseURL == "" {
		return nil
	}
	conv, err := e.Store.GetConversation(ctx, r.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if conv.State == store.StateNeedsHuman {
		return nil
	}
	raw, hash, err := signup.NewToken()
	if err != nil {
		return err
	}
	if err := e.Store.CreateSignupLink(ctx, ident.ID, hash, e.now().Add(signupLinkTTL)); err != nil {
		return err
	}
	link := strings.TrimRight(e.SignupBaseURL, "/") + "/" + raw
	msg := msgSignupReminder1
	if r.Attempt > 1 {
		msg = msgSignupReminder2
	}
	e.audit(ctx, ident.OrgID, "system", "signup_reminder_sent", map[string]any{"identity_id": ident.ID, "attempt": r.Attempt})
	if err := e.send(ctx, conv, fmt.Sprintf(msg, link)); err != nil {
		return err
	}
	if r.Attempt == 1 {
		return e.enqueueReminder(ctx, RemindJob{IdentityID: r.IdentityID, ConversationID: r.ConversationID, Attempt: 2}, secondReminderAfter)
	}
	return nil
}
