package store

import (
	"context"
	"errors"
	"time"
)

var errTesterTODO = errors.New("store: testador ainda não implementado")

// Status do pedido de testador.
const (
	TesterRequested = "requested"
	TesterAdded     = "added"
	TesterAccepted  = "accepted"
	TesterCanceled  = "canceled"
)

// TesterRequest é o pedido para incluir o cliente como testador do app da Meta.
type TesterRequest struct {
	ID             int64
	OrgID          int64
	IdentityID     int64
	ConversationID int64
	ProfileURL     string
	RefKind        string // id | username
	RefValue       string
	Status         string
	RequestedAt    time.Time
	AddedAt        *time.Time
	AddedBy        string
	AcceptedAt     *time.Time
	RemindedAt     *time.Time
}

// TesterRequestView é a linha da fila no painel.
type TesterRequestView struct {
	TesterRequest
	MemberName string
	OrgName    string
	Phone      string
}

// UpsertTesterRequest abre (ou atualiza o perfil do) pedido aberto da identidade.
func (s *Store) UpsertTesterRequest(ctx context.Context, r TesterRequest) (TesterRequest, error) {
	return TesterRequest{}, errTesterTODO
}

// OpenTesterRequest devolve o pedido requested/added da identidade (ErrNotFound se não houver).
func (s *Store) OpenTesterRequest(ctx context.Context, identityID int64) (TesterRequest, error) {
	return TesterRequest{}, errTesterTODO
}

// ListTesterRequests lista a fila (status vazio = abertos: requested e added), mais antigos primeiro.
func (s *Store) ListTesterRequests(ctx context.Context, status string) ([]TesterRequestView, error) {
	return nil, errTesterTODO
}

// MarkTesterAdded passa de requested para added (ErrNotFound se não estiver requested).
func (s *Store) MarkTesterAdded(ctx context.Context, id int64, by string) (TesterRequestView, error) {
	return TesterRequestView{}, errTesterTODO
}

// MarkTesterAcceptedByIdentity fecha como accepted o pedido aberto da identidade (sem pedido: nada).
func (s *Store) MarkTesterAcceptedByIdentity(ctx context.Context, identityID int64) error {
	return errTesterTODO
}

// TestersAwaitingAcceptance lista os pedidos added sem conexão Meta ativa na organização.
func (s *Store) TestersAwaitingAcceptance(ctx context.Context) ([]TesterRequestView, error) {
	return nil, errTesterTODO
}

// MarkTesterReminded grava o lembrete (uma vez só).
func (s *Store) MarkTesterReminded(ctx context.Context, id int64) error { return errTesterTODO }

// SyncAppTesters grava os testadores vistos na Meta e devolve os que não eram conhecidos.
// Na primeira chamada (tabela vazia) grava todos e devolve nenhum.
func (s *Store) SyncAppTesters(ctx context.Context, userIDs []string) ([]string, error) {
	return nil, errTesterTODO
}
