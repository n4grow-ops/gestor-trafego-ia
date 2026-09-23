package openwa_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/openwa"
)

var ctx = context.Background()

func envelope(event, msg string) []byte {
	return []byte(`{"event":"` + event + `","sessionId":"gestor","data":` + msg + `}`)
}

const textMsg = `{"id":"false_5511988887777@c.us_ABC","from":"5511988887777@c.us","sender":{"id":"5511988887777@c.us"},"type":"chat","body":"quero anunciar","fromMe":false,"isGroupMsg":false}`

func parse(t *testing.T, body []byte) (whatsapp.Inbound, bool) {
	t.Helper()
	in, ok, err := openwa.New("http://x", "k").ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	return in, ok
}

func TestParseText(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", textMsg))
	if !ok || in.ExternalID != "false_5511988887777@c.us_ABC" || in.ChatID != "5511988887777@c.us" ||
		in.Phone != "5511988887777" || in.Kind != whatsapp.KindText || in.Text != "quero anunciar" || len(in.Raw) == 0 {
		t.Errorf("in = %+v ok = %v", in, ok)
	}
}

func TestParseAddsBrazilianNinthDigit(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", `{"id":"m1","from":"551188887777@c.us","type":"chat","body":"oi"}`))
	if !ok || in.Phone != "5511988887777" || in.ChatID != "551188887777@c.us" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseAudio(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", `{"id":"m2","from":"5511988887777@c.us","type":"ptt","body":"","mimetype":"audio/ogg; codecs=opus"}`))
	if !ok || in.Kind != whatsapp.KindAudio || in.Text != "" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseImageUsesCaptionNotThumbnail(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", `{"id":"m3","from":"5511988887777@c.us","type":"image","body":"/9j/4AAQSkZJRgABAQ","caption":"meu cardápio"}`))
	if !ok || in.Kind != whatsapp.KindImage || in.Text != "meu cardápio" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseOtherType(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", `{"id":"m4","from":"5511988887777@c.us","type":"sticker","body":"xxx"}`))
	if !ok || in.Kind != whatsapp.KindOther || in.Text != "" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseUsesSenderWhenFromIsLID(t *testing.T) {
	in, ok := parse(t, envelope("onMessage", `{"id":"m5","from":"99999@lid","sender":{"id":"5511988887777@c.us"},"type":"chat","body":"oi"}`))
	if !ok || in.Phone != "5511988887777" || in.ChatID != "99999@lid" {
		t.Errorf("in = %+v", in)
	}
}

func TestParseIgnores(t *testing.T) {
	cases := map[string][]byte{
		"outro evento":    envelope("onAck", `{"id":"a"}`),
		"enviada por nós": envelope("onMessage", `{"id":"m","from":"5511988887777@c.us","type":"chat","body":"x","fromMe":true}`),
		"grupo":           envelope("onMessage", `{"id":"m","from":"5511988887777@c.us","type":"chat","body":"x","isGroupMsg":true}`),
		"id de grupo":     envelope("onMessage", `{"id":"m","from":"12036302@g.us","type":"chat","body":"x"}`),
		"status":          envelope("onMessage", `{"id":"m","from":"status@broadcast","type":"chat","body":"x"}`),
		"só lid":          envelope("onMessage", `{"id":"m","from":"99999@lid","sender":{"id":"99999@lid"},"type":"chat","body":"oi"}`),
		"sem id":          envelope("onMessage", `{"from":"5511988887777@c.us","type":"chat","body":"x"}`),
	}
	for name, body := range cases {
		if _, ok := parse(t, body); ok {
			t.Errorf("%s: deveria ser ignorado", name)
		}
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, _, err := openwa.New("http://x", "k").ParseWebhook([]byte("{nope")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestSendText(t *testing.T) {
	var got struct {
		Args struct {
			To      string `json:"to"`
			Content string `json:"content"`
		} `json:"args"`
	}
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("api_key")
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"success":true,"response":"true_x"}`)
	}))
	defer srv.Close()

	if err := openwa.New(srv.URL, "k").SendText(ctx, "5511988887777@c.us", "olá"); err != nil {
		t.Fatal(err)
	}
	if path != "/sendText" || key != "k" || got.Args.To != "5511988887777@c.us" || got.Args.Content != "olá" {
		t.Errorf("path=%s key=%s body=%+v", path, key, got)
	}
}

func TestSendTextFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":false,"response":null}`)
	}))
	defer srv.Close()
	if err := openwa.New(srv.URL, "k").SendText(ctx, "x@c.us", "olá"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestSendTextHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	if err := openwa.New(srv.URL, "k").SendText(ctx, "x@c.us", "olá"); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestDownloadMedia(t *testing.T) {
	var got struct {
		Args struct {
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
		} `json:"args"`
	}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		data := base64.StdEncoding.EncodeToString([]byte("ogg-bytes"))
		io.WriteString(w, `{"success":true,"response":"data:audio/ogg; codecs=opus;base64,`+data+`"}`)
	}))
	defer srv.Close()

	c := openwa.New(srv.URL, "k")
	in, _, _ := c.ParseWebhook(envelope("onMessage", `{"id":"m2","from":"5511988887777@c.us","type":"ptt"}`))
	data, mime, err := c.DownloadMedia(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ogg-bytes" || mime != "audio/ogg" || path != "/decryptMedia" || got.Args.Message.ID != "m2" {
		t.Errorf("data=%q mime=%q path=%s body=%+v", data, mime, path, got)
	}
}

func TestChatIDForPhone(t *testing.T) {
	if got := openwa.New("http://x", "k").ChatIDForPhone("5511900000000"); got != "5511900000000@c.us" {
		t.Errorf("got %q", got)
	}
}
