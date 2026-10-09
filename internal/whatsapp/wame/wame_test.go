package wame_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

func envelope(field, value string) []byte {
	return []byte(`{"object":"wame","provider":"whatsapp","instance":"inst","official":false,"entry":[{"id":"wame.x","changes":[{"field":"` + field + `","value":` + value + `}]}]}`)
}

func msg(m string) []byte {
	return envelope("messages", `{"messaging_product":"whatsapp","metadata":{"display_phone_number":"5511971214993","phone_number_id":"inst"},"contacts":[{"profile":{"name":"Fulano"},"wa_id":"5511999998888"}],"messages":[`+m+`]}`)
}

var c = wame.New("https://server.wame.api.br", "chave", "")

func TestParseText(t *testing.T) {
	in, ok, err := c.ParseWebhook(msg(`{"from":"5511999998888","chat_type":"individual","id":"W1","timestamp":"1700000000","type":"text","text":{"body":"Olá!"}}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if in.ExternalID != "W1" || in.Phone != "5511999998888" || in.ChatID != "5511999998888" || in.Kind != whatsapp.KindText || in.Text != "Olá!" {
		t.Fatalf("inbound = %+v", in)
	}
}

// A Cloud API manda mensagens de contato individual sem chat_type; só "group" fica de fora.
func TestParseWithoutChatType(t *testing.T) {
	in, ok, err := c.ParseWebhook(msg(`{"from":"5511999998888","id":"W9","type":"text","text":{"body":"oi"}}`))
	if err != nil || !ok || in.ExternalID != "W9" {
		t.Fatalf("in=%+v ok=%v err=%v", in, ok, err)
	}
}

func TestParseNormalizesNinthDigit(t *testing.T) {
	in, ok, _ := c.ParseWebhook(msg(`{"from":"551199998888","chat_type":"individual","id":"W2","type":"text","text":{"body":"oi"}}`))
	if !ok || in.Phone != "5511999998888" || in.ChatID != "551199998888" {
		t.Fatalf("phone=%q chat=%q ok=%v", in.Phone, in.ChatID, ok)
	}
}

func TestParseMedia(t *testing.T) {
	cases := []struct {
		m    string
		kind whatsapp.Kind
		text string
	}{
		{`{"from":"5511999998888","chat_type":"individual","id":"A","type":"audio","audio":{"id":"A","mime_type":"audio/ogg","voice":true}}`, whatsapp.KindAudio, ""},
		{`{"from":"5511999998888","chat_type":"individual","id":"I","type":"image","image":{"id":"I","mime_type":"image/jpeg","caption":"foto"}}`, whatsapp.KindImage, "foto"},
		{`{"from":"5511999998888","chat_type":"individual","id":"V","type":"video","video":{"id":"V","mime_type":"video/mp4","caption":"vid"}}`, whatsapp.KindVideo, "vid"},
		{`{"from":"5511999998888","chat_type":"individual","id":"S","type":"sticker","sticker":{"id":"S"}}`, whatsapp.KindOther, ""},
	}
	for _, tc := range cases {
		in, ok, err := c.ParseWebhook(msg(tc.m))
		if err != nil || !ok || in.Kind != tc.kind || in.Text != tc.text {
			t.Errorf("%s: in=%+v ok=%v err=%v", tc.m, in, ok, err)
		}
	}
}

func TestParseIgnores(t *testing.T) {
	ignored := [][]byte{
		msg(`{"from":"5511777776666","chat_type":"group","group_id":"123@g.us","id":"G","type":"text","text":{"body":"oi"}}`),
		msg(`{"from":"5511999998888","chat_type":"individual","id":"OUT","type":"text","text":{"body":"minha"},"from_me":true}`),
		envelope("messages", `{"messaging_product":"whatsapp","statuses":[{"id":"W1","status":"read","recipient_id":"5511999998888"}]}`),
		envelope("connection", `{"connection":{"status":"close","code":1004,"reason":"disconnected"}}`),
		envelope("presence", `{}`),
		msg(`{"from":"","chat_type":"individual","id":"X","type":"text","text":{"body":"sem remetente"}}`),
	}
	for i, body := range ignored {
		if _, ok, err := c.ParseWebhook(body); ok || err != nil {
			t.Errorf("caso %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, _, err := c.ParseWebhook([]byte("{nope")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestParseConnection(t *testing.T) {
	ev, ok := c.ParseConnection(envelope("connection", `{"connection":{"status":"close","code":1004,"reason":"disconnected"}}`))
	if !ok || ev.Connected || ev.Code != 1004 || ev.Reason != "disconnected" {
		t.Fatalf("close: %+v ok=%v", ev, ok)
	}
	ev, ok = c.ParseConnection(envelope("connection", `{"connection":{"status":"open","code":200}}`))
	if !ok || !ev.Connected {
		t.Fatalf("open: %+v ok=%v", ev, ok)
	}
	if _, ok := c.ParseConnection(msg(`{"from":"5511999998888","chat_type":"individual","id":"W","type":"text","text":{"body":"oi"}}`)); ok {
		t.Fatal("mensagem não é conexão")
	}
}

func TestName(t *testing.T) {
	if c.Name() != "wame" {
		t.Fatal(c.Name())
	}
}
