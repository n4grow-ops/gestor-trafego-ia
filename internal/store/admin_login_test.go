package store_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestAdminLoginFailuresLockAndBurnCode(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	const email = "dono@meuanuncio.io"

	// Sem pedir código, não há código valendo.
	if valid, err := st.AdminLoginGate(ctx, email); err != nil || valid {
		t.Fatalf("antes do código: valid=%v err=%v", valid, err)
	}
	must(t, st.AdminCodeRequest(ctx, email, true))
	if valid, err := st.AdminLoginGate(ctx, email); err != nil || !valid {
		t.Fatalf("depois do código: valid=%v err=%v", valid, err)
	}
	for i := 1; i <= 4; i++ {
		locked, err := st.AdminLoginFailed(ctx, email)
		if err != nil || locked {
			t.Fatalf("falha %d: locked=%v err=%v", i, locked, err)
		}
	}
	// A quinta falha queima o código e bloqueia o e-mail.
	locked, err := st.AdminLoginFailed(ctx, email)
	if err != nil || !locked {
		t.Fatalf("quinta falha: locked=%v err=%v", locked, err)
	}
	if _, err := st.AdminLoginGate(ctx, email); !errors.Is(err, store.ErrLoginLocked) {
		t.Fatalf("bloqueado: %v", err)
	}
	// Bloqueado, nem pede código novo.
	if err := st.AdminCodeRequest(ctx, email, true); !errors.Is(err, store.ErrLoginLocked) {
		t.Fatalf("código com bloqueio: %v", err)
	}
	// Passados os 15 minutos, o código antigo continua sem valer: precisa de um novo.
	_, err = pool.Exec(ctx, `UPDATE admin_login_state SET locked_until = now() - interval '1 second'`)
	must(t, err)
	if valid, err := st.AdminLoginGate(ctx, email); err != nil || valid {
		t.Fatalf("código antigo depois do bloqueio: valid=%v err=%v", valid, err)
	}
	// Errar com o código já queimado não bloqueia de novo (não há código para adivinhar).
	if locked, err := st.AdminLoginFailed(ctx, email); err != nil || locked {
		t.Fatalf("falha sem código valendo: locked=%v err=%v", locked, err)
	}
	if _, err := st.AdminLoginGate(ctx, email); err != nil {
		t.Fatalf("bloqueou de novo sem código: %v", err)
	}
	must(t, st.AdminCodeRequest(ctx, email, true))
	if valid, err := st.AdminLoginGate(ctx, email); err != nil || !valid {
		t.Fatalf("código novo: valid=%v err=%v", valid, err)
	}
	// Entrar consome o código e zera as falhas.
	must(t, st.AdminLoginSucceeded(ctx, email))
	if valid, _ := st.AdminLoginGate(ctx, email); valid {
		t.Fatal("código continua valendo depois do login")
	}
	// Quem não é da equipe passa pelo mesmo caminho (código "pedido", 5 falhas, bloqueio).
	must(t, st.AdminCodeRequest(ctx, "intruso@mail.com", false))
	for i := 0; i < 5; i++ {
		_, err = st.AdminLoginFailed(ctx, "intruso@mail.com")
		must(t, err)
	}
	if _, err := st.AdminLoginGate(ctx, "intruso@mail.com"); !errors.Is(err, store.ErrLoginLocked) {
		t.Fatalf("intruso: %v", err)
	}
	// Outro e-mail não é afetado.
	if _, err := st.AdminLoginGate(ctx, "marina@meuanuncio.io"); err != nil {
		t.Fatalf("outro e-mail: %v", err)
	}
}

func TestAdminLoginDailyFailureCap(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	const email = "dono@meuanuncio.io"
	// 19 falhas no dia (de rodadas anteriores) e uma rodada nova: a vigésima bloqueia.
	_, err := pool.Exec(ctx, `INSERT INTO admin_login_events (email, kind, created_at)
		SELECT $1, 'failure', now() - interval '20 hours' FROM generate_series(1, 19)`, email)
	must(t, err)
	must(t, st.AdminCodeRequest(ctx, email, true))
	locked, err := st.AdminLoginFailed(ctx, email)
	if err != nil || !locked {
		t.Fatalf("vigésima falha do dia: locked=%v err=%v", locked, err)
	}
	_, err = pool.Exec(ctx, `UPDATE admin_login_state SET locked_until = NULL`)
	must(t, err)
	if _, err := st.AdminLoginGate(ctx, email); !errors.Is(err, store.ErrLoginLocked) {
		t.Fatalf("teto diário: %v", err)
	}
	// Falhas de mais de 24 h não contam.
	_, err = pool.Exec(ctx, `UPDATE admin_login_events SET created_at = now() - interval '25 hours'`)
	must(t, err)
	if _, err := st.AdminLoginGate(ctx, email); err != nil {
		t.Fatalf("depois de 24 h: %v", err)
	}
}

func TestAdminCodeRequestHourlyLimits(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	// 5 por e-mail por hora, contando também pedidos para quem não é da equipe (mesma resposta).
	for i := 0; i < 5; i++ {
		must(t, st.AdminCodeRequest(ctx, "dono@meuanuncio.io", true))
		must(t, st.AdminCodeRequest(ctx, "intruso@mail.com", false))
	}
	if err := st.AdminCodeRequest(ctx, "dono@meuanuncio.io", true); !errors.Is(err, store.ErrTooManyCodes) {
		t.Fatalf("sexto código do e-mail: %v", err)
	}
	if err := st.AdminCodeRequest(ctx, "intruso@mail.com", false); !errors.Is(err, store.ErrTooManyCodes) {
		t.Fatalf("sexto pedido de quem não é da equipe: %v", err)
	}
	// 20 envios no total por hora (pedidos sem envio não gastam o teto total).
	for i := 0; i < 15; i++ {
		must(t, st.AdminCodeRequest(ctx, fmt.Sprintf("p%d@meuanuncio.io", i), true))
	}
	if err := st.AdminCodeRequest(ctx, "outro@meuanuncio.io", true); !errors.Is(err, store.ErrTooManyCodes) {
		t.Fatalf("vigésimo primeiro envio na hora: %v", err)
	}
	// Uma hora depois, volta a enviar.
	_, err := pool.Exec(ctx, `UPDATE admin_login_events SET created_at = now() - interval '61 minutes'`)
	must(t, err)
	must(t, st.AdminCodeRequest(ctx, "dono@meuanuncio.io", true))
}
