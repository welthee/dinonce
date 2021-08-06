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
)

const QueryStringInsertLineage = `insert into lineages(id, ext_id, next_nonce, leased_nonce_count, 
released_nonce_count, max_leased_nonce_count, max_nonce_value, version) 
values ($1, $2, $3, 0, 0, $4, 9223372036854775807, 0) 
returning id;`

const QueryStringSelectLineageByExtId = `select id, next_nonce, leased_nonce_count, 
released_nonce_count, max_leased_nonce_count, max_nonce_value, version from lineages where ext_id = $1`

const QueryStringCreateTicket = `select create_ticket($1, $2, $3);`
const QueryStringReleaseTicket = `select release_ticket($1, $2, $3);`
const QueryStringCloseTicket = `select close_ticket($1, $2, $3);`
const QueryStringSelectLineageVersion = `select version from lineages where id = $1;`
const QueryStringSelectTicket = `select nonce,leased_at, lease_status from tickets where lineage_id = $1 and ext_id = $2`

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

	rows, err := p.db.QueryContext(context.TODO(), QueryStringInsertLineage,
		aUuid.String(), request.ExtId, request.StartLeasingFrom, request.MaxLeasedNonceCount)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Constraint {
			case "lineages_ext_id_idx":
				return nil, ticket.ErrInvalidRequest
			default:
				return nil, err
			}
		}
		return nil, err
	}

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

	err := p.db.QueryRowContext(context.TODO(), QueryStringSelectLineageByExtId, extId).
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
		Version:             version,
	}

	log.Info().
		Str("lineageId", id).
		Str("extId", extId).
		Int("version", version).
		Msg("retrieved lineage")

	return resp, nil
}

func (p *Servicer) LeaseTicket(lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error) {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return nil, err
	}

	rows, err := p.db.QueryContext(context.TODO(), QueryStringCreateTicket, lineageId, version, request.ExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok {
			switch pqErr.Message {
			case "validation_error":
				return nil, ticket.ErrInvalidRequest
			case "max_unused_limit_exceeded":
				return nil, ticket.ErrTooManyLeasedTickets
			default:
				return nil, err
			}
		}
		return nil, err
	}

	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Err(err).Msg("can't close rows")
		}
	}(rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return nil, err
	}

	resp := &api.TicketLeaseResponse{
		LineageId: lineageId,
		ExtId:     request.ExtId,
		Nonce:     *nonce,
	}

	log.Info().
		Str("lineageId", resp.LineageId).
		Str("extId", resp.ExtId).
		Int("nonce", resp.Nonce).
		Msg("leased ticket")

	return resp, nil
}

func (p *Servicer) GetTicket(lineageId string, ticketExtId string) (*api.TicketLeaseResponse, error) {
	var nonce int
	var leasedAtStr string
	var stateStr string

	err := p.db.QueryRowContext(context.TODO(), QueryStringSelectTicket, lineageId, ticketExtId).
		Scan(&nonce, &leasedAtStr, &stateStr)
	if err != nil {
		return nil, err
	}

	state := api.TicketLeaseResponseState(stateStr)

	resp := &api.TicketLeaseResponse{
		ExtId:     ticketExtId,
		LeasedAt:  leasedAtStr,
		LineageId: lineageId,
		Nonce:     nonce,
		State:     state,
	}

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Str("leased_at", leasedAtStr).
		Msg("retrieved ticket")

	return resp, nil
}

func (p *Servicer) ReleaseTicket(lineageId string, ticketExtId string) error {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return err
	}

	rows, err := p.db.QueryContext(context.TODO(), QueryStringReleaseTicket, lineageId, version, ticketExtId)
	if err != nil {
		return err
	}

	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Err(err).Msg("can't close rows")
		}
	}(rows)

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return err
	}

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Int("nonce", *nonce).
		Msg("released ticket")

	return nil
}

func (p *Servicer) CloseTicket(lineageId string, ticketExtId string) error {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return err
	}

	_, err = p.db.ExecContext(context.TODO(), QueryStringCloseTicket, lineageId, version, ticketExtId)
	if err != nil {
		return err
	}

	log.Info().
		Str("lineageId", lineageId).
		Str("extId", ticketExtId).
		Msg("closed ticket")

	return nil
}

func (p *Servicer) getLineageVersion(lineageId string) (int64, error) {
	rows, err := p.db.QueryContext(context.TODO(), QueryStringSelectLineageVersion, lineageId)
	if err != nil || !rows.Next() {
		return 0, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {
			log.Error().Err(err).Msg("can't close rows")
		}
	}(rows)

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
