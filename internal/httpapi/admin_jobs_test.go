package httpapi_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Tarefa que falhou: a equipe vê; só admin manda de novo ou descarta (antes era SQL na mão).
func TestFailedJobsRetryAndDiscard(t *testing.T) {
	e := newAdminEnv(t, "otp")
	ctx := context.Background()
	var a, b int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.pool.QueryRow(ctx, `INSERT INTO outbox_jobs (kind, payload, status, attempts, last_error)
		VALUES ('creatives.preview', $1, 'failed', 3, 'wame /message/base64/image: status 422 token=abc123segredo') RETURNING id`,
		fmt.Sprintf(`{"conversation_id": %d, "draft_id": 8}`, e.convID)).Scan(&a))
	must(e.pool.QueryRow(ctx, `INSERT INTO outbox_jobs (kind, payload, status, attempts, last_error)
		VALUES ('meta.publish', '{}', 'failed', 3, 'x') RETURNING id`).Scan(&b))

	staff := e.login("marina@meuanuncio.io")
	r := e.do("GET", "/api/jobs", staff, nil)
	if r.code != 200 || !strings.Contains(r.raw, "creatives.preview") || !strings.Contains(r.raw, "Pizzaria Boa") {
		t.Fatalf("lista: %d %s", r.code, r.raw)
	}
	if strings.Contains(r.raw, "abc123segredo") {
		t.Error("erro com segredo à mostra")
	}
	if r = e.do("POST", fmt.Sprintf("/api/jobs/%d/retry", a), staff, nil); r.code != 403 {
		t.Fatalf("equipe não-admin reprocessou: %d", r.code)
	}

	admin := e.login("dono@meuanuncio.io")
	if r = e.do("POST", fmt.Sprintf("/api/jobs/%d/retry", a), admin, nil); r.code != 200 {
		t.Fatalf("retry: %d %s", r.code, r.raw)
	}
	var status string
	var attempts int
	must(e.pool.QueryRow(ctx, `SELECT status, attempts FROM outbox_jobs WHERE id = $1`, a).Scan(&status, &attempts))
	if status != "pending" || attempts != 0 {
		t.Fatalf("depois do retry: %s %d", status, attempts)
	}
	if r = e.do("POST", fmt.Sprintf("/api/jobs/%d/retry", a), admin, nil); r.code != 404 {
		t.Errorf("retry de tarefa que não está em falha: %d", r.code)
	}
	if r = e.do("POST", fmt.Sprintf("/api/jobs/%d/discard", b), admin, nil); r.code != 200 {
		t.Fatalf("discard: %d", r.code)
	}
	var last string
	must(e.pool.QueryRow(ctx, `SELECT status, last_error FROM outbox_jobs WHERE id = $1`, b).Scan(&status, &last))
	if status != "done" || !strings.Contains(last, "descartado pela equipe (dono@meuanuncio.io)") {
		t.Fatalf("descarte: %s %q", status, last)
	}
	var n int
	must(e.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_log WHERE action IN ('job_retried', 'job_discarded')`).Scan(&n))
	if n != 2 {
		t.Errorf("log da equipe = %d", n)
	}
}
