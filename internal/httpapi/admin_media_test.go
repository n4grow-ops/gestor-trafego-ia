package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// Foto que o cliente mandou aparece no painel: o servidor baixa do provedor pela referência da mensagem.
func TestMessageMedia(t *testing.T) {
	e := newAdminEnv(t, "otp")
	ctx := context.Background()
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00foto")
	var asked string
	e.admin.Media = func(_ context.Context, ref string) ([]byte, string, error) {
		asked = ref
		if ref == "expirada" {
			return nil, "", errors.New("404")
		}
		return jpeg, "image/jpeg", nil
	}
	foto, err := e.st.AddInboundMedia(ctx, e.convID, "image", "[imagem enviada]", "wamid.FOTO")
	if err != nil {
		t.Fatal(err)
	}
	velha, err := e.st.AddInboundMedia(ctx, e.convID, "image", "[imagem enviada]", "expirada")
	if err != nil {
		t.Fatal(err)
	}
	texto, err := e.st.AddInbound(ctx, e.convID, "text", "oi")
	if err != nil {
		t.Fatal(err)
	}
	s := e.login("marina@meuanuncio.io")

	r := e.do("GET", fmt.Sprintf("/api/conversations/%d", e.convID), s, nil)
	msgs := r.body["thread"].(map[string]any)["messages"].([]any)
	media := map[float64]bool{}
	for _, m := range msgs {
		mm := m.(map[string]any)
		media[mm["id"].(float64)] = mm["has_media"].(bool)
	}
	if !media[float64(foto)] || media[float64(texto)] {
		t.Fatalf("has_media errado: %v", media)
	}

	r = e.do("GET", fmt.Sprintf("/api/messages/%d/media", foto), s, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "image/jpeg" || r.raw != string(jpeg) || asked != "wamid.FOTO" {
		t.Fatalf("foto: %d %q %q", r.code, r.hdr.Get("Content-Type"), asked)
	}
	if r.hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("sem nosniff")
	}
	if r = e.do("GET", fmt.Sprintf("/api/messages/%d/media", texto), s, nil); r.code != 404 {
		t.Errorf("mensagem de texto: %d", r.code)
	}
	if r = e.do("GET", fmt.Sprintf("/api/messages/%d/media", velha), s, nil); r.code != 502 {
		t.Errorf("mídia expirada: %d", r.code)
	}
	if r = e.do("GET", fmt.Sprintf("/api/messages/%d/media", foto), nil, nil); r.code != 401 {
		t.Errorf("sem login: %d", r.code)
	}
}

// Provedor devolve outra coisa (ex.: HTML) numa mensagem de foto: desce como arquivo, nunca como página.
func TestMessageMediaWrongType(t *testing.T) {
	e := newAdminEnv(t, "otp")
	e.admin.Media = func(context.Context, string) ([]byte, string, error) {
		return []byte("<html><script>alert(1)</script></html>"), "text/html", nil
	}
	id, err := e.st.AddInboundMedia(context.Background(), e.convID, "image", "[imagem enviada]", "x")
	if err != nil {
		t.Fatal(err)
	}
	r := e.do("GET", fmt.Sprintf("/api/messages/%d/media", id), e.login("marina@meuanuncio.io"), nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "application/octet-stream" || r.hdr.Get("Content-Disposition") != "attachment" {
		t.Fatalf("%d %q %q", r.code, r.hdr.Get("Content-Type"), r.hdr.Get("Content-Disposition"))
	}
}
