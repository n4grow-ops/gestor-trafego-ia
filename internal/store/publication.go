package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrAlreadyPublished: o rascunho já tem publicação (em andamento ou concluída).
var ErrAlreadyPublished = errors.New("store: rascunho já publicado")

var errPublicationTODO = errors.New("store: publicação ainda não implementada")

// Publication é a campanha criada na conta de anúncios do cliente.
type Publication struct {
	ID           int64
	OrgID        int64
	DraftID      int64
	ProjectID    *int64
	ConnectionID int64
	Status       string // publishing | paused | active | failed
	CampaignID   string
	AdSetID      string
	AdIDs        []string
	Warnings     []string
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ActivatedAt  *time.Time
}

// PublicationResult são os IDs devolvidos pela Meta.
type PublicationResult struct {
	CampaignID string
	AdSetID    string
	AdIDs      []string
	Warnings   []string
}

// PublishData é o que a publicação lê do rascunho aprovado.
type PublishData struct {
	DraftID     int64
	OrgID       int64
	ProjectID   *int64
	OrgName     string
	ProjectName string
	Proposal    json.RawMessage // contract.CampaignProposal
	Fields      json.RawMessage // contract.Fields (offer, location...)
	ClientCopy  string
}

// MetaTokenSealed devolve o token cifrado da conexão ativa (ErrNotFound sem conexão).
func (s *Store) MetaTokenSealed(ctx context.Context, orgID int64) (connID int64, sealed []byte, err error) {
	return 0, nil, errPublicationTODO
}

// StartPublication abre a publicação do rascunho; se já existe uma que não falhou, devolve ela com ErrAlreadyPublished.
func (s *Store) StartPublication(ctx context.Context, orgID, draftID int64, projectID *int64, connID int64) (Publication, error) {
	return Publication{}, errPublicationTODO
}

// FinishPublication grava os IDs e deixa a publicação pausada.
func (s *Store) FinishPublication(ctx context.Context, id int64, r PublicationResult) error {
	return errPublicationTODO
}

// FailPublication marca a falha com a mensagem técnica.
func (s *Store) FailPublication(ctx context.Context, id int64, msg string) error {
	return errPublicationTODO
}

// SetPublicationStatus muda para active ou paused (active grava activated_at).
func (s *Store) SetPublicationStatus(ctx context.Context, id int64, status string) error {
	return errPublicationTODO
}

// LatestPublication devolve a publicação mais recente da organização (ErrNotFound se nenhuma).
func (s *Store) LatestPublication(ctx context.Context, orgID int64) (Publication, error) {
	return Publication{}, errPublicationTODO
}

// AppendClientCopy acumula referências de headline/texto do cliente no rascunho (até 2000 runas).
func (s *Store) AppendClientCopy(ctx context.Context, draftID int64, text string) error {
	return errPublicationTODO
}

// PublishData lê o rascunho aprovado para publicar.
func (s *Store) PublishData(ctx context.Context, draftID int64) (PublishData, error) {
	return PublishData{}, errPublicationTODO
}
