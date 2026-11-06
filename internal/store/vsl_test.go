package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestVSLStats(t *testing.T) {
	st, _ := newStore(t)
	now := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	const vid = "B-mCESD6gtg"
	ev := func(sess, vis string, mut func(*store.VSLEvent)) {
		t.Helper()
		e := store.VSLEvent{Session: sess, Visitor: vis, VideoID: vid, Device: "mobile"}
		if mut != nil {
			mut(&e)
		}
		must(t, st.RecordVSL(ctx, e, now))
	}
	a, b, c := "00000000-0000-4000-8000-00000000000a", "00000000-0000-4000-8000-00000000000b", "00000000-0000-4000-8000-00000000000c"
	va, vb := "11111111-0000-4000-8000-000000000001", "11111111-0000-4000-8000-000000000002"

	// a: abriu, deu play, viu até a metade, depois o vídeo todo e clicou.
	ev(a, va, nil)
	ev(a, va, func(e *store.VSLEvent) { e.Played, e.Duration, e.Max, e.Mask = true, 200, 100, 1<<10-1 })
	ev(a, va, func(e *store.VSLEvent) { e.Duration, e.Max, e.Mask, e.Watched = 200, 200, 1<<20-1, 200 })
	// Evento atrasado com menos progresso não apaga o que já foi visto.
	ev(a, va, func(e *store.VSLEvent) { e.Duration, e.Max, e.Mask = 200, 30, 1 })
	ev(a, va, func(e *store.VSLEvent) { e.CTA = true })
	// b: abriu e não deu play.
	ev(b, vb, nil)
	// c: mesmo visitante de b voltou e viu só os primeiros 25%; desktop.
	must(t, st.RecordVSL(ctx, store.VSLEvent{Session: c, Visitor: vb, VideoID: vid, Played: true, Device: "desktop",
		Duration: 200, Max: 50, Mask: 1<<5 - 1, Watched: 50}, now.Add(time.Second)))
	// Sessão de outro visitante não pode ser sobrescrita.
	ev(a, vb, func(e *store.VSLEvent) { e.Mask = 0 })
	// Outro vídeo não entra na conta.
	must(t, st.RecordVSL(ctx, store.VSLEvent{Session: "00000000-0000-4000-8000-0000000000ff", Visitor: va, VideoID: "outroVideo1", Played: true}, now))

	r, err := st.VSLStats(ctx, vid, 30, now.Add(time.Minute))
	must(t, err)
	if r.Views != 3 || r.Visitors != 2 || r.Plays != 2 || r.PlayVisitors != 2 || r.CTA != 1 || r.CTAPlayed != 1 || r.DurationS != 200 {
		t.Fatalf("contagens: %+v", r)
	}
	if r.Retention[0] != 2 || r.Retention[4] != 2 || r.Retention[5] != 1 || r.Retention[19] != 1 {
		t.Fatalf("retenção: %v", r.Retention)
	}
	if r.Milestones["25"] != 1 || r.Milestones["100"] != 1 {
		t.Fatalf("marcos: %v", r.Milestones)
	}
	// (200 s + 50 s) / 2 plays = 125 s
	if r.AvgWatchedS != 125 {
		t.Fatalf("tempo médio: %d", r.AvgWatchedS)
	}
	if r.Devices["mobile"] != 1 || r.Devices["desktop"] != 1 {
		t.Fatalf("aparelhos: %v", r.Devices)
	}
	if len(r.Recent) != 2 || !r.Recent[0].Played {
		t.Fatalf("lista: %+v", r.Recent)
	}
	var full, back store.VSLSession
	for _, s := range r.Recent {
		if s.WatchedPct == 100 {
			full = s
		} else {
			back = s
		}
	}
	if full.MaxPct != 100 || !full.CTA || full.Returning || back.WatchedPct != 25 || back.MaxS != 50 || !back.Returning {
		t.Fatalf("sessões: %+v %+v", full, back)
	}
	if len(r.Series) != 30 || r.Series[29].Views != 3 || r.Series[29].Plays != 2 {
		t.Fatalf("série: %d %+v", len(r.Series), r.Series[len(r.Series)-1])
	}
}
