// Comando seedadmin: preenche um banco de DESENVOLVIMENTO com clientes, conversas, projetos,
// assinaturas, pagamentos e uso de IA para ver o painel com dados. Nunca rode em produção.
//
//	DATABASE_URL=postgres://postgres:pg@localhost:55432/gestor_dev?sslmode=disable go run ./scripts/seedadmin
//
// Recusa banco que já tenha clientes (use -force para somar mais dados) e qualquer URL do Supabase.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
)

type client struct {
	person, business, segment, city, uf string
	stage                               string // pending, registered, collecting, approval, creatives, ready, blocked
	plan, sub                           string // plano e situação da assinatura
	meta                                bool
	idleHours                           float64 // desde a última mensagem
	human                               string  // "", "pediu" ou e-mail de quem assumiu
}

var clients = []client{
	{"Ana Beatriz Souza", "Clínica Bella Pele", "Clínica de estética", "São Paulo", "SP", "ready", "pro", "active", true, 3, ""},
	{"Carlos Mendes", "Pizzaria Forno Bom", "Restaurante", "Campinas", "SP", "creatives", "starter", "active", true, 1.5, "marina@meuanuncio.io"},
	{"Juliana Rocha", "Ju Moda Feminina", "Loja de roupas", "Belo Horizonte", "MG", "approval", "starter", "active", false, 5, ""},
	{"Rafael Lima", "Barbearia do Rafa", "Barbearia", "Curitiba", "PR", "collecting", "free", "active", false, 52, ""},
	{"Patrícia Alves", "Alves Imóveis", "Imobiliária", "Santos", "SP", "ready", "pro", "active", true, 9, ""},
	{"Marcos Oliveira", "Pet Shop Amigo Fiel", "Pet shop", "Ribeirão Preto", "SP", "collecting", "free", "active", false, 2, "pediu"},
	{"Fernanda Costa", "Academia Corpo em Forma", "Academia", "Recife", "PE", "ready", "starter", "active", true, 20, ""},
	{"Bruno Carvalho", "Doce Encanto", "Doceria", "Porto Alegre", "RS", "registered", "free", "active", false, 30, ""},
	{"Larissa Gomes", "Auto Center Gomes", "Oficina mecânica", "Goiânia", "GO", "pending", "", "", false, 2, ""},
	{"Thiago Martins", "English Now", "Escola de idiomas", "Salvador", "BA", "approval", "starter", "past_due", false, 28, ""},
	{"Camila Ribeiro", "Burger da Praça", "Hamburgueria", "Florianópolis", "SC", "creatives", "pro", "active", true, 0.4, ""},
	{"Diego Ferreira", "Sorriso Perfeito", "Odontologia", "Fortaleza", "CE", "collecting", "free", "active", false, 76, ""},
	{"Aline Barbosa", "Studio Aline Nails", "Salão de beleza", "Campinas", "SP", "pending", "", "", false, 30, ""},
	{"Rodrigo Dias", "Dias Advocacia", "Advocacia", "Brasília", "DF", "registered", "free", "active", false, 3, ""},
	{"Vanessa Teixeira", "Floricultura Jardim", "Floricultura", "Niterói", "RJ", "collecting", "starter", "trialing", false, 6, ""},
	{"Gustavo Pereira", "GP Climatização", "Serviços", "Manaus", "AM", "ready", "starter", "active", true, 40, ""},
	{"Renata Cardoso", "Renata Doces Finos", "Doceria", "São Paulo", "SP", "approval", "free", "active", false, 1, ""},
	{"Felipe Araújo", "Ótica Visão Clara", "Ótica", "Uberlândia", "MG", "pending", "", "", false, 49, ""},
	{"Priscila Nunes", "Nunes Fisioterapia", "Fisioterapia", "Londrina", "PR", "collecting", "free", "active", false, 12, ""},
	{"Leonardo Moura", "Moura Materiais", "Material de construção", "Joinville", "SC", "blocked", "free", "canceled", false, 200, ""},
	{"Bianca Freitas", "Bia Pet Grooming", "Pet shop", "Sorocaba", "SP", "creatives", "starter", "active", false, 8, ""},
	{"André Ramos", "Ramos Contabilidade", "Contabilidade", "Vitória", "ES", "pending", "", "", false, 0.2, ""},
	{"Tatiane Lopes", "Tati Confeitaria", "Doceria", "Natal", "RN", "registered", "free", "active", false, 60, ""},
	{"Eduardo Pinto", "Pinto Odonto", "Odontologia", "Belém", "PA", "collecting", "pro", "active", false, 26, ""},
}

