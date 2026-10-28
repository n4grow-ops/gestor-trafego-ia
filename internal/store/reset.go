package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// ResetStep é quantas linhas uma tabela perdeu (ou perderia, na simulação).
type ResetStep struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// ResetReport é o resultado de zerar um contato de teste.
type ResetReport struct {
	OrgIDs  []int64     `json:"org_ids"`
	Phones  []string    `json:"phones"`
	Steps   []ResetStep `json:"steps"`
	Applied bool        `json:"applied"`
}

// Rows devolve as linhas da tabela no relatório.
func (r ResetReport) Rows(table string) int64 {
	for _, s := range r.Steps {
		if s.Table == table {
			return s.Rows
		}
	}
	return 0
}

// ErrRealClient: a organização tem pagamento registrado ou cadastro completo com plano pago. Zerar é
// só para conta de teste.
var ErrRealClient = errors.New("store: cliente real (pagamento ou plano pago) não pode ser zerado")

// realClient diz se alguma das organizações é cliente de verdade: qualquer pagamento registrado, ou
// cadastro completo com assinatura de plano pago.
func realClient(ctx context.Context, tx pgx.Tx, orgIDs []int64) (bool, error) {
	var real bool
	err := tx.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM payments p JOIN subscriptions s ON s.id = p.subscription_id WHERE s.organization_id = ANY($1))
		OR EXISTS (SELECT 1 FROM organizations o JOIN subscriptions s ON s.organization_id = o.id JOIN plans pl ON pl.code = s.plan_code
			WHERE o.id = ANY($1) AND o.registration_status = 'complete' AND pl.price_cents > 0)`, orgIDs).Scan(&real)
	return real, err
}

// ResetOrganization apaga o cliente inteiro (organização, telefones, conversa, projetos, assinatura,
// pagamentos, conexão Meta, uso de IA e eventos) para o número voltar como contato novo e refazer o
// cadastro. Vale para qualquer cliente (o administrador decide no painel). Sem apply, só simula. Ao
// aplicar, grava audit (com os telefones e as linhas apagadas) no registro do painel na mesma
// transação: sem registro, nada é apagado. Arquivos de anúncios no Storage e campanhas na Meta não
// são apagados.
func (s *Store) ResetOrganization(ctx context.Context, orgID int64, apply bool, audit AdminAction) (ResetReport, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResetReport{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE)`, orgID).Scan(&exists); err != nil {
		return ResetReport{}, err
	}
	if !exists {
		return ResetReport{}, ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT w.phone_e164 FROM whatsapp_identities w JOIN members m ON m.id = w.member_id
		WHERE m.organization_id = $1 ORDER BY w.id`, orgID)
	if err != nil {
		return ResetReport{}, err
	}
	phones, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return ResetReport{}, err
	}
	rep, err := resetOrganizations(ctx, tx, []int64{orgID}, phones)
	if err != nil {
		return rep, err
	}
	if !apply {
		return rep, nil
	}
	audit.Data = map[string]any{"phones": rep.Phones, "steps": rep.Steps}
	if err := logAdminAction(ctx, tx, audit); err != nil {
		return rep, fmt.Errorf("store: zerar sem registro: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, err
	}
	rep.Applied = true
	return rep, nil
}

// ResetContacts é o mesmo para scripts/resetcontato: acha as organizações pelos telefones.
// others > 0 quando essas organizações têm outros telefones (não apaga nada). Também recusa
// cliente real (ErrRealClient).
func (s *Store) ResetContacts(ctx context.Context, phones []string, apply bool) (rep ResetReport, others int, err error) {
	return s.ResetContactsWith(ctx, phones, apply, false)
}

// paidClient diz se alguma das organizações tem pagamento registrado (cliente que já pagou).
func paidClient(ctx context.Context, tx pgx.Tx, orgIDs []int64) (bool, error) {
	var paid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments p JOIN subscriptions s ON s.id = p.subscription_id
		WHERE s.organization_id = ANY($1))`, orgIDs).Scan(&paid)
	return paid, err
}

// ResetContactsWith é o ResetContacts; unpaidOK zera também cadastro completo em plano pago que nunca
// pagou (conta de teste da equipe). Com qualquer pagamento registrado continua recusando.
func (s *Store) ResetContactsWith(ctx context.Context, phones []string, apply, unpaidOK bool) (rep ResetReport, others int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rep, 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT DISTINCT m.organization_id FROM whatsapp_identities w JOIN members m ON m.id = w.member_id
		WHERE w.phone_e164 = ANY($1)`, phones)
	if err != nil {
		return rep, 0, err
	}
	orgIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return rep, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM whatsapp_identities w JOIN members m ON m.id = w.member_id
		WHERE m.organization_id = ANY($1) AND NOT (w.phone_e164 = ANY($2))`, orgIDs, phones).Scan(&others); err != nil {
		return rep, 0, err
	}
	if others > 0 {
		return ResetReport{OrgIDs: orgIDs, Phones: phones}, others, nil
	}
	check := realClient
	if unpaidOK {
		check = paidClient
	}
	if real, err := check(ctx, tx, orgIDs); err != nil {
		return rep, 0, err
	} else if real {
		return ResetReport{OrgIDs: orgIDs, Phones: phones}, 0, ErrRealClient
	}
	rep, err = resetOrganizations(ctx, tx, orgIDs, phones)
	if err != nil || !apply {
		return rep, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, 0, err
	}
	rep.Applied = true
	return rep, 0, nil
}

