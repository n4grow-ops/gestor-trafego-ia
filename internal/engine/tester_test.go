package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeTesters guarda os pedidos de testador em memória (o store real é de outro agente).
type fakeTesters struct {
	mu       sync.Mutex
	reqs     []store.TesterRequestView
	known    map[string]bool
	accepted []int64
	reminded []int64

	notified       map[int64]time.Time
	acceptNotified map[int64]bool
}

func (f *fakeTesters) open(identityID int64) int {
	for i, r := range f.reqs {
		if r.IdentityID == identityID && (r.Status == store.TesterRequested || r.Status == store.TesterAdded) {
			return i
		}
	}
	return -1
}

func (f *fakeTesters) UpsertTesterRequest(_ context.Context, r store.TesterRequest) (store.TesterRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.open(r.IdentityID); i >= 0 {
		if f.reqs[i].RefKind != r.RefKind || f.reqs[i].RefValue != r.RefValue {
			f.reqs[i].Status = store.TesterRequested // perfil novo: a equipe adiciona de novo
		}
		f.reqs[i].ProfileURL, f.reqs[i].RefKind, f.reqs[i].RefValue = r.ProfileURL, r.RefKind, r.RefValue
		return f.reqs[i].TesterRequest, nil
	}
	r.ID = int64(len(f.reqs) + 1)
	r.Status = store.TesterRequested
	f.reqs = append(f.reqs, store.TesterRequestView{TesterRequest: r, MemberName: "Ana", OrgName: "Pizzaria Boa", Phone: phone})
	return r, nil
}

func (f *fakeTesters) OpenTesterRequest(_ context.Context, identityID int64) (store.TesterRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.open(identityID); i >= 0 {
		return f.reqs[i].TesterRequest, nil
	}
	return store.TesterRequest{}, store.ErrNotFound
}

func (f *fakeTesters) ListTesterRequests(_ context.Context, status string) ([]store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.TesterRequestView
	for _, r := range f.reqs {
		if r.Status == status || (status == "" && (r.Status == store.TesterRequested || r.Status == store.TesterAdded)) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeTesters) MarkTesterAcceptedByIdentity(_ context.Context, identityID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.open(identityID); i >= 0 {
		f.reqs[i].Status = store.TesterAccepted
	}
	f.accepted = append(f.accepted, identityID)
	return nil
}

func (f *fakeTesters) TestersAwaitingAcceptance(context.Context) ([]store.TesterRequestView, error) {
	return f.ListTesterRequests(context.Background(), store.TesterAdded)
}

func (f *fakeTesters) OpenTestersWithoutMeta(context.Context) ([]store.TesterRequestView, error) {
	return f.ListTesterRequests(context.Background(), "")
}

func (f *fakeTesters) TesterRequestByID(_ context.Context, id int64) (store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.ID == id {
			return r, nil
		}
	}
	return store.TesterRequestView{}, store.ErrNotFound
}

func (f *fakeTesters) MarkTesterAcceptedByOrg(_ context.Context, orgID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.reqs {
		if r.OrgID == orgID && (r.Status == store.TesterRequested || r.Status == store.TesterAdded) {
			f.reqs[i].Status = store.TesterAccepted
			f.accepted = append(f.accepted, r.IdentityID)
		}
	}
	return nil
}

// MarkTesterNotified segue a regra do store: avisa se nunca avisou ou se passou o intervalo.
func (f *fakeTesters) MarkTesterNotified(_ context.Context, id int64, within time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notified == nil {
		f.notified = map[int64]time.Time{}
	}
	if at, ok := f.notified[id]; ok && time.Since(at) < within {
		return false, nil
	}
	f.notified[id] = time.Now()
	return true, nil
}

// backdateNotified simula que o último aviso à equipe foi há muito tempo.
func (f *fakeTesters) backdateNotified() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.notified {
		f.notified[id] = time.Now().Add(-time.Hour)
	}
}

