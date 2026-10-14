package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/report"
)

func ownerReportHarness(t *testing.T) (*harness, *llm.Fake, int64) {
	t.Helper()
	h, pub, _ := publishWith(t, &meta.Funding{Status: 1, HasSource: true, AvailableCents: -1})
	h.say("ATIVAR")
	pub.insights = meta.Insights{SpendCents: 3500, Impressions: 2100, Reach: 1300, Conversations: 5}
	tts := &llm.Fake{Speech: []byte("MP3")}
	h.eng.Voice, h.eng.TTSModel, h.eng.TTSVoice = tts, "tts", "nova"
	var org int64
	h.must(h.pool.QueryRow(ctx, `SELECT id FROM organizations LIMIT 1`).Scan(&org))
	return h, tts, org
}

func (h *harness) runResults() {
	h.t.Helper()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobDailyResults)))
	// O job diário sempre agenda o do dia seguinte (é recorrente por desenho): o drain o rodaria de novo e de
	// novo, sem nunca esvaziar a fila. Tira o próximo da fila e drena só o que o relatório de hoje gerou.
	_, err := h.pool.Exec(ctx, `DELETE FROM outbox_jobs WHERE status = 'pending' AND kind = $1`, engine.JobDailyResults)
	h.must(err)
	h.drain()
}

// Padrão: só texto, com o convite para o áudio uma única vez.
func TestOwnerReportTextOnlyOffersAudioOnce(t *testing.T) {
	h, tts, org := ownerReportHarness(t)
	h.runResults()
	sent := h.wa.to(chatID)
	got := sent[len(sent)-1]
	for _, want := range []string{"R$ 35,00", "5 conversas", "R$ 7,00 por conversa", report.Offer} {
		if !strings.Contains(got, want) {
			t.Fatalf("relatório sem %q: %q", want, got)
		}
	}
	if len(tts.Spoken) != 0 || len(h.wa.voices) != 0 {
		t.Fatal("sem aceite não manda áudio")
	}
	pref, err := h.st.GetReportPref(ctx, org)
	h.must(err)
	if pref.Audio || !pref.Offered {
		t.Fatalf("pref = %+v", pref)
	}
	h.runResults() // no dia seguinte
	sent = h.wa.to(chatID)
	if again := sent[len(sent)-1]; strings.Contains(again, report.Offer) {
		t.Fatalf("convite repetido: %q", again)
	}
}

// Com o aceite: texto e áudio; o áudio fala o dinheiro por extenso.
func TestOwnerReportSendsAudioWhenAccepted(t *testing.T) {
	h, tts, org := ownerReportHarness(t)
	h.must(h.st.SetReportAudio(ctx, org, true))
	h.runResults()
	sent := h.wa.to(chatID)
	if got := sent[len(sent)-1]; !strings.Contains(got, "R$ 35,00") || strings.Contains(got, report.Offer) {
		t.Fatalf("texto = %q", got)
	}
	if len(tts.Spoken) != 1 || !strings.Contains(tts.Spoken[0], "35 reais") || strings.Contains(tts.Spoken[0], "R$") {
		t.Fatalf("TTS recebeu %q", tts.Spoken)
	}
	if len(h.wa.voices) != 1 {
		t.Fatalf("vozes = %d", len(h.wa.voices))
	}
}

// Voz que falha não repete o relatório em texto.
func TestOwnerReportVoiceFailureDoesNotDuplicateText(t *testing.T) {
	h, _, org := ownerReportHarness(t)
	h.must(h.st.SetReportAudio(ctx, org, true))
	h.wa.voiceFail = true
	before := h.wa.count()
	h.runResults()
	if n := h.wa.count() - before; n != 1 {
		t.Fatalf("textos enviados = %d, queria 1", n)
	}
}

// Fora da janela de 24 h da API oficial: só o modelo aprovado, sem áudio e sem convite.
func TestOwnerReportOutsideWindowNoAudio(t *testing.T) {
	h, tts, org := ownerReportHarness(t)
	h.must(h.st.SetReportAudio(ctx, org, true))
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	_, err := h.pool.Exec(ctx, `UPDATE messages SET created_at = $1::timestamptz - interval '30 hours' WHERE direction = 'in'`, h.now)
	h.must(err)
	h.runResults()
	if len(tts.Spoken) != 0 || len(h.wa.voices) != 0 {
		t.Fatal("fora da janela não manda áudio")
	}
	var tpl int
	for _, tp := range wa.tpls {
		if tp.Name == engine.TplDailyResults {
			tpl++
		}
	}
	if tpl != 1 {
		t.Fatalf("modelos de resultado = %d", tpl)
	}
}

// Entre 21h e 8h (BRT) nada sai: o job se reagenda para as 8h.
func TestOwnerReportQuietHours(t *testing.T) {
	h, tts, org := ownerReportHarness(t)
	h.must(h.st.SetReportAudio(ctx, org, true))
	h.now = time.Date(2026, 9, 23, 1, 0, 0, 0, time.UTC) // 22h de Brasília
	before := h.wa.count()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobDailyResults)))
	h.runJobs()
	if h.wa.count() != before || len(tts.Spoken) != 0 {
		t.Fatal("relatório saiu no horário de silêncio")
	}
	if h.pendingJobs(engine.JobDailyResults) != 1 {
		t.Fatal("o relatório deveria ficar agendado para as 8h")
	}
}

// "áudio" e "só texto" mudam a preferência, mas só depois do convite.
func TestOwnerReportPreferenceCommands(t *testing.T) {
	h, _, org := ownerReportHarness(t)
	for len(h.att.Responses) < 50 { // sem convite, "áudio" segue o fluxo normal da Atendente
		h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Certo!"}`)
	}
	h.say("áudio")
	h.runJobs()
	if pref, err := h.st.GetReportPref(ctx, org); err != nil || pref.Audio || pref.Offered {
		t.Fatalf("sem convite a palavra não muda nada: %+v %v", pref, err)
	}
	h.runResults() // faz o convite
	h.say("áudio")
	h.runJobs()
	if pref, _ := h.st.GetReportPref(ctx, org); !pref.Audio {
		t.Fatal("áudio não ligou")
	}
	sent := h.wa.to(chatID)
	if got := sent[len(sent)-1]; !strings.Contains(got, "também mando em áudio") {
		t.Fatalf("resposta = %q", got)
	}
	h.say("só texto")
	h.runJobs()
	if pref, _ := h.st.GetReportPref(ctx, org); pref.Audio {
		t.Fatal("só texto não desligou")
	}
}
