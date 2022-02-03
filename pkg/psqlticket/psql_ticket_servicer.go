package psqlticket

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
	"github.com/welthee/dinonce/v2/pkg/ticket"
)

// Optimistic lock retry constants
const (
	optimisticLockMaxRetryAttempts  = 5
	optimisticLockJitterSleepFactor = 2
	optimisticLockSleepBase         = 10 * time.Millisecond
	optimisticLockSleepMax          = 1 * time.Second
)

// SQL Custom Errors
const (
	sqlErrConstraintLineagesExtIdx = "lineages_ext_id_idx"

	sqlErrMessageValidationError        = "validation_error"
	sqlErrMessageMaxUnusedLimitExceeded = "max_unused_limit_exceeded"
	sqlErrMessageOptimisticLock         = "optimistic_lock"
	sqlErrMessageNoSuchTicket           = "no_such_ticket"
	sqlErrMessageAlreadyClosed          = "already_closed"
)

// Queries
const (
	queryStringInsertLineage = `insert into lineages(id, ext_id, next_nonce, leased_nonce_count, 
released_nonce_count, max_leased_nonce_count, max_nonce_value, version) 
values ($1, $2, $3, 0, 0, $4, 9223372036854775807, 0) 
returning id;`

	queryStringSelectLineageByExtId = `select id, next_nonce, leased_nonce_count, 
released_nonce_count, max_leased_nonce_count, max_nonce_value, version from lineages where ext_id = $1`

	queryStringCreateTicket = `select create_ticket($1, $2, $3);`

	queryStringReleaseTicket = `select release_ticket($1, $2, $3);`

	queryStringCloseTicket = `select close_ticket($1, $2, $3);`

	queryStringSelectLineageVersion = `select version from lineages where id = $1;`

	queryStringSelectTicket = `select nonce, lease_status from tickets where lineage_id = $1 and ext_id = $2`
)

type Servicer struct {
	db *sql.DB
}


func NewServicer(db *sql.DB) ticket.Servicer {
	return &Servicer{
		db: db,
	}
}

func (p *Servicer) CreateLineage(ctx context.Context, request *api.LineageCreationRequest) (
	*api.LineageCreationResponse, error) {

	aUuid, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	if request.StartLeasingFrom == nil {
		zero := 0
		request.StartLeasingFrom = &zero
	}

	rows, err := p.db.QueryContext(ctx, queryStringInsertLineage,
		aUuid.String(), request.ExtId, request.StartLeasingFrom, request.MaxLeasedNonceCount)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Constraint {
			case sqlErrConstraintLineagesExtIdx:
				return nil, ticket.ErrInvalidRequest
			default:
				return nil, err
			}
		}
		return nil, err
	}
	defer rowClose(ctx, rows)

	if !rows.Next() {
		return nil, errors.New("expected newly created lineage_id in result set")
	}

	var lineageId string
	if err := rows.Scan(&lineageId); err != nil {
		return nil, err
	}

	resp := &api.LineageCreationResponse{
		Id:    lineageId,
		ExtId: request.ExtId,
	}

	log.Ctx(ctx).Info().
		Str("id", lineageId).
		Str("extId", request.ExtId).
		Msg("created lineage")

	return resp, nil
}

func (p *Servicer) GetLineage(ctx context.Context, extId string) (*api.LineageGetResponse, error) {
	var id string
	var nextNonce int
	var leasedNonceCount int
	var releasedNonceCount int
	var maxLeasedNonceCount int
	var maxNonceValue int
	var version int

	err := p.db.QueryRowContext(ctx, queryStringSelectLineageByExtId, extId).
		Scan(&id, &nextNonce, &leasedNonceCount,
			&releasedNonceCount, &maxLeasedNonceCount, &maxNonceValue, &version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ticket.ErrNoSuchLineage
		}

		return nil, err
	}

	resp := &api.LineageGetResponse{
		Id:                  id,
		ExtId:               extId,
		NextNonce:           nextNonce,
		LeasedNonceCount:    leasedNonceCount,
		ReleasedNonceCount:  releasedNonceCount,
		MaxLeasedNonceCount: maxLeasedNonceCount,
		MaxNonceValue:       maxNonceValue,
	}

	log.Ctx(ctx).Info().
		Str("lineageId", id).
		Str("extId", extId).
		Int("version", version).
		Msg("retrieved lineage")

	return resp, nil
}