func (f *fakeTesters) MarkTesterAcceptNotified(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acceptNotified == nil {
		f.acceptNotified = map[int64]bool{}
	}
	if f.acceptNotified[id] {
		return store.ErrNotFound
	}
	f.acceptNotified[id] = true
	return nil
}

func (f *fakeTesters) MarkTesterReminded(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		if f.reqs[i].ID == id && f.reqs[i].RemindedAt == nil {
			now := time.Now()
			f.reqs[i].RemindedAt = &now
			f.reminded = append(f.reminded, id)
			return nil
		}
	}
	return store.ErrNotFound // já lembrado: é a trava do store real
}

func (f *fakeTesters) SyncAppTesters(_ context.Context, ids []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	first := f.known == nil
	if first {
		f.known = map[string]bool{}
	}
	var fresh []string
	for _, id := range ids {
		if !f.known[id] {
			f.known[id] = true
			if !first {
				fresh = append(fresh, id)
			}
		}
	}
	return fresh, nil
}

// markAdded simula o "Adicionei" do painel.
func (f *fakeTesters) markAdded(id int64, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		if f.reqs[i].ID == id {
			f.reqs[i].Status, f.reqs[i].AddedAt = store.TesterAdded, &at
		}
	}
}

func (f *fakeTesters) all() []store.TesterRequestView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.TesterRequestView(nil), f.reqs...)
}

// fakeParse reconhece os links de perfil dos testes (o parser real é de outro agente).
var fakeProfileRe = regexp.MustCompile(`https?://(?:www\.)?(facebook\.com|fb\.me)/(profile\.php\?id=(\d+)|share/\S+|([A-Za-z0-9.]+))`)

func fakeParse(text string) (meta.ProfileRef, bool) {
	m := fakeProfileRe.FindStringSubmatch(text)
	switch {
	case m == nil:
		return meta.ProfileRef{}, false
	case m[1] == "fb.me" || strings.HasPrefix(m[2], "share/"):
		return meta.ProfileRef{Kind: "share", Value: m[0], URL: m[0]}, true
	case m[3] != "":
		return meta.ProfileRef{Kind: "id", Value: m[3], URL: m[0]}, true
	default:
		return meta.ProfileRef{Kind: "username", Value: m[4], URL: m[0]}, true
	}
}

func newTesterHarness(t *testing.T) (*harness, *fakeTesters) {
	h := newMetaHarness(t, "owner")
	ft := &fakeTesters{}
	h.eng.Testers = ft
	h.eng.ParseProfile = fakeParse
	h.eng.MetaAppID = "999"
	h.eng.ResolveShare = func(context.Context, string) (string, error) { return "", errors.New("sem rede nos testes") }
	return h, ft
}

const profileLink = "https://www.facebook.com/profile.php?id=1000123"

func TestProfileLinkOpensTesterRequest(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.say("segue meu perfil " + profileLink)
	h.runJobs()
	if h.att.Calls() != 0 {
		t.Fatal("link de perfil não deve ir para a IA")
	}
	reqs := ft.all()
	if len(reqs) != 1 || reqs[0].RefKind != "id" || reqs[0].RefValue != "1000123" || reqs[0].ProfileURL != profileLink ||
		reqs[0].ConversationID != h.conv().ID || reqs[0].Status != store.TesterRequested {
		t.Fatalf("pedidos = %+v", reqs)
	}
	if !strings.Contains(h.sentText(), "Recebi! Já pedi pra equipe te liberar como testador") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	ops := h.wa.to(operatorChat)
	if len(ops) != 1 {
		t.Fatalf("equipe = %v", ops)
	}
	for _, want := range []string{"🧪 Cliente enviou ID, preciso que adicione como testador", "Ana", "Pizzaria Boa", phone,
		"1000123", profileLink, "https://developers.facebook.com/apps/999/roles/roles/"} {
		if !strings.Contains(ops[0], want) {
			t.Errorf("aviso sem %q: %q", want, ops[0])
		}
	}

	// mesmo link de novo: não incomoda a equipe outra vez
	h.say(profileLink)
	h.runJobs()
	if ops := h.wa.to(operatorChat); len(ops) != 1 {
		t.Fatalf("aviso repetido: %v", ops)
	}
	if len(ft.all()) != 1 || !strings.Contains(h.wa.last().Text, "já pedi") {
		t.Fatalf("repetição: %q", h.wa.last().Text)
	}
}

