package ticket

import (
	"context"
	"errors"

	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
)

var (
	ErrNoSuchLineage             = errors.New("no such lineage")
	ErrNoSuchTicket              = errors.New("no such ticket")
	ErrInvalidRequest            = errors.New("invalid request")
	ErrTooManyLeasedTickets      = errors.New("too many leased tickets")
	ErrTooManyConcurrentRequests = errors.New("too many concurrent requests")
)

type Servicer interface {
	CreateLineage(ctx context.Context, request *api.LineageCreationRequest) (*api.LineageCreationResponse, error)
	GetLineage(ctx context.Context, extId string) (*api.LineageGetResponse, error)
	LeaseTickets(ctx context.Context, lineageId string, requests *api.TicketBulkLeaseRequest) (*api.TicketLeaseResponseList, error)
	LeaseTicket(ctx context.Context, lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error)
	GetTicket(ctx context.Context, lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error)
	ReleaseTicket(ctx context.Context, lineageId string, ticketExtId string) error
	CloseTicket(ctx context.Context, lineageId string, ticketExtId string) error
}
