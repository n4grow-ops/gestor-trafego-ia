package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Ana@Mail.COM ": "ana@mail.com",
		"ana@mail.com":    "ana@mail.com",
		"sem-arroba":      "",
		"a@b":             "",
		"a b@mail.com":    "",
		"":                "",
	}
	for in, want := range cases {
		got, ok := store.NormalizeEmail(in)
		if got != want || ok != (want != "") {
			t.Errorf("NormalizeEmail(%q) = %q, %v; quero %q", in, got, ok, want)
		}
	}
}

func TestTeamMembers(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()

	if err := st.EnsureAdmins(ctx, []string{" Dono@MeuAnuncio.io", "", "invalido"}); err != nil {
		t.Fatal(err)
	}
	owner, err := st.TeamMemberByEmail(ctx, "dono@meuanuncio.io")
	if err != nil || owner.Role != "admin" || !owner.Active {
		t.Fatalf("bootstrap: %+v %v", owner, err)
	}
	// O único administrador não pode ser rebaixado.
	if _, err := st.AddTeamMember(ctx, "dono@meuanuncio.io", "Dono", "staff", "teste"); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("rebaixar o último admin: %v", err)
	}
	if owner, _ = st.TeamMemberByEmail(ctx, "dono@meuanuncio.io"); owner.Role != "admin" {
		t.Fatalf("último admin rebaixado: %+v", owner)
	}
	// Com outro administrador, rebaixa; e o bootstrap não desfaz o que foi feito à mão.
	if _, err := st.AddTeamMember(ctx, "socio@meuanuncio.io", "Sócio", "admin", "dono@meuanuncio.io"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTeamMember(ctx, "dono@meuanuncio.io", "Dono", "staff", "teste"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE team_members SET active = false WHERE email = 'socio@meuanuncio.io'`); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureAdmins(ctx, []string{"dono@meuanuncio.io", "socio@meuanuncio.io"}); err != nil {
		t.Fatal(err)
	}
	owner, _ = st.TeamMemberByEmail(ctx, "dono@meuanuncio.io")
	if owner.Role != "staff" || owner.Name != "Dono" {
		t.Fatalf("bootstrap promoveu de novo quem foi rebaixado: %+v", owner)
	}
	if socio, _ := st.TeamMemberByEmail(ctx, "socio@meuanuncio.io"); socio.Active {
		t.Fatalf("bootstrap reativou quem foi desativado: %+v", socio)
	}
	// Volta ao estado inicial para o resto do teste.
	if _, err := st.AddTeamMember(ctx, "dono@meuanuncio.io", "Dono", "admin", "teste"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM team_members WHERE email = 'socio@meuanuncio.io'`); err != nil {
		t.Fatal(err)
	}

	staff, err := st.AddTeamMember(ctx, "Marina@MeuAnuncio.io", "Marina", "staff", "dono@meuanuncio.io")
	if err != nil || staff.Email != "marina@meuanuncio.io" || staff.Role != "staff" || staff.AddedBy != "dono@meuanuncio.io" {
		t.Fatalf("adicionar: %+v %v", staff, err)
	}
	if _, err := st.AddTeamMember(ctx, "x@y.z", "X", "dono", ""); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("papel inválido: %v", err)
	}
	if _, err := st.AddTeamMember(ctx, "nao-e-email", "X", "staff", ""); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("e-mail inválido: %v", err)
	}

	team, err := st.ListTeam(ctx)
	if err != nil || len(team) != 2 || team[0].Role != "admin" {
		t.Fatalf("lista (admins primeiro): %+v %v", team, err)
	}

	byID, err := st.TeamMemberByID(ctx, staff.ID)
	if err != nil || byID.Email != staff.Email {
		t.Fatalf("por id: %+v %v", byID, err)
	}
	if err := st.TouchTeamLogin(ctx, staff.ID); err != nil {
		t.Fatal(err)
	}
	byID, _ = st.TeamMemberByID(ctx, staff.ID)
	if byID.LastLoginAt == nil {
		t.Fatal("último login não gravado")
	}

	// O último administrador ativo não sai.
	if err := st.RemoveTeamMember(ctx, owner.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("remover último admin: %v", err)
	}
	if err := st.RemoveTeamMember(ctx, staff.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TeamMemberByID(ctx, staff.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removido continua: %v", err)
	}
	if err := st.RemoveTeamMember(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remover inexistente: %v", err)
	}
}