func TestProfileChangedAfterAddedNotifiesTeamAgain(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	ft.backdateNotified() // o limite é um aviso a cada 30 min
	h.say("na verdade é este: https://www.facebook.com/joao.silva")
	h.runJobs()
	ops := h.wa.to(operatorChat)
	if len(ops) != 2 || !strings.Contains(ops[1], "joao.silva") {
		t.Fatalf("equipe = %v", ops)
	}
	if r := ft.all(); len(r) != 1 || r[0].Status != store.TesterRequested || r[0].RefValue != "joao.silva" {
		t.Fatalf("pedido = %+v", r)
	}
}

func TestShareLinkIsResolved(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.eng.ResolveShare = func(_ context.Context, u string) (string, error) {
		if u != "https://www.facebook.com/share/1AbCd" {
			t.Errorf("url = %q", u)
		}
		return "https://www.facebook.com/joao.silva", nil
	}
	h.say("https://www.facebook.com/share/1AbCd")
	h.runJobs()
	if reqs := ft.all(); len(reqs) != 1 || reqs[0].RefKind != "username" || reqs[0].RefValue != "joao.silva" {
		t.Fatalf("pedidos = %+v", reqs)
	}
}

func TestShareLinkUnresolvedAsksAddressBar(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.say("https://fb.me/xyz123")
	h.runJobs()
	if len(ft.all()) != 0 || len(h.wa.to(operatorChat)) != 0 || h.att.Calls() != 0 {
		t.Fatal("link não resolvido não abre pedido")
	}
	if !strings.Contains(h.sentText(), "barra de endereço") {
		t.Fatalf("cliente = %q", h.sentText())
	}
}

func TestProfileLinkIgnoredWithoutMetaConnect(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.eng.MetaConnectBaseURL = ""
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Legal!"}`)
	h.say(profileLink)
	if len(ft.all()) != 0 || h.att.Calls() != 1 {
		t.Fatal("conexão desligada: segue para a IA")
	}
}

// Na aprovação, link de página com conversa em volta vai para a Atendente (parser real, sem contexto
// de testador) e a aprovação segue.
func TestProfileLinkKeepsApprovalFlow(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.eng.ParseProfile = nil // parser real
	code := h.reachApproval()
	calls := h.att.Calls()
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Legal!"}`)
	h.say("olha a página facebook.com/pizzariaboa, dá pra usar essas fotos no anúncio?")
	h.runJobs()
	if len(ft.all()) != 0 || h.att.Calls() != calls+1 || h.conv().State != store.StateAwaitingApproval {
		t.Fatalf("pedidos=%d ia=%d estado=%s", len(ft.all()), h.att.Calls()-calls, h.conv().State)
	}
	h.say("SIM " + code)
	if h.conv().State == store.StateAwaitingApproval {
		t.Fatal("aprovação não andou depois do link")
	}
}

func TestProfileLinkSilentWithHuman(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.att.Responses = append(h.att.Responses, `{"intent":"human","reply":"ok"}`)
	h.say("quero falar com uma pessoa")
	h.runJobs()
	h.say(profileLink)
	h.runJobs()
	if len(ft.all()) != 0 {
		t.Fatal("atendimento humano: o bot não age")
	}
}

func TestMetaLinkMentionsTesterHelp(t *testing.T) {
	h, _ := newTesterHarness(t)
	h.say("conectar meta")
	if !strings.Contains(h.sentText(), "Se aparecer que o app está indisponível, me manda o link do seu perfil do Facebook") {
		t.Fatalf("cliente = %q", h.sentText())
	}
}

