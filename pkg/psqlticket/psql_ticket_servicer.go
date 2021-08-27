package psqlticket

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
	"github.com/welthee/dinonce/v2/pkg/ticket"
	"math"
	"math/rand"
	"time"
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

func (p *Servicer) CreateLineage(request *api.LineageCreationRequest) (*api.LineageCreationResponse, error) {
	aUuid, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	if request.StartLeasingFrom == nil {
		zero := 0
		request.StartLeasingFrom = &zero
	}

	rows, err := p.db.QueryContext(context.TODO(), queryStringInsertLineage,
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
	defer rowClose(rows)

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

	log.Info().
		Str("id", lineageId).
		Str("extId", request.ExtId).
		Msg("created lineage")

	return resp, nil
}

func (p *Servicer) GetLineage(extId string) (*api.LineageGetResponse, error) {
	var id string
	var nextNonce int
	var leasedNonceCount int
	var releasedNonceCount int
	var maxLeasedNonceCount int
	var maxNonceValue int
	var version int

	err := p.db.QueryRowContext(context.TODO(), queryStringSelectLineageByExtId, extId).
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

	log.Info().
		Str("lineageId", id).
		Str("extId", extId).
		Int("version", version).
		Msg("retrieved lineage")

	return resp, nil
}

func (p *Servicer) LeaseTicket(lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error) {
	var err error
	shouldRetry := true
	nonce := 0

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		nonce, shouldRetry, err = p.tryLeaseTicket(lineageId, request)
		if err != nil {
			if shouldRetry {
				log.Info().
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
	}

	log.Info().
		Str("lineageId", resp.LineageId).
		Str("extId", resp.ExtId).
		Int("nonce", resp.Nonce).
		Msg("leased ticket")

	return resp, nil
}

func (p *Servicer) tryLeaseTicket(lineageId string, request *api.TicketLeaseRequest) (int, bool, error) {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return 0, false, err
	}

	rows, err := p.db.QueryContext(context.TODO(), queryStringCreateTicket, lineageId, version, request.ExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Message {
			case sqlErrMessageValidationError:
				return 0, false, ticket.ErrInvalidRequest
			case sqlErrMessageMaxUnusedLimitExceeded:
				log.Info().
					Str("lineageId", lineageId).
					Str("extId", request.ExtId).
					Msg("can not lease ticket, too many leased tickets in lineage")

				return 0, false, ticket.ErrTooManyLeasedTickets
			case sqlErrMessageOptimisticLock:
				log.Debug().
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
	defer rowClose(rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return 0, false, err
	}

	return *nonce, false, nil
}

func (p *Servicer) GetTicket(lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error) {
	var nonce int
	var stateStr string

	err := p.db.QueryRowContext(context.TODO(), queryStringSelectTicket, lineageId, ticketExtId).
		Scan(&nonce, &stateStr)
	if err != nil {
		return nil, err
	}

	resp := &api.TicketLeaseResponse{
		ExtId:     ticketExtId,
		LineageId: lineageId,
		Nonce:     nonce,
		State:     api.TicketLeaseResponseState(stateStr),
	}

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Msg("retrieved ticket")

	return resp, nil
}

func (p *Servicer) ReleaseTicket(lineageId string, ticketExtId string) error {
	var err error
	shouldRetry := true

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		shouldRetry, err = p.tryReleaseTicket(lineageId, ticketExtId)
		if err != nil {
			if shouldRetry {
				log.Info().
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

func (p *Servicer) tryReleaseTicket(lineageId string, ticketExtId string) (bool, error) {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return false, err
	}

	rows, err := p.db.QueryContext(context.TODO(), queryStringReleaseTicket, lineageId, version, ticketExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Message {
			case sqlErrMessageNoSuchTicket:
				log.Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("ticket not found")

				return false, ticket.ErrNoSuchTicket
			case sqlErrMessageOptimisticLock:
				log.Debug().
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
	defer rowClose(rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return false, err
	}

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Int("nonce", *nonce).
		Msg("released ticket")

	return false, nil
}

func (p *Servicer) CloseTicket(lineageId string, ticketExtId string) error {
	var err error
	shouldRetry := true

	for attempt := 1; shouldRetry && attempt <= optimisticLockMaxRetryAttempts; attempt++ {
		shouldRetry, err = p.tryCloseTicket(lineageId, ticketExtId)
		if err != nil {
			if shouldRetry {
				log.Info().
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

func (p *Servicer) tryCloseTicket(lineageId string, ticketExtId string) (bool, error) {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return false, err
	}

	_, err = p.db.ExecContext(context.TODO(), queryStringCloseTicket, lineageId, version, ticketExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Message {
			case sqlErrMessageNoSuchTicket:
				log.Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("ticket not found")

				return false, ticket.ErrNoSuchTicket
			case sqlErrMessageAlreadyClosed:
				log.Info().
					Str("lineageId", lineageId).
					Str("extId", ticketExtId).
					Msg("not closing ticket, was already closed")

				return false, nil
			case sqlErrMessageOptimisticLock:
				log.Debug().
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

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Msg("closed ticket")
	return false, nil
}

func (p *Servicer) getLineageVersion(lineageId string) (int64, error) {
	rows, err := p.db.QueryContext(context.TODO(), queryStringSelectLineageVersion, lineageId)
	if err != nil {
		return 0, err
	}
	if !rows.Next() {
		return 0, ticket.ErrNoSuchLineage
	}
	defer rowClose(rows)

	var v int64
	if err := rows.Scan(&v); err != nil {
		return 0, err
	}

	return v, nil
}

func getNonceFromRow(rows *sql.Rows) (*int, error) {
	var nonce int
	if !rows.Next() {
		return nil, errors.New("expected nonce in result set")
	}
	if err := rows.Scan(&nonce); err != nil {
		return nil, err
	}

	return &nonce, nil
}

func rowClose(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		log.Error().Err(err).Msg("can't close rows")
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
