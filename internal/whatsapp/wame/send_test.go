package wame_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

var (
	_ whatsapp.Provider         = (*wame.Client)(nil)
	_ whatsapp.ConnectionParser = (*wame.Client)(nil)
	_ whatsapp.TemplateSender   = (*wame.Client)(nil)
	_ whatsapp.FlowSender       = (*wame.Client)(nil)
)

type call struct {
	Method, Path, Auth string
	Body               map[string]any
}

func server(t *testing.T, status int, reply string) (*wame.Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		calls = append(calls, call{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), body})
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return wame.New(srv.URL, "KEY", "TOK"), &calls
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSendText(t *testing.T) {
	c, calls := server(t, 200, `{"status":200}`)
	must(t, c.SendText(context.Background(), "5511999998888@c.us", "oi"))
	got := (*calls)[0]
	if got.Method != "POST" || got.Path != "/KEY/message/text" || got.Auth != "Bearer TOK" {
		t.Fatalf("%+v", got)
	}
	if got.Body["to"] != "5511999998888" || got.Body["text"] != "oi" {
		t.Fatalf("body %+v", got.Body)
	}
}

func TestNoTokenNoHeader(t *testing.T) {
	auth := "x"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { auth = r.Header.Get("Authorization") }))
	defer srv.Close()
	must(t, wame.New(srv.URL, "KEY", "").SendText(context.Background(), "5511999998888", "oi"))
	if auth != "" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestSendErrors(t *testing.T) {
	c, _ := server(t, 401, `{"code":"token_invalid"}`)
	if err := c.SendText(context.Background(), "5511999998888", "oi"); err == nil {
		t.Fatal("esperava erro em 401")
	}
}

func TestVoiceAndImage(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	ctx := context.Background()
	must(t, c.SendVoice(ctx, "5511999998888", []byte("AUD"), "audio/mpeg"))
	must(t, c.SendImage(ctx, "5511999998888", []byte("IMG"), "image/jpeg", "prévia"))
	aud, img := (*calls)[0], (*calls)[1]
	if aud.Path != "/KEY/message/base64/audio" || aud.Body["base64"] != "data:audio/mpeg;base64,"+base64.StdEncoding.EncodeToString([]byte("AUD")) {
		t.Errorf("voz: %+v", aud)
	}
	if img.Path != "/KEY/message/base64/image" || img.Body["caption"] != "prévia" || img.Body["base64"] != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString([]byte("IMG")) {
		t.Errorf("imagem: %+v", img)
	}
}

func TestPresenceAndSeenUnofficial(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	ctx := context.Background()
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresenceTyping))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresenceRecording))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresencePaused))
	must(t, c.MarkSeen(ctx, "5511999998888"))
	for i, want := range []string{"composing", "recording", "paused"} {
		if (*calls)[i].Path != "/KEY/message/presence" || (*calls)[i].Body["status"] != want {
			t.Errorf("%d: %+v", i, (*calls)[i])
		}
	}
	if seen := (*calls)[3]; seen.Method != "PATCH" || seen.Path != "/KEY/chat?action=markRead&id=5511999998888&value=true" {
		t.Errorf("visto: %+v", seen)
	}
}

// Na Cloud API, visto e "digitando…" usam o id da última mensagem recebida do chat.
func TestPresenceAndSeenOfficial(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	c.Official = true
	ctx := context.Background()
	// Sem mensagem conhecida do chat: nada a fazer, sem erro.
	must(t, c.MarkSeen(ctx, "5511999998888"))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresenceTyping))
	if len(*calls) != 0 {
		t.Fatalf("chamou sem id: %+v", *calls)
	}
	if _, ok, _ := c.ParseWebhook(msg(`{"from":"5511999998888","id":"wamid.1","type":"text","text":{"body":"oi"}}`)); !ok {
		t.Fatal("parse")
	}
	must(t, c.MarkSeen(ctx, "5511999998888@c.us"))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresenceTyping))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresenceRecording))
	must(t, c.SetPresence(ctx, "5511999998888", whatsapp.PresencePaused))
	if len(*calls) != 3 {
		t.Fatalf("chamadas: %+v", *calls)
	}
	for i, typing := range []bool{false, true, true} {
		got := (*calls)[i]
		if got.Path != "/KEY/message/read" || got.Body["messageId"] != "wamid.1" || got.Body["typing"] != typing {
			t.Errorf("%d: %+v", i, got)
		}
	}
}