func (p *Servicer) LeaseTickets(ctx context.Context, lineageId string, requests *api.TicketBulkLeaseRequest) (*api.TicketLeaseResponseList, error) {
	responseList := api.TicketLeaseResponseList{Leases: &[]api.TicketLeaseResponse{}}
	var hasFailedToLeaseTicket bool

	for _, extID := range requests.ExtIds {
		resp, err := p.LeaseTicket(ctx, lineageId, &api.TicketLeaseRequest{ExtId: extID})
		if err != nil {
			err := p.ReleaseTicket(ctx, lineageId, extID)
			if err != nil {
				log.Ctx(ctx).Error().Err(err).Msg("")
			}
			hasFailedToLeaseTicket = true
			break
		}
		*responseList.Leases = append(*responseList.Leases, *resp)
	}

	if hasFailedToLeaseTicket {
		for _, response := range *responseList.Leases {
			err := p.ReleaseTicket(ctx, lineageId, response.ExtId)
			if err != nil {
				log.Ctx(ctx).Error().Err(err).Msg("")
			}
		}
	}

	return &responseList, nil
}

func (p *Servicer) LeaseTicket(ctx context.Context, lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error) {
	var err error
	shouldRetry := true
	nonce := 0

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		nonce, shouldRetry, err = p.tryLeaseTicket(ctx, lineageId, request)
		if err != nil {
			if shouldRetry {
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", request.ExtId).
					Msg("retrying to lease ticket")

				jitterSleep(attempt, optimisticLockSleepBase, optimisticLockSleepMax)
			} else {
				return nil, err
			}
		}
	}

	resp := &api.TicketLeaseResponse{
		LineageId: lineageId,
		ExtId:     request.ExtId,
		Nonce:     nonce,
		State:     api.TicketLeaseResponseStateLeased,
	}

	log.Ctx(ctx).Info().
		Str("lineageId", resp.LineageId).
		Str("extId", resp.ExtId).
		Int("nonce", resp.Nonce).
		Msg("leased ticket")

	return resp, nil
}

func (p *Servicer) tryLeaseTicket(ctx context.Context, lineageId string, request *api.TicketLeaseRequest) (int, bool, error) {
	version, err := p.getLineageVersion(ctx, lineageId)
	if err != nil {
		return 0, false, err
	}

	array := pq.Array([1]string{request.ExtId})

	// select create_ticket('5747600c-c493-456c-bec9-cad43eb90b8a', 55, {"foo"});

	rows, err := p.db.QueryContext(ctx, queryStringCreateTicket, lineageId, version, array)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Code {
			// 22P02 INVALID TEXT REPRESENTATION
			case "22P02":
				log.Ctx(ctx).Error().
					Str("lineageId", lineageId).
					Str("extId", request.ExtId).
					Str("pqErr Where", pqErr.Where).
					Err(err).
					Msg("")
				return 0, false, ticket.ErrInvalidRequest
			}

			switch pqErr.Message {
			case sqlErrMessageValidationError:
				return 0, false, ticket.ErrInvalidRequest
			case sqlErrMessageMaxUnusedLimitExceeded:
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", request.ExtId).
					Msg("can not lease ticket, too many leased tickets in lineage")

				return 0, false, ticket.ErrTooManyLeasedTickets
			case sqlErrMessageOptimisticLock:
				log.Ctx(ctx).Debug().
					Str("lineageId", lineageId).
					Str("extId", request.ExtId).
					Msg("can not lease ticket due to too many concurrent requests(optimistic lock)")

				return 0, true, ticket.ErrTooManyConcurrentRequests
			default:
				return 0, false, err
			}
		}
		return 0, false, err
	}
	defer rowClose(ctx, rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return 0, false, err
	}

	return *nonce, false, nil
}

func (p *Servicer) GetTicket(ctx context.Context, lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error) {
	var nonce int
	var stateStr string

	row := p.db.QueryRowContext(ctx, queryStringSelectTicket, lineageId, ticketExtId)

	if err := row.Err(); err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Code {
			// 22P02 INVALID TEXT REPRESENTATION
			case "22P02":
				return nil, ticket.ErrInvalidRequest
			}
		}

		return nil, err
	}

	if err := row.Scan(&nonce, &stateStr); err != nil {
		if err == sql.ErrNoRows {
			return nil, ticket.ErrNoSuchTicket
		}

		return nil, err
	}

	resp := &api.TicketLeaseResponse{
		ExtId:     ticketExtId,
		LineageId: lineageId,
		Nonce:     nonce,
		State:     api.TicketLeaseResponseState(stateStr),
	}

	log.Ctx(ctx).Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Msg("retrieved ticket")

	return resp, nil
}