var conversation = [][2]string{
	{"in", "Oi! Quero anunciar meu negócio no Instagram"},
	{"out", "Oi! Que bom te ver por aqui 😊 Me conta: o que você quer anunciar primeiro?"},
	{"in", "Quero divulgar a promoção de outubro"},
	{"out", "Boa! Pra quem é essa promoção e em qual região você atende?"},
	{"in", "Mulheres de 25 a 45 anos, aqui na região central"},
	{"out", "Perfeito. Quanto você quer investir por dia? Dá pra começar com R$ 20."},
	{"in", "Pode ser R$ 30 por dia"},
	{"out", "Fechado! Vou montar a proposta com o especialista e já te mando."},
}

func main() {
	force := flag.Bool("force", false, "soma dados mesmo com clientes no banco")
	flag.Parse()
	url := os.Getenv("DATABASE_URL")
	if url == "" || strings.Contains(url, "supabase") {
		fmt.Println("seedadmin: defina DATABASE_URL de um banco LOCAL de desenvolvimento (Supabase é recusado)")
		os.Exit(2)
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, url)
	check(err)
	defer pool.Close()
	check(db.Migrate(ctx, pool))
	var n int
	check(pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&n))
	if n > 0 && !*force {
		fmt.Printf("seedadmin: o banco já tem %d clientes; use -force para somar mais\n", n)
		os.Exit(1)
	}
	s := &seeder{pool: pool, ctx: ctx, rnd: rand.New(rand.NewPCG(7, 2026)), now: time.Now()}
	s.team()
	for i, c := range clients {
		s.client(i, c)
	}
	s.billing()
	s.usage()
	s.ops()
	fmt.Printf("seedadmin: %d clientes, equipe dev@meuanuncio.io (admin) e marina@meuanuncio.io (staff)\n", len(clients))
}

type seeder struct {
	pool *pgxpool.Pool
	ctx  context.Context
	rnd  *rand.Rand
	now  time.Time
	subs []sub
}

