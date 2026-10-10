package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fakeDirectory conta as leituras e devolve a lista (ou o erro) da vez.
type fakeDirectory struct {
	calls  int
	phones []string
	err    error
}

func (f *fakeDirectory) load(context.Context) ([]string, error) {
	f.calls++
	return f.phones, f.err
}

func newTeamEngine(dir *fakeDirectory, now *time.Time) *Engine {
	e := &Engine{
		TeamPhones:    []string{"5511911111111"},
		OperatorPhone: "5511900000000",
		Now:           func() time.Time { return *now },
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if dir != nil {
		e.TeamDirectory = dir.load
	}
	return e
}

func TestTeamPhonesFallbackOrder(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	// Sem diretório: TEAM_PHONES; sem TEAM_PHONES: OPERATOR_PHONE; sem nada: ninguém.
	e := newTeamEngine(nil, &now)
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511911111111" {
		t.Fatalf("TEAM_PHONES = %q", got)
	}
	e.TeamPhones = nil
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511900000000" {
		t.Fatalf("OPERATOR_PHONE = %q", got)
	}
	e.OperatorPhone = ""
	if got := e.teamPhones(ctx); len(got) != 0 {
		t.Fatalf("sem números = %q", got)
	}

	// Diretório vazio cai no TEAM_PHONES.
	dir := &fakeDirectory{}
	e = newTeamEngine(dir, &now)
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511911111111" {
		t.Fatalf("diretório vazio = %q", got)
	}

	// Com números no painel, eles valem no lugar do TEAM_PHONES e do OPERATOR_PHONE.
	dir = &fakeDirectory{phones: []string{"5511922222222", "5511933333333"}}
	e = newTeamEngine(dir, &now)
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511922222222,5511933333333" {
		t.Fatalf("painel = %q", got)
	}
}

func TestTeamDirectoryCache(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	dir := &fakeDirectory{phones: []string{"5511922222222"}}
	e := newTeamEngine(dir, &now)

	e.teamPhones(ctx)
	now = now.Add(29 * time.Second)
	e.teamPhones(ctx)
	if dir.calls != 1 {
		t.Fatalf("leituras dentro de 30 s = %d", dir.calls)
	}

	// Passados 30 s lê de novo e pega a mudança feita no painel.
	dir.phones = []string{"5511944444444"}
	now = now.Add(2 * time.Second)
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511944444444" || dir.calls != 2 {
		t.Fatalf("depois do TTL = %q (%d leituras)", got, dir.calls)
	}

	// Banco fora: segue com a última lista boa e não tenta de novo antes do TTL.
	dir.err = errors.New("banco fora")
	now = now.Add(31 * time.Second)
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511944444444" {
		t.Fatalf("banco fora = %q", got)
	}
	e.teamPhones(ctx)
	if dir.calls != 3 {
		t.Fatalf("martelou o banco fora do ar: %d leituras", dir.calls)
	}

	// Invalidate força a leitura seguinte.
	dir.err = nil
	dir.phones = []string{"5511955555555"}
	e.InvalidateTeamDirectory()
	if got := strings.Join(e.teamPhones(ctx), ","); got != "5511955555555" || dir.calls != 4 {
		t.Fatalf("invalidate = %q (%d leituras)", got, dir.calls)
	}
}

func TestTeamDirectoryErrorWithoutCacheFallsBack(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	dir := &fakeDirectory{err: errors.New("banco fora")}
	e := newTeamEngine(dir, &now)
	if got := strings.Join(e.teamPhones(context.Background()), ","); got != "5511911111111" {
		t.Fatalf("erro sem lista anterior = %q", got)
	}
}
