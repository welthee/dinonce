package ticket

import (
	"errors"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
)

var (
	ErrorNoSuchLineage = errors.New("no such lineage")
	ErrorNoSuchTicket  = errors.New("no such ticket")
)

type Servicer interface {
	CreateLineage(request *api.LineageCreationRequest) (*api.LineageCreationResponse, error)
	LeaseTicket(lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error)
	GetTicket(lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error)
	ReleaseTicket(lineageId string, ticketExtId string) error
	CloseTicket(lineageId string, ticketExtId string) error
}
