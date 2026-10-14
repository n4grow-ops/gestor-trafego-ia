package report

import (
	"strings"
	"testing"
	"time"
)

func TestOwnerTextWithData(t *testing.T) {
	got := OwnerText(Input{
		Campaigns: []Campaign{{Name: "Pizza", SpendCents: 3500, Conversations: 5}},
		Prev:      &Totals{SpendCents: 3000, Conversations: 5},
	})
	for _, want := range []string{"R$ 35,00", "5 conversas", "R$ 7,00 por conversa"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sem %q: %q", want, got)
		}
	}
	if strings.Contains(got, "mais barato") {
		t.Fatalf("um anúncio só não tem comparação: %q", got)
	}
}

func TestOwnerTextNoData(t *testing.T) {
	for _, in := range []Input{{}, {Campaigns: []Campaign{{Name: "A"}}}} {
		if got := OwnerText(in); got != NoData {
			t.Fatalf("got %q", got)
		}
	}
}

func TestOwnerTextBestAd(t *testing.T) {
	got := OwnerText(Input{Campaigns: []Campaign{
		{Name: "Promo", SpendCents: 4000, Conversations: 2},
		{Name: "Delivery", SpendCents: 3000, Conversations: 6},
		{Name: "Sem conversa", SpendCents: 1000},
	}})
	if !strings.Contains(got, "*Delivery*, a R$ 5,00 por conversa") {
		t.Fatalf("anúncio mais barato errado: %q", got)
	}
	// só uma campanha com conversa: não há o que comparar
	got = OwnerText(Input{Campaigns: []Campaign{{Name: "A", SpendCents: 1000, Conversations: 2}, {Name: "B", SpendCents: 1000}}})
	if strings.Contains(got, "mais barato") {
		t.Fatalf("comparou com anúncio sem conversa: %q", got)
	}
}

func TestOwnerTextDrop(t *testing.T) {
	got := OwnerText(Input{
		Campaigns: []Campaign{{Name: "A", SpendCents: 3000, Conversations: 2}},
		Prev:      &Totals{SpendCents: 3000, Conversations: 8},
	})
	if !strings.Contains(got, "menos conversas que no dia anterior: 2, contra 8") || !strings.Contains(got, "revisa o anúncio") {
		t.Fatalf("queda sem aviso: %q", got)
	}
}

func TestOwnerTextZeroConversations(t *testing.T) {
	got := OwnerText(Input{Campaigns: []Campaign{{Name: "A", SpendCents: 2000}}})
	if !strings.Contains(got, "ainda não chegou nenhuma conversa") || strings.Contains(got, "por conversa") {
		t.Fatalf("got %q", got)
	}
	// zero depois de um dia bom é queda, com sugestão
	got = OwnerText(Input{Campaigns: []Campaign{{Name: "A", SpendCents: 2000}}, Prev: &Totals{SpendCents: 2000, Conversations: 4}})
	if !strings.Contains(got, "contra 4") {
		t.Fatalf("got %q", got)
	}
}

func TestOwnerTextLittleDataIsHonest(t *testing.T) {
	got := OwnerText(Input{Campaigns: []Campaign{{Name: "A", SpendCents: 1500, Conversations: 2}}})
	if !strings.Contains(got, "pouco dado") || strings.Contains(got, "Se quiser") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(OwnerText(Input{Campaigns: []Campaign{{Name: "A", SpendCents: 1500, Conversations: 1}}}), "1 conversas") {
		t.Fatal("plural errado")
	}
}

func TestOwnerTextRise(t *testing.T) {
	got := OwnerText(Input{
		Campaigns: []Campaign{{Name: "A", SpendCents: 3000, Conversations: 9}},
		Prev:      &Totals{SpendCents: 3000, Conversations: 5},
	})
	if !strings.Contains(got, "melhor que o dia anterior: 9 conversas, contra 5") {
		t.Fatalf("got %q", got)
	}
}

