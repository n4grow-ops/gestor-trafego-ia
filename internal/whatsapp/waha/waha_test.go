package waha_test

import (
	"context"
	"encoding/base64"
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

// Formato real observado em 2026-09-23 (GOWS): remetente só com LID; o telefone vem em _data.Info.SenderAlt.
func TestParseLIDWithSenderAlt(t *testing.T) {
	in, ok := parse(t, event("message", `{"id":"false_162170146963623@lid_3EB0","from":"162170146963623@lid","fromMe":false,"participant":null,"body":"Quero anunciar minha pizzaria","hasMedia":false,"_data":{"Info":{"Chat":"162170146963623@lid","Sender":"162170146963623:7@lid","SenderAlt":"557399372664:7@s.whatsapp.net"}}}`))
	if !ok {
		t.Fatal("mensagem com SenderAlt não pode ser ignorada")
	}
	if in.Phone != "5573999372664" || in.ChatID != "557399372664@c.us" || in.Text != "Quero anunciar minha pizzaria" {
		t.Errorf("in = %+v", in)
	}
}

func TestHumanEndpoints(t *testing.T) {
	type call struct {
		Path string
		Body map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("sem X-Api-Key em %s", r.URL.Path)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		calls = append(calls, call{r.URL.Path, b})
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := waha.New(srv.URL, "key", "default")
	if err := c.MarkSeen(ctx, "5511@c.us"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPresence(ctx, "5511@c.us", whatsapp.PresenceRecording); err != nil {
		t.Fatal(err)
	}
	if err := c.SendVoice(ctx, "5511@c.us", []byte("MP3"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("chamadas = %d", len(calls))
	}
	if calls[0].Path != "/api/sendSeen" || calls[0].Body["chatId"] != "5511@c.us" || calls[0].Body["session"] != "default" {
		t.Errorf("sendSeen = %+v", calls[0])
	}
	if calls[1].Path != "/api/default/presence" || calls[1].Body["presence"] != "recording" || calls[1].Body["chatId"] != "5511@c.us" {
		t.Errorf("presence = %+v", calls[1])
	}
	file, _ := calls[2].Body["file"].(map[string]any)
	if calls[2].Path != "/api/sendVoice" || calls[2].Body["convert"] != true ||
		file["mimetype"] != "audio/mpeg" || file["data"] != base64.StdEncoding.EncodeToString([]byte("MP3")) {
		t.Errorf("sendVoice = %+v", calls[2])
	}
}

func TestHumanEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := waha.New(srv.URL, "key", "").SendVoice(ctx, "x@c.us", []byte("a"), "audio/mpeg"); err == nil {
		t.Fatal("esperava erro")
	}
}