func TestTesterAddedJobPayload(t *testing.T) {
	b, _ := json.Marshal(engine.TesterAddedJob{RequestID: 7})
	if string(b) != `{"request_id":7}` || engine.JobTesterAdded != "tester.added" || engine.JobTesterPoll != "tester.poll" {
		t.Fatalf("contrato do job = %s", b)
	}
}

// openRequest abre um pedido pelo WhatsApp e devolve o id.
func openRequest(h *harness, ft *fakeTesters) int64 {
	h.t.Helper()
	h.say(profileLink)
	h.runJobs()
	reqs := ft.all()
	if len(reqs) != 1 {
		h.t.Fatalf("pedidos = %d", len(reqs))
	}
	return reqs[0].ID
}

func enqueueAdded(h *harness, id int64) {
	h.t.Helper()
	h.must(h.st.Enqueue(ctx, store.JobSpec{Kind: engine.JobTesterAdded, Payload: map[string]int64{"request_id": id}}))
	h.runJobs()
}

func TestTesterAddedSendsInviteAndLink(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	enqueueAdded(h, id)
	last := h.wa.last().Text
	if !strings.Contains(h.sentText(), "Te adicionei como testador! ✅") || !strings.Contains(h.sentText(), "https://developers.facebook.com/requests") ||
		!strings.Contains(last, metaBase+"/") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
}

