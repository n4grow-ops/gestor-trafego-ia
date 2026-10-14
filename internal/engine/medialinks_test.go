package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func (h *harness) links(kind string) []string {
	h.t.Helper()
	rows, err := h.pool.Query(ctx, `SELECT bucket || '/' || path || ' ' || url FROM media_links WHERE kind = $1 ORDER BY id`, kind)
	h.must(err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		h.must(rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

// Foto que o cliente manda fica guardada no Storage com link, ligada à mensagem: o painel mostra mesmo
// depois que o WhatsApp apaga a mídia (cerca de 30 dias).
func TestInboundMediaIsArchived(t *testing.T) {
	h, fs := newCreativeHarness(t)
	h.att.Responses = []string{`{"intent":"other","reply":"Que foto!"}`}
	h.sendFile(square)
	h.runKind(engine.JobMediaArchive)
	l := h.links("message")
	if len(l) != 1 || !strings.HasPrefix(l[0], engine.ChatMediaBucket+"/") || !strings.Contains(l[0], "https://files.test/") {
		t.Fatalf("links = %v", l)
	}
	var ref int64
	h.must(h.pool.QueryRow(ctx, `SELECT ref_id FROM media_links WHERE kind = 'message'`).Scan(&ref))
	var kind string
	h.must(h.pool.QueryRow(ctx, `SELECT kind FROM messages WHERE id = $1`, ref).Scan(&kind))
	if kind != "image" {
		t.Fatalf("link ligado à mensagem errada (%s)", kind)
	}
	if fs.count() != 1 {
		t.Fatalf("arquivos = %d", fs.count())
	}
}

// Anúncio recebido e prévia gerada ganham link (o painel mostra na aba do projeto).
func TestCreativeAndPreviewGetLinks(t *testing.T) {
	h, _, _ := newPublishHarness(t, "owner")
	h.connectMeta()
	h.readyProject()
	if c := h.links("creative"); len(c) != 1 || !strings.HasPrefix(c[0], engine.CreativesBucket+"/") {
		t.Fatalf("anúncio sem link: %v", c)
	}
	h.runKind(engine.JobCreativesPreview)
	if p := h.links("preview"); len(p) != 1 || !strings.HasPrefix(p[0], engine.PreviewsBucket+"/") {
		t.Fatalf("prévia sem link: %v", p)
	}
}

// A limpeza diária renova os links que vencem em menos de 7 dias.
func TestCleanupRenewsLinks(t *testing.T) {
	h, _ := newCreativeHarness(t)
	_, err := h.pool.Exec(ctx, `INSERT INTO media_links (kind, ref_id, bucket, path, mime, url, url_until)
		VALUES ('message', 1, 'chat-media', 'x.jpg', 'image/jpeg', 'velho', $1), ('message', 2, 'chat-media', 'y.jpg', 'image/jpeg', 'novo', $2)`,
		h.now.Add(2*24*time.Hour), h.now.Add(20*24*time.Hour))
	h.must(err)
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobCleanup)))
	var a, b string
	h.must(h.pool.QueryRow(ctx, `SELECT url FROM media_links WHERE path = 'x.jpg'`).Scan(&a))
	h.must(h.pool.QueryRow(ctx, `SELECT url FROM media_links WHERE path = 'y.jpg'`).Scan(&b))
	if a == "velho" || b != "novo" {
		t.Fatalf("renovação: x=%q y=%q", a, b)
	}
}

func jobOf(kind string) store.Job { return store.Job{Kind: kind, Payload: []byte(`{}`)} }
