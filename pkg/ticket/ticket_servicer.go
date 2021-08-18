package ticket

import (
	"errors"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
)

var (
	ErrNoSuchLineage        = errors.New("no such lineage")
	ErrNoSuchTicket         = errors.New("no such ticket")
	ErrInvalidRequest       = errors.New("invalid request")
	ErrTooManyLeasedTickets = errors.New("too many leased tickets")
	ErrTooManyConcurrentRequests = errors.New("too many concurrent requests")
)

type Servicer interface {
	CreateLineage(request *api.LineageCreationRequest) (*api.LineageCreationResponse, error)
	GetLineage(extId string) (*api.LineageGetResponse, error)
	LeaseTicket(lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error)
	GetTicket(lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error)
	ReleaseTicket(lineageId string, ticketExtId string) error
	CloseTicket(lineageId string, ticketExtId string) error
}