func TestTeamAlertPhones(t *testing.T) {
	st, pool := newStore(t)
	ctx := context.Background()
	ana, err := st.AddTeamMember(ctx, "ana@meuanuncio.io", "Ana", "admin", "teste")
	if err != nil {
		t.Fatal(err)
	}
	bia, err := st.AddTeamMember(ctx, "bia@meuanuncio.io", "Bia", "staff", "teste")
	if err != nil {
		t.Fatal(err)
	}
	caio, err := st.AddTeamMember(ctx, "caio@meuanuncio.io", "Caio", "staff", "teste")
	if err != nil {
		t.Fatal(err)
	}

	// Sem ninguém marcado, a lista vem vazia (o motor cai no TEAM_PHONES).
	if got, err := st.TeamAlertPhones(ctx); err != nil || len(got) != 0 {
		t.Fatalf("vazio: %q %v", got, err)
	}

	// Número com máscara fica só com dígitos.
	m, err := st.SetTeamMemberAlerts(ctx, ana.ID, "+55 (11) 98888-7777", true)
	if err != nil || m.WhatsApp != "5511988887777" || !m.ReceivesAlerts {
		t.Fatalf("ana: %+v %v", m, err)
	}
	// Número guardado, mas sem receber avisos.
	if _, err := st.SetTeamMemberAlerts(ctx, bia.ID, "5511977776666", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetTeamMemberAlerts(ctx, caio.ID, "5511966665555", true); err != nil {
		t.Fatal(err)
	}
	// Inválidos: curto, longo e marcar aviso sem número.
	for _, c := range []struct {
		num      string
		receives bool
	}{{"123456789", false}, {"1234567890123456", true}, {"", true}, {"abc", true}} {
		if _, err := st.SetTeamMemberAlerts(ctx, bia.ID, c.num, c.receives); !errors.Is(err, store.ErrInvalidInput) {
			t.Fatalf("SetTeamMemberAlerts(%q, %v) = %v", c.num, c.receives, err)
		}
	}
	// Vazio com aviso desligado limpa o número.
	if m, err := st.SetTeamMemberAlerts(ctx, bia.ID, "", false); err != nil || m.WhatsApp != "" {
		t.Fatalf("limpar: %+v %v", m, err)
	}
	if _, err := st.SetTeamMemberAlerts(ctx, 99999, "5511900000000", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}

	got, err := st.TeamAlertPhones(ctx)
	if err != nil || len(got) != 2 || got[0] != "5511966665555" || got[1] != "5511988887777" {
		t.Fatalf("lista: %q %v", got, err)
	}
	// Inativo sai da lista.
	if _, err := pool.Exec(ctx, `UPDATE team_members SET active = false WHERE id = $1`, caio.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.TeamAlertPhones(ctx); len(got) != 1 || got[0] != "5511988887777" {
		t.Fatalf("sem inativo: %q", got)
	}
	// O banco também recusa aviso ligado sem número (trava da migration).
	if _, err := pool.Exec(ctx, `UPDATE team_members SET receives_alerts = true, whatsapp = '' WHERE id = $1`, bia.ID); err == nil {
		t.Fatal("banco aceitou aviso sem número")
	}
	byID, err := st.TeamMemberByID(ctx, ana.ID)
	if err != nil || byID.WhatsApp != "5511988887777" || !byID.ReceivesAlerts {
		t.Fatalf("por id: %+v %v", byID, err)
	}
}

func TestAdminAuditLog(t *testing.T) {
	st := store.New(testdb.New(t))
	ctx := context.Background()
	admin, _ := st.AddTeamMember(ctx, "dono@meuanuncio.io", "Dono", "admin", "")
	staff, _ := st.AddTeamMember(ctx, "marina@meuanuncio.io", "Marina", "staff", "")

	for _, a := range []store.AdminAction{
		{ActorID: admin.ID, ActorEmail: admin.Email, Action: "client_block", TargetType: "organization", TargetID: "7", TargetLabel: "Pizzaria", IP: "10.0.0.1"},
		{ActorID: staff.ID, ActorEmail: staff.Email, Action: "conversation_takeover", TargetType: "conversation", TargetID: "3", Data: map[string]any{"from": "collecting"}},
		{ActorID: staff.ID, ActorEmail: staff.Email, Action: "conversation_send", TargetType: "conversation", TargetID: "3"},
	} {
		if err := st.LogAdminAction(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.AdminLog(ctx, store.AdminLogFilter{Limit: 10})
	if err != nil || len(all) != 3 || all[0].Action != "conversation_send" {
		t.Fatalf("tudo, mais novo primeiro: %+v %v", all, err)
	}
	if all[2].IP != "10.0.0.1" || all[2].TargetLabel != "Pizzaria" || all[2].CreatedAt.IsZero() {
		t.Fatalf("campos: %+v", all[2])
	}
	mine, _ := st.AdminLog(ctx, store.AdminLogFilter{ActorEmail: staff.Email, Limit: 10})
	if len(mine) != 2 {
		t.Fatalf("só as da Marina: %+v", mine)
	}
	conv, _ := st.AdminLog(ctx, store.AdminLogFilter{TargetType: "conversation", TargetID: "3", Limit: 10})
	if len(conv) != 2 || string(conv[1].Data) != `{"from": "collecting"}` {
		t.Fatalf("por alvo: %+v", conv)
	}
	// Quem sai da equipe continua no registro.
	if err := st.RemoveTeamMember(ctx, staff.ID); err != nil {
		t.Fatal(err)
	}
	mine, _ = st.AdminLog(ctx, store.AdminLogFilter{ActorEmail: staff.Email, Limit: 10})
	if len(mine) != 2 {
		t.Fatalf("registro de quem saiu sumiu: %+v", mine)
	}
	if err := st.LogAdminAction(ctx, store.AdminAction{ActorEmail: "", Action: "x"}); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("sem autor: %v", err)
	}
}