func resetOrganizations(ctx context.Context, tx pgx.Tx, orgIDs []int64, phones []string) (ResetReport, error) {
	rep := ResetReport{OrgIDs: orgIDs, Phones: phones}
	idents := `SELECT w.id FROM whatsapp_identities w JOIN members m ON m.id = w.member_id WHERE m.organization_id = ANY($1)`
	conv := `SELECT c.id FROM conversations c WHERE c.identity_id IN (` + idents + `)`
	steps := []struct{ table, sql string }{
		{"payments", `DELETE FROM payments WHERE subscription_id IN (SELECT id FROM subscriptions WHERE organization_id = ANY($1))`},
		{"subscriptions", `DELETE FROM subscriptions WHERE organization_id = ANY($1)`},
		{"consents", `DELETE FROM consents WHERE member_id IN (SELECT id FROM members WHERE organization_id = ANY($1))`},
		{"meta_publications", `DELETE FROM meta_publications WHERE organization_id = ANY($1)`},
		{"creatives", `DELETE FROM creatives WHERE organization_id = ANY($1)`},
		{"approval_codes", `DELETE FROM approval_codes WHERE draft_id IN (SELECT id FROM campaign_drafts WHERE organization_id = ANY($1))`},
		{"campaign_drafts", `DELETE FROM campaign_drafts WHERE organization_id = ANY($1)`},
		{"outbox_jobs", `DELETE FROM outbox_jobs WHERE (payload->>'conversation_id') ~ '^[0-9]+$' AND (payload->>'conversation_id')::bigint IN (` + conv + `)
			OR (payload->>'identity_id') ~ '^[0-9]+$' AND (payload->>'identity_id')::bigint IN (` + idents + `)`},
		{"tester_requests", `DELETE FROM tester_requests WHERE organization_id = ANY($1) OR identity_id IN (` + idents + `)`},
		{"messages", `DELETE FROM messages WHERE conversation_id IN (` + conv + `)`},
		{"conversations", `DELETE FROM conversations WHERE id IN (` + conv + `)`},
		{"projects", `DELETE FROM projects WHERE organization_id = ANY($1)`},
		{"shield_account_state", `DELETE FROM shield_account_state WHERE organization_id = ANY($1)`},
		{"meta_connections", `DELETE FROM meta_connections WHERE organization_id = ANY($1) OR connected_by IN (` + idents + `)`},
		{"meta_connect_links", `DELETE FROM meta_connect_links WHERE identity_id IN (` + idents + `)`},
		{"signup_links", `DELETE FROM signup_links WHERE identity_id IN (` + idents + `)`},
		{"whatsapp_identities", `DELETE FROM whatsapp_identities WHERE id IN (` + idents + `)`},
		{"members", `DELETE FROM members WHERE organization_id = ANY($1)`},
		{"llm_usage", `DELETE FROM llm_usage WHERE organization_id = ANY($1)`},
		{"audit_events", `DELETE FROM audit_events WHERE organization_id = ANY($1)`},
		{"organizations", `DELETE FROM organizations WHERE id = ANY($1)`},
	}
	for _, st := range steps {
		tag, err := tx.Exec(ctx, st.sql, orgIDs)
		if err != nil {
			return rep, fmt.Errorf("store: zerar %s: %w", st.table, err)
		}
		rep.Steps = append(rep.Steps, ResetStep{Table: st.table, Rows: tag.RowsAffected()})
	}
	var inbox int64
	for _, p := range phones {
		tag, err := tx.Exec(ctx, `DELETE FROM inbox_events WHERE payload::text LIKE '%' || $1 || '%'`, p)
		if err != nil {
			return rep, fmt.Errorf("store: zerar inbox_events: %w", err)
		}
		inbox += tag.RowsAffected()
	}
	rep.Steps = append(rep.Steps, ResetStep{Table: "inbox_events", Rows: inbox})
	return rep, nil
}

var secretPattern = regexp.MustCompile(`(?i)\b(access_token|appsecret_proof|token|key|apikey|api_key|secret|password|senha|authorization|bearer)([=: ]+)("?)[A-Za-z0-9._\-]{8,}`)

// RedactSecrets troca valores com cara de segredo (token=..., key: ..., Bearer ...) por [oculto].
func RedactSecrets(s string) string {
	return secretPattern.ReplaceAllString(s, "$1$2$3[oculto]")
}

var linkToken = regexp.MustCompile(`/(cadastro|c|m)/[A-Za-z0-9_-]{10,}`)

// redactLinkTokens tira o token dos links de cadastro e de conexão Meta (quem lê o painel não usa o link).
func redactLinkTokens(s string) string { return linkToken.ReplaceAllString(s, "/$1/[link]") }
