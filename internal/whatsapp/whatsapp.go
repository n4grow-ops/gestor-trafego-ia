// Package whatsapp define o contrato do canal. O adaptador desta etapa é o WAHA;
// a Cloud API oficial entra depois implementando a mesma interface.
package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Kind string

const (
	KindText  Kind = "text"
	KindAudio Kind = "audio"
	KindImage Kind = "image"
	KindVideo Kind = "video"
	KindOther Kind = "other"
)

type Inbound struct {
	ExternalID string
	ChatID     string // destino para responder, no formato do provedor
	Phone      string // telefone canônico (ver NormalizePhone)
	Kind       Kind
	Text       string // texto ou legenda
	Raw        json.RawMessage
}

// Presence é o que o cliente vê no topo da conversa.
type Presence string

const (
	PresenceTyping    Presence = "typing"
	PresenceRecording Presence = "recording"
	PresencePaused    Presence = "paused"
)

type Provider interface {
	Name() string
	// ParseWebhook devolve ok=false para o que não é mensagem de cliente (grupo, status, enviadas por nós).
	ParseWebhook(body []byte) (in Inbound, ok bool, err error)
	SendText(ctx context.Context, chatID, text string) error
	// MarkSeen marca as mensagens do chat como lidas (visto azul).
	MarkSeen(ctx context.Context, chatID string) error
	// SetPresence mostra "digitando…"/"gravando áudio…" ou limpa (paused).
	SetPresence(ctx context.Context, chatID string, p Presence) error
	// SendVoice envia mensagem de voz; o provedor converte o áudio para o formato do WhatsApp.
	SendVoice(ctx context.Context, chatID string, audio []byte, mime string) error
	// SendImage envia uma imagem com legenda (ex.: prévia do anúncio).
	SendImage(ctx context.Context, chatID string, image []byte, mime, caption string) error
	DownloadMedia(ctx context.Context, in Inbound) (data []byte, mimeType string, err error)
	// ChatIDForPhone monta o destino para um telefone canônico (usado para avisar o operador).
	ChatIDForPhone(phone string) string
}

var nonDigits = regexp.MustCompile(`\D`)

// NormalizePhone deixa só dígitos e insere o nono dígito em celular brasileiro que chega sem ele
// (55 + DDD + 8 dígitos começando em 6–9), para o cadastro e a mensagem casarem.
func NormalizePhone(s string) (string, error) {
	d := nonDigits.ReplaceAllString(s, "")
	if len(d) < 10 || len(d) > 15 {
		return "", fmt.Errorf("telefone inválido: %q", s)
	}
	if len(d) == 12 && strings.HasPrefix(d, "55") && d[4] >= '6' {
		d = d[:4] + "9" + d[4:]
	}
	return d, nil
}

// Digits só remove o que não é dígito, sem mexer no nono dígito. Usado para destinos
// que precisam casar com o número exatamente como está registrado no WhatsApp.
func Digits(s string) string { return nonDigits.ReplaceAllString(s, "") }