// Nunca passa de ~45 s falado, com no máximo uma sugestão, mesmo com nome de anúncio enorme.
func TestOwnerTextShort(t *testing.T) {
	long := strings.Repeat("promoção imperdível ", 10)
	got := OwnerText(Input{
		Campaigns: []Campaign{{Name: long, SpendCents: 123456, Conversations: 20}, {Name: long + "2", SpendCents: 90000, Conversations: 30}},
		Prev:      &Totals{SpendCents: 3000, Conversations: 60},
	})
	if n := len(strings.Fields(got)); n > MaxWords {
		t.Fatalf("%d palavras: %q", n, got)
	}
	if strings.Count(got, "Se quiser") > 1 {
		t.Fatalf("mais de uma sugestão: %q", got)
	}
}

func TestBRLAndSpoken(t *testing.T) {
	if got := BRL(123456); got != "R$ 1.234,56" {
		t.Fatal(got)
	}
	if got := BRL(5); got != "R$ 0,05" {
		t.Fatal(got)
	}
	if got := Spoken("*Ontem* R$ 35,00 e R$ 7,50 e R$ 1,00"); got != "Ontem 35 reais e 7 reais e 50 centavos e 1 real" {
		t.Fatal(got)
	}
	if got := Spoken("custou R$ 0,05"); got != "custou 0 reais e 5 centavos" {
		t.Fatal(got)
	}
}

func TestQuiet(t *testing.T) {
	day := func(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, BRT) }
	cases := []struct {
		t    time.Time
		want bool
	}{{day(7, 59), true}, {day(8, 0), false}, {day(20, 59), false}, {day(21, 0), true}, {day(0, 30), true},
		{time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC), false}, // 08h BRT
		{time.Date(2026, 10, 14, 10, 59, 0, 0, time.UTC), true}, // 07h59 BRT
	}
	for _, c := range cases {
		if got := Quiet(c.t); got != c.want {
			t.Errorf("Quiet(%v) = %v", c.t, got)
		}
	}
}

func TestDelivery(t *testing.T) {
	ok := Delivery{AudioPref: true, VoiceAvailable: true}
	if !ok.Audio() {
		t.Fatal("áudio aceito e liberado deveria sair")
	}
	for name, d := range map[string]Delivery{
		"sem aceite":             {VoiceAvailable: true},
		"sem voz configurada":    {AudioPref: true},
		"silêncio":               {AudioPref: true, VoiceAvailable: true, Quiet: true},
		"fora da janela oficial": {AudioPref: true, VoiceAvailable: true, NeedsTemplates: true, OutsideWindow: true},
	} {
		if d.Audio() {
			t.Errorf("%s: não deveria mandar áudio", name)
		}
	}
	// dentro da janela da API oficial ainda sai
	if !(Delivery{AudioPref: true, VoiceAvailable: true, NeedsTemplates: true}).Audio() {
		t.Fatal("dentro da janela deveria sair")
	}
}

func TestOfferOnce(t *testing.T) {
	d := Delivery{VoiceAvailable: true}
	if !d.OfferAudio(false) {
		t.Fatal("primeira vez deveria convidar")
	}
	if d.OfferAudio(true) {
		t.Fatal("já convidou")
	}
	if (Delivery{VoiceAvailable: true, AudioPref: true}).OfferAudio(false) {
		t.Fatal("já aceitou")
	}
	if (Delivery{VoiceAvailable: true, NeedsTemplates: true, OutsideWindow: true}).OfferAudio(false) {
		t.Fatal("convite não cabe em modelo aprovado")
	}
	if (Delivery{}).OfferAudio(false) {
		t.Fatal("sem voz não convida")
	}
}

func TestParsePref(t *testing.T) {
	cases := map[string]PrefCommand{
		"áudio": PrefAudio, "Audio": PrefAudio, "sim, áudio!": PrefAudio, "quero em áudio": PrefAudio, "resumo em áudio": PrefAudio,
		"só texto": PrefText, "So texto": PrefText, "apenas texto.": PrefText, "quero só texto": PrefText,
		"manda um áudio explicando o anúncio por favor": PrefNone, "texto": PrefNone, "ok": PrefNone, "": PrefNone,
		"mande o áudio": PrefNone,
	}
	for in, want := range cases {
		if got := ParsePref(in); got != want {
			t.Errorf("ParsePref(%q) = %v, quer %v", in, got, want)
		}
	}
}