type sub struct {
	id      int64
	price   int
	started time.Time
	status  string
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func (s *seeder) exec(sql string, args ...any) {
	_, err := s.pool.Exec(s.ctx, sql, args...)
	check(err)
}

func (s *seeder) id(sql string, args ...any) int64 {
	var id int64
	check(s.pool.QueryRow(s.ctx, sql, args...).Scan(&id))
	return id
}

func (s *seeder) ago(h float64) time.Time { return s.now.Add(-time.Duration(h * float64(time.Hour))) }

func (s *seeder) team() {
	s.exec(`INSERT INTO team_members (email, name, role, added_by, last_login_at) VALUES
		('dev@meuanuncio.io', 'Guilherme', 'admin', 'seedadmin', now() - interval '2 hours'),
		('marina@meuanuncio.io', 'Marina', 'staff', 'dev@meuanuncio.io', now() - interval '20 minutes'),
		('pedro@meuanuncio.io', 'Pedro', 'staff', 'dev@meuanuncio.io', NULL)
		ON CONFLICT (email) DO NOTHING`)
}

func (s *seeder) client(i int, c client) {
	created := s.ago(c.idleHours + 24*float64(2+i%9))
	phone := fmt.Sprintf("55119%08d", 71000000+i*7919)
	reg := "complete"
	if c.stage == "pending" {
		reg = "pending"
	}
	status := "active"
	if c.stage == "blocked" {
		status = "blocked"
	}
	profile, _ := json.Marshal(map[string]string{"business_name": c.business, "city": c.city, "offer": "Promoção de outubro", "audience": "Mulheres de 25 a 45 anos"})
	org := s.id(`INSERT INTO organizations (name, trade_name, niche, segment, city, uf, max_daily_budget_brl, status, registration_status,
			registered_at, signup_source, created_at, profile, cnpj, website, instagram, monthly_budget_range, ads_experience)
		VALUES ($1, $1, $2, $2, $3, $4, 100, $5, $6, CASE WHEN $6 = 'complete' THEN $7::timestamptz + interval '15 minutes' END, 'auto', $7, $8,
			CASE WHEN $9 % 3 = 0 THEN NULL ELSE lpad(($9 * 1234567)::text, 14, '1') END,
			CASE WHEN $9 % 2 = 0 THEN 'https://' || lower(replace($1, ' ', '')) || '.com.br' END, '@' || lower(replace($1, ' ', '')),
			'R$ 500 a R$ 1.000', 'Já anunciei sozinho') RETURNING id`,
		c.business, c.segment, c.city, c.uf, status, reg, created, string(profile), i+1)
	email := strings.ToLower(strings.Fields(c.person)[0]) + "@gmail.com"
	mem := s.id(`INSERT INTO members (organization_id, name, full_name, role, email, email_verified_at, created_at)
		VALUES ($1, $2, $2, 'owner', CASE WHEN $3 = 'complete' THEN $4 END, CASE WHEN $3 = 'complete' THEN $5::timestamptz END, $5) RETURNING id`,
		org, c.person, reg, email, created)
	ident := s.id(`INSERT INTO whatsapp_identities (provider, phone_e164, member_id, created_at) VALUES ('waha', $1, $2, $3) RETURNING id`, phone, mem, created)
	s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'system', 'auto_signup', $2, $3)`,
		org, fmt.Sprintf(`{"identity_id":%d}`, ident), created)
	if reg == "complete" {
		for _, k := range []string{"terms", "privacy"} {
			s.exec(`INSERT INTO consents (member_id, kind, version, accepted_at) VALUES ($1, $2, '2026-09-23', $3)`, mem, k, created.Add(15*time.Minute))
		}
		s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'member', 'registration_completed', '{}', $2)`,
			org, created.Add(15*time.Minute))
	} else {
		s.exec(`INSERT INTO signup_links (identity_id, token_hash, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
			ident, fmt.Sprintf("seed-%d-%d", org, s.rnd.Int64()), created.Add(24*time.Hour), created)
		s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'system', 'signup_link_sent', '{}', $2)`, org, created)
	}

	state := map[string]string{"pending": "idle", "registered": "onboarding", "collecting": "collecting", "approval": "awaiting_approval",
		"creatives": "awaiting_creatives", "ready": "idle", "blocked": "idle"}[c.stage]
	var humanBy, prev any
	var humanSince any
	if c.human != "" {
		prev = state
		state = "needs_human"
		if c.human != "pediu" {
			humanBy = c.human
			humanSince = s.ago(c.idleHours + 0.3)
		} else {
			prev = nil
		}
	}
	conv := s.id(`INSERT INTO conversations (identity_id, chat_id, state, updated_at, human_by, human_since, human_prev_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, ident, phone+"@c.us", state, s.ago(c.idleHours), humanBy, humanSince, prev)
	msgs := conversation
	switch c.stage {
	case "pending":
		msgs = [][2]string{{"in", "Oi, vi o anúncio de vocês"}, {"out", "Oi! Pra gente começar, faz seu cadastro rapidinho por aqui:\n\nhttps://meuanuncio.io/c/[link]\n\nLeva uns 2 minutos."}}
	case "registered":
		msgs = conversation[:2]
	}
	if c.human == "pediu" {
		msgs = append(append([][2]string{}, msgs...), [2]string{"in", "Prefiro falar com uma pessoa, pode ser?"},
			[2]string{"out", "Beleza! Vou chamar uma pessoa da nossa equipe pra continuar com você."})
	}
	if c.human != "" && c.human != "pediu" {
		msgs = append(append([][2]string{}, msgs...), [2]string{"in", "Pode subir com essas fotos?"})
	}
	step := 0.35
	for k, m := range msgs {
		at := s.ago(c.idleHours + float64(len(msgs)-1-k)*step)
		kind := "text"
		s.exec(`INSERT INTO messages (conversation_id, direction, kind, body, created_at) VALUES ($1, $2, $3, $4, $5)`, conv, m[0], kind, m[1], at)
	}
	if c.human != "" && c.human != "pediu" {
		s.exec(`INSERT INTO messages (conversation_id, direction, kind, body, author, created_at) VALUES ($1, 'out', 'staff', $2, 'Marina', $3)`,
			conv, "Oi Carlos! Aqui é a Marina, da equipe. Vou conferir as fotos com você agora.", s.ago(c.idleHours-0.1))
		s.exec(`INSERT INTO admin_audit_log (actor_email, action, target_type, target_id, target_label, data, created_at)
			VALUES ($1, 'conversation_takeover', 'conversation', $2, $3, '{"from":"awaiting_creatives"}', $4)`,
			c.human, fmt.Sprint(conv), c.business, s.ago(c.idleHours+0.3))
	}
	if c.human == "pediu" {
		s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'system', 'escalated_human', $2, $3)`,
			org, fmt.Sprintf(`{"conversation_id":%d,"reason":"cliente pediu atendimento humano"}`, conv), s.ago(c.idleHours))
	}
	s.exec(`UPDATE conversations SET replied_through = (SELECT coalesce(max(id), 0) FROM messages WHERE conversation_id = $1 AND direction = 'in') WHERE id = $1`, conv)

	if c.plan != "" {
		started := created.Add(20 * time.Minute)
		id := s.id(`INSERT INTO subscriptions (organization_id, plan_code, status, started_at, updated_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			org, c.plan, c.sub, started, s.ago(c.idleHours))
		price := map[string]int{"free": 0, "starter": 9700, "pro": 29700}[c.plan]
		s.subs = append(s.subs, sub{id: id, price: price, started: started, status: c.sub})
	}

	projStage := map[string]string{"collecting": "collecting", "approval": "awaiting_approval", "creatives": "awaiting_creatives", "ready": "ready"}[c.stage]
	if projStage == "" {
		return
	}
	fields := `{"offer":"Promoção de outubro","audience":"Mulheres de 25 a 45 anos","location":"` + c.city + `","daily_budget_brl":30}`
	proj := s.id(`INSERT INTO projects (organization_id, name, stage, fields, created_at, updated_at) VALUES ($1, 'Promo de outubro', $2, $3, $4, $5) RETURNING id`,
		org, projStage, fields, created.Add(time.Hour), s.ago(c.idleHours))
	s.exec(`UPDATE conversations SET active_project_id = $2, fields = $3 WHERE id = $1`, conv, proj, fields)
	if projStage == "collecting" {
		return
	}
	draftStatus, approvedBy, approvedAt := "proposed", any(nil), any(nil)
	if projStage != "awaiting_approval" {
		draftStatus, approvedBy, approvedAt = "approved", mem, s.ago(c.idleHours+2)
	}
	proposal := fmt.Sprintf(`{"objective":"OUTCOME_ENGAGEMENT","daily_budget_brl":%d,"duration_days":7,"audience":{"age_min":25,"age_max":45},"copies":[{"headline":"Promo de outubro","text":"Chama no WhatsApp","cta":"WHATSAPP_MESSAGE"}],"rationale":"Público local.","risks":[]}`, 20+i%4*10)
	draft := s.id(`INSERT INTO campaign_drafts (organization_id, conversation_id, project_id, briefing, proposal, proposal_hash, status, approved_by, approved_at, created_at)
		VALUES ($1, $2, $3, '{}', $4, 'seed', $5, $6, $7, $8) RETURNING id`, org, conv, proj, proposal, draftStatus, approvedBy, approvedAt, s.ago(c.idleHours+3))
	s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'manager', 'proposal_created', $2, $3)`,
		org, fmt.Sprintf(`{"draft_id":%d}`, draft), s.ago(c.idleHours+3))
	if draftStatus == "approved" {
		s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, $2, 'draft_approved', $3, $4)`,
			org, fmt.Sprintf("member:%d", mem), fmt.Sprintf(`{"draft_id":%d}`, draft), approvedAt)
		for k := 0; k < 1+i%3; k++ {
			s.exec(`INSERT INTO creatives (organization_id, draft_id, project_id, kind, storage_path, mime, size_bytes, width, height, format, placement, created_at)
				VALUES ($1, $2, $3, 'image', $4, 'image/jpeg', 180000, 1080, 1350, 'portrait', 'feed', $5)`,
				org, draft, proj, fmt.Sprintf("seed/%d/%d.jpg", org, k), s.ago(c.idleHours+1))
		}
	}
	if c.meta {
		link := s.id(`INSERT INTO meta_connect_links (identity_id, token_hash, expires_at, used_at, created_at) VALUES ($1, $2, $3, $3, $3) RETURNING id`,
			ident, fmt.Sprintf("seed-meta-%d", org), s.ago(c.idleHours+5))
		s.exec(`INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, business_id, ad_account_id, ad_account_name, currency,
				timezone_name, account_status, page_id, page_name, instagram_username, page_whatsapp, status, connected_at)
			VALUES ($1, $2, $3, $4, 'B1', $5, $6, 'BRL', 'America/Sao_Paulo', 1, 'P1', $7, $8, $9, 'active', $10)`,
			org, ident, link, fmt.Sprintf("U%d", org), fmt.Sprintf("act_%d", 1556567192+i*7919), "Conta "+c.business, c.business,
			strings.ToLower(strings.ReplaceAll(c.business, " ", "")), i%2 == 0, s.ago(c.idleHours+5))
		s.exec(`INSERT INTO audit_events (organization_id, actor, action, data, created_at) VALUES ($1, 'member', 'meta_connected', '{}', $2)`, org, s.ago(c.idleHours+5))
	}
}

// billing: mensalidades pagas desde o início de cada assinatura paga, a última em aberto para quem está em atraso.
func (s *seeder) billing() {
	methods := []string{"pix", "pix", "boleto", "card"}
	for k, sb := range s.subs {
		if sb.price == 0 || sb.status == "trialing" {
			continue
		}
		start := sb.started.AddDate(0, -k%5, 0) // algumas assinaturas mais antigas, para o gráfico de 6 meses
		s.exec(`UPDATE subscriptions SET started_at = $2 WHERE id = $1`, sb.id, start)
		for at := start; at.Before(s.now); at = at.AddDate(0, 1, 0) {
			status, paidAt := "paid", any(at.Add(2*time.Hour))
			last := at.AddDate(0, 1, 0).After(s.now)
			if last && sb.status == "past_due" {
				status, paidAt = "pending", nil
			}
			s.exec(`INSERT INTO payments (subscription_id, amount_cents, method, status, provider, paid_at, created_at, recorded_by)
				VALUES ($1, $2, $3, $4, 'manual', $5, $6, 'dev@meuanuncio.io')`, sb.id, sb.price, methods[k%len(methods)], status, paidAt, at)
		}
	}
	if len(s.subs) > 2 {
		s.exec(`INSERT INTO payments (subscription_id, amount_cents, method, status, provider, created_at) VALUES ($1, 9700, 'card', 'failed', 'manual', now() - interval '3 days')`, s.subs[2].id)
	}
}

// usage: custo de IA diário dos últimos seis meses, crescendo com a base.
func (s *seeder) usage() {
	for d := 180; d >= 0; d-- {
		calls := 4 + (180-d)/12 + s.rnd.IntN(6)
		for k := 0; k < calls; k++ {
			agent := []string{"attendant", "attendant", "attendant", "manager", "transcriber", "tts"}[s.rnd.IntN(6)]
			cost := 0.002 + s.rnd.Float64()*0.02
			if agent == "manager" {
				cost *= 4
			}
			s.exec(`INSERT INTO llm_usage (organization_id, agent, model, input_tokens, output_tokens, cost_usd, created_at)
				SELECT id, $1, 'seed', 1200, 300, $2, now() - ($3 || ' days')::interval - ($4 || ' minutes')::interval FROM organizations ORDER BY random() LIMIT 1`,
				agent, cost, fmt.Sprint(d), fmt.Sprint(s.rnd.IntN(1440)))
		}
	}
}

// ops: estado do WhatsApp, envios que falharam e ações da equipe.
func (s *seeder) ops() {
	s.exec(`INSERT INTO whatsapp_status (provider, connected, code, reason, updated_at) VALUES ('waha', true, 200, NULL, now() - interval '3 hours')
		ON CONFLICT (provider) DO NOTHING`)
	s.exec(`INSERT INTO outbox_jobs (kind, payload, status, attempts, last_error, created_at, updated_at) VALUES
		('whatsapp.send', '{"conversation_id":0,"chat_id":"x","text":"Resumo das últimas 24 h"}', 'failed', 8, 'waha: sendText status 500 (sessão FAILED)', now() - interval '5 hours', now() - interval '4 hours'),
		('whatsapp.send', '{"conversation_id":0,"chat_id":"x","text":"Lembrete de cadastro"}', 'failed', 8, 'waha: sendText status 422', now() - interval '7 hours', now() - interval '6 hours')`)
	for _, a := range []struct{ who, action, label string }{
		{"dev@meuanuncio.io", "login", ""}, {"marina@meuanuncio.io", "login", ""},
		{"dev@meuanuncio.io", "payment_record", "Clínica Bella Pele"}, {"marina@meuanuncio.io", "client_signup_link", "Studio Aline Nails"},
		{"dev@meuanuncio.io", "client_block", "Moura Materiais"}, {"dev@meuanuncio.io", "team_add", "pedro@meuanuncio.io"},
	} {
		s.exec(`INSERT INTO admin_audit_log (actor_email, action, target_type, target_label, created_at) VALUES ($1, $2, '', $3, now() - ($4 || ' minutes')::interval)`,
			a.who, a.action, a.label, fmt.Sprint(30+s.rnd.IntN(3000)))
	}
}