func TestDownloadMedia(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "audio/ogg; codecs=opus")
		io.WriteString(w, "OGG")
	}))
	defer srv.Close()
	c := wame.New(srv.URL, "KEY", "")
	in, ok, _ := c.ParseWebhook(msg(`{"from":"5511999998888","chat_type":"individual","id":"A1","type":"audio","audio":{"id":"A1","url":"https://evil.example/x","mime_type":"audio/ogg"}}`))
	if !ok {
		t.Fatal("parse")
	}
	data, mime, err := c.DownloadMedia(context.Background(), in)
	if err != nil || string(data) != "OGG" || mime != "audio/ogg" {
		t.Fatalf("data=%q mime=%q err=%v", data, mime, err)
	}
	if path != "/KEY/message/A1/media?format=binary" {
		t.Fatalf("path = %q (não pode seguir a URL do payload)", path)
	}
}

func TestDownloadMediaError(t *testing.T) {
	c, _ := server(t, 404, `{}`)
	if _, _, err := c.DownloadMedia(context.Background(), whatsapp.Inbound{ExternalID: "X"}); err == nil {
		t.Fatal("esperava erro em 404")
	}
}

func TestChatIDForPhone(t *testing.T) {
	if got := wame.New("", "K", "").ChatIDForPhone("5511999998888"); got != "5511999998888" {
		t.Fatal(got)
	}
}

func TestSendTemplate(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	if c.NeedsTemplates() {
		t.Fatal("instância QR não usa modelos")
	}
	c.Official = true
	if !c.NeedsTemplates() {
		t.Fatal("instância oficial usa modelos")
	}
	must(t, c.SendTemplate(context.Background(), "5511999998888@c.us", whatsapp.Template{Name: "aviso_equipe", Params: []string{"Ana\nSilva", "", "a    b"}}))
	got := (*calls)[0]
	if got.Path != "/KEY/message/template" || got.Body["to"] != "5511999998888" || got.Body["name"] != "aviso_equipe" || got.Body["language"] != "pt_BR" {
		t.Fatalf("%+v", got)
	}
	comps := got.Body["components"].([]any)
	params := comps[0].(map[string]any)["parameters"].([]any)
	want := []string{"Ana Silva", "-", "a b"}
	for i, p := range params {
		if p.(map[string]any)["text"] != want[i] || p.(map[string]any)["type"] != "text" {
			t.Errorf("param %d = %v, want %q", i, p, want[i])
		}
	}
}

func TestSendTemplateWithoutParams(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	must(t, c.SendTemplate(context.Background(), "5511999998888", whatsapp.Template{Name: "ola"}))
	if _, has := (*calls)[0].Body["components"]; has {
		t.Fatalf("sem variáveis não manda components: %+v", (*calls)[0].Body)
	}
}

func TestSendTemplateTruncatesLongParams(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	long := strings.Repeat("á", 2000)
	must(t, c.SendTemplate(context.Background(), "5511999998888", whatsapp.Template{Name: "aviso_equipe", Params: []string{long}}))
	p := (*calls)[0].Body["components"].([]any)[0].(map[string]any)["parameters"].([]any)[0].(map[string]any)["text"].(string)
	if n := len([]rune(p)); n > 900 || !strings.HasSuffix(p, "…") {
		t.Fatalf("variável com %d caracteres", n)
	}
}

func TestSendFlow(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	must(t, c.SendFlow(context.Background(), "5511999998888@c.us", whatsapp.Flow{ID: "FLOW1", Token: "tok", CTA: "Fazer meu cadastro", Body: "corpo", Header: "cab", Footer: "rod", Screen: "DADOS"}))
	got := (*calls)[0]
	want := map[string]any{"to": "5511999998888", "flowId": "FLOW1", "flowToken": "tok", "flowCta": "Fazer meu cadastro", "body": "corpo",
		"header": "cab", "footer": "rod", "screen": "DADOS", "flowAction": "navigate", "mode": "published"}
	if got.Path != "/KEY/message/flow" {
		t.Fatalf("path %s", got.Path)
	}
	for k, v := range want {
		if got.Body[k] != v {
			t.Errorf("%s = %v, want %v", k, got.Body[k], v)
		}
	}
}
