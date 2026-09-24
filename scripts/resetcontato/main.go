// Comando resetcontato: apaga todos os registros de um telefone de teste para ele voltar como
// contato novo (cadastro do zero). Só apaga se a organização não tiver outros telefones.
//
//	DATABASE_URL=... go run ./scripts/resetcontato 5511999998888          (simulação)
//	DATABASE_URL=... go run ./scripts/resetcontato 5511999998888 -apply   (apaga)
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("uso: resetcontato <telefone> [-apply]")
		os.Exit(2)
	}
	phone, err := whatsapp.NormalizePhone(os.Args[1])
	if err != nil {
		panic(err)
	}
	phones := []string{phone}
	if len(phone) == 13 && strings.HasPrefix(phone, "55") { // também sem o nono dígito
		phones = append(phones, phone[:4]+phone[5:])
	}
	apply := len(os.Args) > 2 && os.Args[2] == "-apply"
	ctx := context.Background()
	pool, err := db.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		panic(err)
	}
	defer tx.Rollback(ctx)

	rows, _ := tx.Query(ctx, `SELECT DISTINCT m.organization_id FROM whatsapp_identities w JOIN members m ON m.id = w.member_id WHERE w.phone_e164 = ANY($1)`, phones)
	orgIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		panic(err)
	}
	fmt.Println("telefones:", phones, "organizações:", orgIDs)
	var others int
	tx.QueryRow(ctx, `SELECT count(*) FROM whatsapp_identities w JOIN members m ON m.id = w.member_id
		WHERE m.organization_id = ANY($1) AND NOT (w.phone_e164 = ANY($2))`, orgIDs, phones).Scan(&others)
	if others > 0 {
		fmt.Println("ABORTADO: a organização tem outros telefones:", others)
		return
	}
	conv := `SELECT c.id FROM conversations c JOIN whatsapp_identities w ON w.id = c.identity_id JOIN members m ON m.id = w.member_id WHERE m.organization_id = ANY($1)`
	steps := []struct{ name, sql string }{
		{"payments", `DELETE FROM payments WHERE subscription_id IN (SELECT id FROM subscriptions WHERE organization_id = ANY($1))`},
		{"subscriptions", `DELETE FROM subscriptions WHERE organization_id = ANY($1)`},
		{"consents", `DELETE FROM consents WHERE member_id IN (SELECT id FROM members WHERE organization_id = ANY($1))`},
		{"approval_codes", `DELETE FROM approval_codes WHERE draft_id IN (SELECT id FROM campaign_drafts WHERE organization_id = ANY($1))`},
		{"campaign_drafts", `DELETE FROM campaign_drafts WHERE organization_id = ANY($1)`},
		{"outbox_jobs", `DELETE FROM outbox_jobs WHERE (payload->>'conversation_id')::bigint IN (` + conv + `)`},
		{"messages", `DELETE FROM messages WHERE conversation_id IN (` + conv + `)`},
		{"conversations", `DELETE FROM conversations WHERE id IN (` + conv + `)`},
		{"projects", `DELETE FROM projects WHERE organization_id = ANY($1)`},
		{"signup_links", `DELETE FROM signup_links WHERE identity_id IN (SELECT w.id FROM whatsapp_identities w JOIN members m ON m.id = w.member_id WHERE m.organization_id = ANY($1))`},
		{"whatsapp_identities", `DELETE FROM whatsapp_identities WHERE member_id IN (SELECT id FROM members WHERE organization_id = ANY($1))`},
		{"members", `DELETE FROM members WHERE organization_id = ANY($1)`},
		{"llm_usage", `DELETE FROM llm_usage WHERE organization_id = ANY($1)`},
		{"audit_events", `DELETE FROM audit_events WHERE organization_id = ANY($1)`},
		{"organizations", `DELETE FROM organizations WHERE id = ANY($1)`},
	}
	for _, s := range steps {
		tag, err := tx.Exec(ctx, s.sql, orgIDs)
		if err != nil {
			panic(fmt.Sprintf("%s: %v", s.name, err))
		}
		fmt.Printf("%-20s %d\n", s.name, tag.RowsAffected())
	}
	var inbox int64
	for _, p := range phones {
		tag, err := tx.Exec(ctx, `DELETE FROM inbox_events WHERE payload::text LIKE '%' || $1 || '%'`, p)
		if err != nil {
			panic(err)
		}
		inbox += tag.RowsAffected()
	}
	fmt.Printf("%-20s %d\n", "inbox_events", inbox)
	if !apply {
		fmt.Println("SIMULAÇÃO: nada foi apagado (use -apply)")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("APAGADO")
}
