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
		Id:    &lineageId,
		ExtId: &request.ExtId,
	}

	return resp, nil
}

func (p *Servicer) LeaseTicket(lineageId string, request *api.TicketLeaseRequest) (*api.TicketLeaseResponse, error) {
	version, err := p.getLineageVersion(lineageId)
	if err != nil {
		return nil, err
	}

	rows, err := p.db.QueryContext(context.TODO(), QueryStringCreateTicket, lineageId, version, request.ExtId)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Message == "validation_error" {
			return nil, ticket.ErrInvalidRequest
		}

		return nil, err
	}
	defer rows.Close()

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return nil, err
	}

	resp := &api.TicketLeaseResponse{
		LineageId: &lineageId,
		ExtId:     &request.ExtId,
		Nonce:     nonce,
	}

	log.Info().
		Str("lineage", *resp.LineageId).
		Str("ref", *resp.ExtId).
		Int("nonce", *resp.Nonce).
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
		ExtId:     &ticketExtId,
		LeasedAt:  &leasedAtStr,
		LineageId: &lineageId,
		Nonce:     &nonce,
		State:     &state,
	}

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
	defer rows.Close()

	nonce, err := getNonceFromRow(rows)
	if err != nil {
		return err
	}

	log.Info().
		Str("lineage", lineageId).
		Str("ref", ticketExtId).
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
		Str("lineage", lineageId).
		Str("ref", ticketExtId).
		Msg("closed ticket")

	return nil
}

func (p *Servicer) getLineageVersion(lineageId string) (int64, error) {
	rows, err := p.db.QueryContext(context.TODO(), QueryStringSelectLineageVersion, lineageId)
	if err != nil || !rows.Next() {
		return 0, err
	}
	defer rows.Close()

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
