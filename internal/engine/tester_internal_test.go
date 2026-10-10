package engine

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// M6: link na fala do cliente vira só o host no motivo do aviso.
func TestClientMotiveMasksLinksToHost(t *testing.T) {
	got := clientMotive("olha https://www.facebook.com/pizzariaboa/posts/1?x=2 e www.site.com.br/promo?cupom=AB12 e cadastro.meuanuncio.io/c/AbCdEf123456")
	for _, bad := range []string{"pizzariaboa", "posts", "cupom", "AbCdEf123456", "promo"} {
		if strings.Contains(got, bad) {
			t.Errorf("motivo com %q: %q", bad, got)
		}
	}
	for _, want := range []string{"[link facebook.com]", "[link site.com.br]", "[link cadastro.meuanuncio.io]", "olha"} {
		if !strings.Contains(got, want) {
			t.Errorf("motivo sem %q: %q", want, got)
		}
	}
}

// M7: aviso sem nenhum número da equipe não some calado.
func TestTeamJobsWarnsWhenNoDestination(t *testing.T) {
	var buf bytes.Buffer
	e := &Engine{Log: slog.New(slog.NewTextHandler(&buf, nil))}
	if jobs := e.teamJobs(context.Background(), TeamNotice{Kind: TeamTester, Reason: "x"}); len(jobs) != 0 {
		t.Fatalf("jobs = %v", jobs)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "tester") {
		t.Fatalf("log = %q", buf.String())
	}
}
