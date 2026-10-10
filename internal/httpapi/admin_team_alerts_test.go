package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

func TestTeamAlertsPermissionsAndAudit(t *testing.T) {
	e := newAdminEnv(t, "otp")
	staff := e.login("marina@meuanuncio.io")
	admin := e.login("dono@meuanuncio.io")
	staffPath := "/api/team/" + strconv.FormatInt(e.staffID, 10) + "/alerts"
	adminPath := "/api/team/" + strconv.FormatInt(e.adminID, 10) + "/alerts"

	// Equipe edita o próprio número (com máscara, fica só com dígitos).
	r := e.do("PUT", staffPath, staff, map[string]any{"whatsapp": "+55 (11) 97777-6666", "receives_alerts": true})
	if r.code != 200 || r.body["whatsapp"] != "5511977776666" || r.body["receives_alerts"] != true {
		t.Fatalf("staff edita o próprio = %d %s", r.code, r.raw)
	}
	// Equipe não edita o de outra pessoa nem manda teste para ela.
	if r := e.do("PUT", adminPath, staff, map[string]any{"whatsapp": "5511900000000", "receives_alerts": true}); r.code != http.StatusForbidden {
		t.Fatalf("staff edita outro = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", adminPath+"/test", staff, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff testa outro = %d %s", r.code, r.raw)
	}
	// Administrador edita qualquer um.
	if r := e.do("PUT", staffPath, admin, map[string]any{"whatsapp": "5511977776666", "receives_alerts": false}); r.code != 200 || r.body["receives_alerts"] != false {
		t.Fatalf("admin edita staff = %d %s", r.code, r.raw)
	}
	// Inválidos: número curto e aviso ligado sem número.
	if r := e.do("PUT", adminPath, admin, map[string]any{"whatsapp": "1234", "receives_alerts": false}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("número curto = %d %s", r.code, r.raw)
	}
	if r := e.do("PUT", adminPath, admin, map[string]any{"whatsapp": "", "receives_alerts": true}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("aviso sem número = %d %s", r.code, r.raw)
	}
	if r := e.do("PUT", "/api/team/99999/alerts", admin, map[string]any{"whatsapp": "", "receives_alerts": false}); r.code != http.StatusNotFound {
		t.Fatalf("inexistente = %d %s", r.code, r.raw)
	}
	// Campo a mais é recusado (sem atribuição em massa).
	if r := e.do("PUT", staffPath, staff, map[string]any{"whatsapp": "5511977776666", "receives_alerts": true, "role": "admin"}); r.code != http.StatusBadRequest {
		t.Fatalf("campo a mais = %d %s", r.code, r.raw)
	}
	// Sem CSRF não grava.
	if r := e.do("PUT", staffPath, &session{cookie: staff.cookie, csrf: "errado"}, map[string]any{"whatsapp": "", "receives_alerts": false}); r.code != http.StatusForbidden || r.body["error"] != "csrf" {
		t.Fatalf("sem csrf = %d %s", r.code, r.raw)
	}

	phones, err := e.st.TeamAlertPhones(context.Background())
	if err != nil || len(phones) != 0 {
		t.Fatalf("ninguém marcado = %q %v", phones, err)
	}

	// Registro com antes e depois.
	var raw []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT data FROM admin_audit_log WHERE action = 'team_alerts' AND actor_email = 'dono@meuanuncio.io'
		ORDER BY id DESC LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Antes, Depois struct {
			WhatsApp       string `json:"whatsapp"`
			ReceivesAlerts bool   `json:"receives_alerts"`
		}
	}
	if err := json.Unmarshal(raw, &d); err != nil || !d.Antes.ReceivesAlerts || d.Depois.ReceivesAlerts || d.Depois.WhatsApp != "5511977776666" {
		t.Fatalf("registro = %s %v", raw, err)
	}
}

func TestTeamAlertTestEnqueuesNotice(t *testing.T) {
	e := newAdminEnv(t, "otp")
	staff := e.login("marina@meuanuncio.io")
	path := "/api/team/" + strconv.FormatInt(e.staffID, 10) + "/alerts"

	// Sem número salvo, não há para onde mandar.
	if r := e.do("POST", path+"/test", staff, nil); r.code != http.StatusConflict || r.body["error"] != "sem_whatsapp" {
		t.Fatalf("sem número = %d %s", r.code, r.raw)
	}
	if r := e.do("PUT", path, staff, map[string]any{"whatsapp": "5511977776666", "receives_alerts": true}); r.code != 200 {
		t.Fatalf("salvar = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", path+"/test", staff, nil); r.code != http.StatusAccepted {
		t.Fatalf("teste = %d %s", r.code, r.raw)
	}
	var chatID, text, tpl string
	if err := e.pool.QueryRow(context.Background(), `SELECT payload->>'chat_id', payload->>'text', coalesce(payload->'template'->>'name', '')
		FROM outbox_jobs WHERE kind = $1 ORDER BY id DESC LIMIT 1`, engine.JobSend).Scan(&chatID, &text, &tpl); err != nil {
		t.Fatal(err)
	}
	if chatID != "5511977776666@c.us" || text != engine.MsgTeamTest || tpl != engine.TplTeamAlert {
		t.Fatalf("job = %q %q %q", chatID, text, tpl)
	}
	if acts := e.auditActions("marina@meuanuncio.io"); !contains(acts, "team_alerts_test") {
		t.Fatalf("registro = %v", acts)
	}
	// Limite: não dá para disparar teste sem parar.
	limited := false
	for i := 0; i < 6; i++ {
		if r := e.do("POST", path+"/test", staff, nil); r.code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("sem limite no envio de teste")
	}
}
