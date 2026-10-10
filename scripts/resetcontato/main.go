// Comando resetcontato: apaga todos os registros de um telefone de teste para ele voltar como
// contato novo (cadastro do zero). Só apaga se a organização não tiver outros telefones.
// A mesma lógica atende o botão "Zerar contato" do painel (store.ResetOrganization).
//
//	DATABASE_URL=... go run ./scripts/resetcontato 5511999998888          (simulação)
//	DATABASE_URL=... go run ./scripts/resetcontato 5511999998888 -apply   (apaga)
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
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

	rep, others, err := store.New(pool).ResetContacts(ctx, phones, apply)
	if err != nil {
		panic(err)
	}
	fmt.Println("telefones:", phones, "organizações:", rep.OrgIDs)
	if others > 0 {
		fmt.Println("ABORTADO: a organização tem outros telefones:", others)
		return
	}
	for _, s := range rep.Steps {
		fmt.Printf("%-20s %d\n", s.Table, s.Rows)
	}
	if !rep.Applied {
		fmt.Println("SIMULAÇÃO: nada foi apagado (use -apply)")
		return
	}
	fmt.Println("APAGADO")
}