// "Reenviar aviso" no painel enfileira o mesmo job para um pedido que continua added.
func TestTesterAddedResendWhileAdded(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	enqueueAdded(h, id)
	h.now = h.now.Add(15 * time.Minute)
	enqueueAdded(h, id)
	if n := strings.Count(h.sentText(), "Te adicionei como testador!"); n != 2 {
		t.Fatalf("avisos = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 2 {
		t.Fatalf("links = %d", n)
	}
	h.must(ft.MarkTesterAcceptedByIdentity(ctx, h.conv().IdentityID))
	n := h.wa.count()
	enqueueAdded(h, id)
	if h.wa.count() != n {
		t.Fatal("pedido aceito não recebe reenvio")
	}
}

func TestTesterAddedIgnoresClosedRequest(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	n := h.wa.count()
	enqueueAdded(h, id)    // ainda requested
	enqueueAdded(h, 12345) // não existe
	if h.wa.count() != n || h.pendingJobs(engine.JobTesterAdded) != 0 {
		t.Fatal("pedido fora de added não manda nada")
	}
}

func TestTesterAddedOutsideWindowAlertsTeam(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	_, err := h.pool.Exec(ctx, `UPDATE messages SET created_at = $1::timestamptz - interval '30 hours'`, h.now)
	h.must(err)
	ft.markAdded(id, h.now)
	before := len(h.wa.to(chatID))
	enqueueAdded(h, id)
	if len(h.wa.to(chatID)) != before || h.count(`SELECT count(*) FROM meta_connect_links`) != 0 {
		t.Fatal("fora da janela não manda texto livre ao cliente")
	}
	if len(wa.tpls) != 1 || wa.tpls[0].Name != engine.TplTeamAlert || !strings.Contains(strings.Join(wa.tpls[0].Params, " "), "janela") {
		t.Fatalf("equipe = %+v", wa.tpls)
	}
}

// runPoll roda o job do testador já agendado (o próximo fica para daqui a 5 min).
func (h *harness) runPoll() {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE kind = $1 AND status = 'pending'`, engine.JobTesterPoll)
	h.must(err)
	h.runJobs()
}

func TestTesterPollSendsLinkWhenNewTesterAppears(t *testing.T) {
	h, ft := newTesterHarness(t)
	ids := []string{"a"}
	h.eng.AppTesters = func(context.Context) ([]string, error) { return ids, nil }
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.must(h.eng.EnsureTesterPoll(ctx))
	if n := h.pendingJobs(engine.JobTesterPoll); n != 1 {
		t.Fatalf("polls agendados = %d", n)
	}
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	h.runPoll() // primeira leitura só guarda os conhecidos
	if strings.Contains(h.sentText(), "Vi que você aceitou") {
		t.Fatal("primeira leitura não avisa")
	}
	ids = []string{"a", "b"}
	h.runPoll()
	if !strings.Contains(h.sentText(), "Vi que você aceitou o convite ✅ Toca aqui pra conectar sua conta:") ||
		!strings.Contains(h.wa.last().Text, metaBase+"/") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	if n := h.pendingJobs(engine.JobTesterPoll); n != 1 {
		t.Fatalf("próximo poll = %d", n)
	}
}

func TestTesterPollAmbiguousDoesNotSend(t *testing.T) {
	h, ft := newTesterHarness(t)
	ids := []string{"a"}
	h.eng.AppTesters = func(context.Context) ([]string, error) { return ids, nil }
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	ft.reqs = append(ft.reqs, store.TesterRequestView{TesterRequest: store.TesterRequest{ID: 99, IdentityID: 999, Status: store.TesterAdded, AddedAt: &h.now}})
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	ids = []string{"a", "b"}
	h.runPoll()
	if strings.Contains(h.sentText(), "Vi que você aceitou") {
		t.Fatal("dois pedidos esperando: não dá pra saber quem aceitou")
	}
}

func TestTesterPollRemindsOnceAfter24h(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.eng.AppTesters = func(context.Context) ([]string, error) { return nil, nil }
	id := openRequest(h, ft)
	ft.markAdded(id, h.now.Add(-25*time.Hour))
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	if !strings.Contains(h.sentText(), "aceitar o convite") || !strings.Contains(h.wa.last().Text, metaBase+"/") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	ops := h.wa.to(operatorChat)
	if len(ops) != 2 || !strings.Contains(ops[1], "ainda não aceitou") {
		t.Fatalf("equipe = %v", ops)
	}
	if len(ft.reminded) != 1 || ft.reminded[0] != id {
		t.Fatalf("lembrados = %v", ft.reminded)
	}
	n := h.wa.count()
	h.runPoll()
	if h.wa.count() != n {
		t.Fatal("lembrete só uma vez")
	}
}

func TestTesterPollSurvivesMetaError(t *testing.T) {
	h, _ := newTesterHarness(t)
	h.eng.AppTesters = func(context.Context) ([]string, error) { return nil, errors.New("meta fora") }
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	if n := h.pendingJobs(engine.JobTesterPoll); n != 1 {
		t.Fatalf("poll parou depois do erro: %d", n)
	}
}

func TestTesterPollOffWithoutAppTesters(t *testing.T) {
	h, _ := newTesterHarness(t)
	h.must(h.eng.EnsureTesterPoll(ctx))
	if h.pendingJobs(engine.JobTesterPoll) != 0 {
		t.Fatal("sem leitura da Meta não agenda")
	}
}

func TestMetaConnectedMarksTesterAccepted(t *testing.T) {
	h, ft := newTesterHarness(t)
	id := openRequest(h, ft)
	ft.markAdded(id, h.now)
	conv := h.conv()
	h.must(h.st.Enqueue(ctx, engine.MetaConnectedJob(conv.ID, chatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", AccountStatus: 1, PageName: "Loja", PageWhatsApp: true})))
	for range 4 { // balões com "digitando…": adianta as esperas
		h.runJobs()
		_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE status = 'pending'`)
		h.must(err)
	}
	if r := ft.all()[0]; r.Status != store.TesterAccepted || len(ft.accepted) != 1 || ft.accepted[0] != conv.IdentityID {
		t.Fatalf("pedido = %+v aceitos = %v", r, ft.accepted)
	}
	if !strings.Contains(h.sentText(), "Conectado ✅") {
		t.Fatalf("cliente = %q", h.sentText())
	}
}
