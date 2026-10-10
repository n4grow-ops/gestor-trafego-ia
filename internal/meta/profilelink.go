package meta

import (
	"context"
	"errors"
	"net/http"
)

var errProfileTODO = errors.New("meta: ainda não implementado")

// ProfileRef é o perfil do Facebook que o cliente mandou: ID numérico ou nome de usuário.
type ProfileRef struct {
	Kind  string // "id" | "username" | "share" (link de compartilhamento ainda não resolvido)
	Value string // ID, nome de usuário ou a URL de compartilhamento
	URL   string // link original, normalizado
}

// ParseProfileLink acha um link de perfil do Facebook no texto.
func ParseProfileLink(text string) (ProfileRef, bool) { return ProfileRef{}, false }

// ResolveShareLink segue o redirect de um link de compartilhamento até o perfil.
func ResolveShareLink(ctx context.Context, hc *http.Client, url string) (string, error) {
	return "", errProfileTODO
}

// AppTesters devolve os IDs (do app) dos testadores que já aceitaram o convite.
func (c *Client) AppTesters(ctx context.Context) ([]string, error) { return nil, errProfileTODO }
