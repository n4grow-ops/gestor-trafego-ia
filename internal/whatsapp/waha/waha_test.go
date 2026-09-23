package waha_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/waha"
)

var ctx = context.Background()

func event(name, payload string) []byte {
	return []byte(`{"id":"evt_1","timestamp":1,"session":"default","engine":"GOWS","event":"` + name + `","payload":` + payload + `,"environment":{}}`)
}

func parse(t *testing.T, body []byte) (whatsapp.Inbound, bool) {
	t.Helper()
	in, ok, err := waha.New("http://waha:3000", "k", "default").ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	return in, ok
}

func TestParseText(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"false_5511988887777@c.us_ABC","from":"5511988887777@c.us","fromMe":false,"body":"quero anunciar","hasMedia":false}`))
	if !ok || in.ExternalID != "false_5511988887777@c.us_ABC" || in.ChatID != "5511988887777@c.us" ||
		in.Phone != "5511988887777" || in.Kind != whatsapp.KindText || in.Text != "quero anunciar" || len(in.Raw) == 0 {
		t.Errorf("in = %+v ok = %v", in, ok)
	}
}

func TestParseAddsBrazilianNinthDigit(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m1","from":"551188887777@c.us","body":"oi"}`))
	if !ok || in.Phone != "5511988887777" || in.ChatID != "551188887777@c.us" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseAcceptsWhatsAppNetSuffix(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m1","from":"5511988887777@s.whatsapp.net","body":"oi"}`))
	if !ok || in.Phone != "5511988887777" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseAudio(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m2","from":"5511988887777@c.us","body":"","hasMedia":true,"media":{"url":"http://localhost:3000/api/files/default/m2.oga","mimetype":"audio/ogg; codecs=opus"}}`))
	if !ok || in.Kind != whatsapp.KindAudio || in.Text != "" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseImageUsesCaption(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m3","from":"5511988887777@c.us","body":"meu cardápio","hasMedia":true,"media":{"url":"http://x/api/files/default/m3.jpeg","mimetype":"image/jpeg"}}`))
	if !ok || in.Kind != whatsapp.KindImage || in.Text != "meu cardápio" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseOtherMedia(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m4","from":"5511988887777@c.us","body":"","hasMedia":true,"media":{"mimetype":"application/pdf"}}`))
	if !ok || in.Kind != whatsapp.KindOther {
		t.Errorf("in = %+v", in)
	}
}

func TestParseUsesParticipantWhenFromIsLID(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"m5","from":"99999@lid","participant":"5511988887777@c.us","body":"oi"}`))
	if !ok || in.Phone != "5511988887777" || in.ChatID != "99999@lid" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseIgnores(t *testing.T) {
	cases := map[string][]byte{
		"outro evento":    event("message.ack", `{"id":"a","from":"5511988887777@c.us"}`),
		"sessão":          event("session.status", `{"status":"WORKING"}`),
		"enviada por nós": event("message", `{"id":"m","from":"5511988887777@c.us","fromMe":true,"body":"x"}`),
		"grupo":           event("message", `{"id":"m","from":"12036302@g.us","participant":"5511988887777@c.us","body":"x"}`),
		"status":          event("message", `{"id":"m","from":"status@broadcast","body":"x"}`),
		"canal":           event("message", `{"id":"m","from":"123@newsletter","body":"x"}`),
		"só lid":          event("message", `{"id":"m","from":"99999@lid","body":"oi"}`),
		"sem id":          event("message", `{"from":"5511988887777@c.us","body":"x"}`),
	}
	for name, body := range cases {
		if _, ok := parse(t, body); ok {
			t.Errorf("%s: deveria ser ignorado", name)
		}
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, _, err := waha.New("http://x", "k", "default").ParseWebhook([]byte("{nope")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestSendText(t *testing.T) {
	var got map[string]any
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("X-Api-Key")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(201)
		io.WriteString(w, `{"id":"true_x"}`)
	}))
	defer srv.Close()

	if err := waha.New(srv.URL, "k", "default").SendText(ctx, "5511988887777@c.us", "olá"); err != nil {
		t.Fatal(err)
	}
	if path != "/api/sendText" || key != "k" || got["chatId"] != "5511988887777@c.us" || got["text"] != "olá" || got["session"] != "default" {
		t.Errorf("path=%s key=%s body=%v", path, key, got)
	}
}

func TestSendTextHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(422) }))
	defer srv.Close()
	if err := waha.New(srv.URL, "k", "default").SendText(ctx, "x@c.us", "olá"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestDownloadMediaUsesBaseURLAndKey(t *testing.T) {
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "audio/ogg")
		io.WriteString(w, "ogg-bytes")
	}))
	defer srv.Close()

	c := waha.New(srv.URL, "k", "default")
	// A URL do evento aponta para outro host (base externa do WAHA); só o caminho é usado.
	in, _, _ := c.ParseWebhook(event("message", `{"id":"m2","from":"5511988887777@c.us","hasMedia":true,"media":{"url":"http://localhost:3000/api/files/default/m2.oga","mimetype":"audio/ogg; codecs=opus"}}`))
	data, mime, err := c.DownloadMedia(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ogg-bytes" || mime != "audio/ogg" || path != "/api/files/default/m2.oga" || key != "k" {
		t.Errorf("data=%q mime=%q path=%s key=%s", data, mime, path, key)
	}
}

func TestDownloadMediaWithoutURL(t *testing.T) {
	c := waha.New("http://x", "k", "default")
	in, _, _ := c.ParseWebhook(event("message", `{"id":"m2","from":"5511988887777@c.us","hasMedia":true,"media":{"mimetype":"audio/ogg","error":"falhou"}}`))
	if _, _, err := c.DownloadMedia(ctx, in); err == nil {
		t.Fatal("esperava erro sem URL de mídia")
	}
}

func TestNameAndChatID(t *testing.T) {
	c := waha.New("http://x", "k", "default")
	if c.Name() != "waha" || c.ChatIDForPhone("5511900000000") != "5511900000000@c.us" {
		t.Errorf("name=%s", c.Name())
	}
}
