package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// keepTurns: o painel mostra o raciocínio da Mia dos últimos 30 dias.
const keepTurns = 30 * 24 * time.Hour

// turnLog junta, durante uma resposta, o que a IA entendeu e o que o sistema fez (painel: "por que a Mia
// respondeu assim").
type turnLog struct {
	mu      sync.Mutex
	turn    *contract.AttendantTurn
	actions []string
}

type turnLogKey struct{}

func withTurnLog(ctx context.Context) (context.Context, *turnLog) {
	l := &turnLog{}
	return context.WithValue(ctx, turnLogKey{}, l), l
}

func turnLogOf(ctx context.Context) *turnLog {
	l, _ := ctx.Value(turnLogKey{}).(*turnLog)
	return l
}

// noteAction anota o que o sistema fez nesta resposta (sem coletor, não faz nada).
func noteAction(ctx context.Context, action string) {
	if l := turnLogOf(ctx); l != nil {
		l.mu.Lock()
		l.actions = append(l.actions, action)
		l.mu.Unlock()
	}
}

// noteTurn guarda o que a IA entendeu.
func noteTurn(ctx context.Context, t contract.AttendantTurn) {
	if l := turnLogOf(ctx); l != nil {
		l.mu.Lock()
		l.turn = &t
		l.mu.Unlock()
	}
}

// saveTurn grava o turno da resposta (só quando houve IA ou alguma ação do sistema).
func (e *Engine) saveTurn(ctx context.Context, l *turnLog, convID int64, batch []int64) {
	l.mu.Lock()
	turn, actions := l.turn, append([]string(nil), l.actions...)
	l.mu.Unlock()
	if turn == nil && len(actions) == 0 {
		return
	}
	t := store.MiaTurn{ConversationID: convID, Intent: "sistema"}
	if len(batch) > 0 {
		t.MessageID = batch[len(batch)-1]
	} else if id, err := e.Store.LatestInboundID(ctx, convID); err == nil {
		t.MessageID = id
	}
	if turn != nil {
		t.Intent, t.Project, t.Reply = string(turn.Intent), turn.Project, turn.Reply
		t.Fields, _ = json.Marshal(turn.Fields)
		t.Profile, _ = json.Marshal(turn.Profile)
	}
	if len(actions) == 0 {
		actions = []string{"só respondeu"}
	}
	t.Action = strings.Join(actions, " · ")
	if err := e.Store.AddMiaTurn(context.WithoutCancel(ctx), t); err != nil {
		e.Log.Warn("turno da Mia não gravado", "conversation_id", convID, "erro", err)
	}
}