func (p *Servicer) ReleaseTicket(ctx context.Context, lineageId string, ticketExtId string) error {
	var err error
	shouldRetry := true

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		shouldRetry, err = p.tryReleaseTicket(ctx, lineageId, ticketExtId)
		if err != nil {
			if shouldRetry {
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("retrying to release ticket")

				jitterSleep(attempt, optimisticLockSleepBase, optimisticLockSleepMax)
			} else {
				return err
			}
		}
	}

	return nil
}

func (p *Servicer) tryReleaseTicket(ctx context.Context, lineageId string, ticketExtId string) (bool, error) {
	version, err := p.getLineageVersion(ctx, lineageId)
	if err != nil {
		return false, err
	}

	rows, err := p.db.QueryContext(ctx, queryStringReleaseTicket, lineageId, version, ticketExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Code {
			// 22P02 INVALID TEXT REPRESENTATION
			case "22P02":
				return false, ticket.ErrInvalidRequest
			}

			switch pqErr.Message {
			case sqlErrMessageNoSuchTicket:
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("ticket not found")

				return false, ticket.ErrNoSuchTicket
			case sqlErrMessageOptimisticLock:
				log.Ctx(ctx).Debug().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("can not release due to too many concurrent requests(optimistic lock)")

				return true, ticket.ErrTooManyConcurrentRequests
			default:
				return false, err
			}
		}
		return false, err
	}
	defer rowClose(ctx, rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return false, err
	}

	log.Ctx(ctx).Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Int("nonce", *nonce).
		Msg("released ticket")

	return false, nil
}

func (p *Servicer) CloseTicket(ctx context.Context, lineageId string, ticketExtId string) error {
	var err error
	shouldRetry := true

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		shouldRetry, err = p.tryCloseTicket(ctx, lineageId, ticketExtId)
		if err != nil {
			if shouldRetry {
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("retrying to close ticket")

				jitterSleep(attempt, optimisticLockSleepBase, optimisticLockSleepMax)
			} else {
				return err
			}
		}
	}

	return nil
}

func (p *Servicer) tryCloseTicket(ctx context.Context, lineageId string, ticketExtId string) (bool, error) {
	version, err := p.getLineageVersion(ctx, lineageId)
	if err != nil {
		return false, err
	}

	_, err = p.db.ExecContext(ctx, queryStringCloseTicket, lineageId, version, ticketExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Code {
			// 22P02 INVALID TEXT REPRESENTATION
			case "22P02":
				return false, ticket.ErrInvalidRequest
			}

			switch pqErr.Message {
			case sqlErrMessageNoSuchTicket:
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("ticket not found")

				return false, ticket.ErrNoSuchTicket
			case sqlErrMessageAlreadyClosed:
				log.Ctx(ctx).Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("not closing ticket, was already closed")

				return false, nil
			case sqlErrMessageOptimisticLock:
				log.Ctx(ctx).Debug().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("can not close due to too many concurrent requests(optimistic lock)")

				return true, ticket.ErrTooManyConcurrentRequests
			default:
				return false, err
			}
		}
		return false, err
	}

	log.Ctx(ctx).Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Msg("closed ticket")
	return false, nil
}

func (p *Servicer) getLineageVersion(ctx context.Context, lineageId string) (int64, error) {
	rows, err := p.db.QueryContext(ctx, queryStringSelectLineageVersion, lineageId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Code {
			// 22P02 INVALID TEXT REPRESENTATION
			case "22P02":
				return 0, ticket.ErrInvalidRequest
			}
		}
		return 0, err
	}

	if !rows.Next() {
		return 0, ticket.ErrNoSuchLineage
	}
	defer rowClose(ctx, rows)

	var v int64
	if err := rows.Scan(&v); err != nil {
		return 0, err
	}

	return v, nil
}

func getNonceFromRow(rows *sql.Rows) (*int, error) {
	var nonces []int
	if !rows.Next() {
		return nil, errors.New("expected nonce in result set")
	}
	if err := rows.Scan(pq.Array(&nonces)); err != nil {
		return nil, err
	}

	return &nonces[0], nil
}

func rowClose(ctx context.Context, rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("can't close rows")
	}
}

func jitterSleep(attempt int, base, max time.Duration) {
	mx := float64(max)
	mn := float64(base)

	dur := mn * math.Pow(optimisticLockJitterSleepFactor, float64(attempt))
	if dur > mx {
		dur = mx
	}
	j := time.Duration(rand.Float64()*(dur-mn) + mn)
	time.Sleep(j)
}
