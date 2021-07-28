package psqlticket_test

import (
	"database/sql"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
	"github.com/welthee/dinonce/v2/pkg/psqlticket"
	"github.com/welthee/dinonce/v2/pkg/ticket"
	"testing"
	"time"
)

const (
	host     = "localhost"
	port     = 5433
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

const Version = 1

var victim ticket.Servicer

func init() {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal().Err(err).Msg("can not open db connection")
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		log.Fatal().Err(err).Msg("can not get db instance")
	}

	m, err := migrate.NewWithDatabaseInstance("file://../../scripts/psql/migrations", "postgres", driver)
	if err != nil {
		log.Fatal().Err(err).Msg("can not migrate database schema")
	}

	if err := m.Migrate(Version); err != nil && err != migrate.ErrNoChange {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}

	victim = psqlticket.NewServicer(db)
}

func TestServicer_CreateLineage(t *testing.T) {
	id := createLineage(t)

	_, err := uuid.Parse(id)
	if err != nil {
		t.Errorf("lineageId must be valid UUID %s", err)
	}
}

func TestServicer_LeaseTicket_SingleTicketOk(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", *resp.Nonce)
	}
}

func TestServicer_LeaseTicket_SingleTicketIdempotencyWithSameNonce(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", *resp.Nonce)
	}

	resp, err = victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("fail on multiple lease operations, should be idempotent")
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected second ticket creation request to have no side effects and reuse initially "+
			"assigned nonce 0, got %d", *resp.Nonce)
	}
}

func TestServicer_LeaseTicket_SingleTicketClose(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", *resp.Nonce)
	}

	err = victim.CloseTicket(*resp.LineageId, *resp.ExtId)
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}
}

func TestServicer_LeaseTicket_SingleTicketCloseAndAfterCreateValidation(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", *resp.Nonce)
	}

	err = victim.CloseTicket(*resp.LineageId, *resp.ExtId)
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	resp, err = victim.LeaseTicket(lineageId, request)
	if err == nil {
		t.Error("should not be able to lease a ticket with a closed ticket's ref")
	}

	if err != ticket.ErrInvalidRequest {
		t.Errorf("expected validation error")
	}
}

func TestServicer_LeaseTicket_ReleasedNonceCorrectReassignment(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	err = victim.ReleaseTicket(lineageId, *resp.ExtId)

	request = &api.TicketLeaseRequest{
		ExtId: "tx2",
	}

	resp, err = victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease second ticket %s", err)
	}

	if *resp.Nonce != 0 {
		t.Errorf("expected released nonce 0 to be reused on second tx, got %d", *resp.Nonce)
	}
}

func TestServicer_GetTicket_LeasedOk(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	_, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	resp, err := victim.GetTicket(lineageId, request.ExtId)
	if err != nil {
		t.Errorf("can not get ticket %s", err)
	}

	if *resp.State != "leased" {
		t.Error("ticket should be in leased state")
	}

}

func TestServicer_GetTicket_ClosedOk(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	_, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	err = victim.CloseTicket(lineageId, request.ExtId)
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	resp, err := victim.GetTicket(lineageId, request.ExtId)
	if err != nil {
		t.Errorf("can not get ticket %s", err)
	}

	if *resp.State != "closed" {
		t.Error("ticket should be in leased state")
	}

}

func createLineage(t *testing.T) string {
	resp, err := victim.CreateLineage(&api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%d", time.Now().Unix()),
		MaxLeasedNonceCount: 64,
	})
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}
	return *resp.Id
}
