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
	"sync"
	"testing"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

const maxLeasedNonceCount = 64

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

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
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

func TestServicer_CreateLineage_DuplicateExtIdFails(t *testing.T) {
	extIdUUID, _ := uuid.NewUUID()

	req := &api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%s", extIdUUID.String()),
		MaxLeasedNonceCount: maxLeasedNonceCount,
	}

	_, err := victim.CreateLineage(req)
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}

	_, err = victim.CreateLineage(req)
	if err == nil {
		t.Errorf("second lineage creation with same extId should fail")
	}
	if err != ticket.ErrInvalidRequest {
		t.Errorf("expected error to be of type invalid request")
	}
}

func TestServicer_GetLineage(t *testing.T) {
	extIdUUID, _ := uuid.NewUUID()

	createLineageResponse, err := victim.CreateLineage(&api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%s", extIdUUID),
		MaxLeasedNonceCount: maxLeasedNonceCount,
	})
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}

	resp, err := victim.GetLineage(createLineageResponse.ExtId)
	if err != nil {
		t.Errorf("can not retrieve lineage %s", err)
	}

	if resp.ExtId != createLineageResponse.ExtId {
		t.Errorf("created and retrieved extId should be equal")
	}
}

func TestServicer_GetLineage_NoSuchLineageError(t *testing.T) {
	id, _ := uuid.NewUUID()

	_, err := victim.GetLineage(id.String())
	if err == nil {
		t.Errorf("inexistent lineage should retrun error")
	}

	if err != ticket.ErrNoSuchLineage {
		t.Errorf("expected NoSuchLineage error, got %s", err)
	}
}

func TestServicer_LeaseTicket(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", resp.Nonce)
	}
}

func TestServicer_LeaseTicket_NoSuchLineage(t *testing.T) {
	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	lineageId, _ := uuid.NewUUID()

	_, err := victim.LeaseTicket(lineageId.String(), request)
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_LeaseTicket_WithSameNonce(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", resp.Nonce)
	}

	resp, err = victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("fail on multiple lease operations, should be idempotent")
	}

	if resp.Nonce != 0 {
		t.Errorf("expected second ticket creation request to have no side effects and reuse initially "+
			"assigned nonce 0, got %d", resp.Nonce)
	}
}

func TestServicer_LeaseTicket_SameTicketDoubleLeaseWhenLineageFull(t *testing.T) {
	lineageId := createLineage(t)

	for i := 0; i < maxLeasedNonceCount-1; i++ {
		request := &api.TicketLeaseRequest{
			ExtId: fmt.Sprintf("tx%d", i),
		}

		if _, err := victim.LeaseTicket(lineageId, request); err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	request := &api.TicketLeaseRequest{
		ExtId: fmt.Sprintf("test-tx"),
	}

	if _, err := victim.LeaseTicket(lineageId, request); err != nil {
		t.Errorf("can not lease last ticket %s", err)
	}

	if _, err := victim.LeaseTicket(lineageId, request); err != nil {
		t.Errorf("can not double lease last ticket. should be idempotent. %s", err)
	}
}

func TestServicer_CloseTicket(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", resp.Nonce)
	}

	err = victim.CloseTicket(resp.LineageId, resp.ExtId)
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}
}

func TestServicer_CloseTicket_NoSuchLineage(t *testing.T) {
	lineageId, _ := uuid.NewUUID()

	err := victim.CloseTicket(lineageId.String(), "nonexistent")
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_CloseTicket_Idempotency(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", resp.Nonce)
	}

	err = victim.CloseTicket(resp.LineageId, resp.ExtId)
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	err = victim.CloseTicket(resp.LineageId, resp.ExtId)
	if err != nil {
		t.Errorf("can not close already closed ticket %s", err)
	}
}

func TestServicer_CloseTicket_NoSuchTicketError(t *testing.T) {
	lineageId := createLineage(t)

	err := victim.CloseTicket(lineageId, "nonexistent")
	if err == nil {
		t.Error("should not be able to close nonexistent ticket")
	}

	if err != ticket.ErrNoSuchTicket {
		t.Errorf("expected ErrNoSuchTicket, got %s", err)
	}
}

func TestServicer_CloseTicket_Concurrency(t *testing.T) {
	lineageId := createLineage(t)

	for i := 0; i < maxLeasedNonceCount; i++ {
		request := &api.TicketLeaseRequest{
			ExtId: fmt.Sprintf("tx%d", i),
		}

		_, err := victim.LeaseTicket(lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	wg := sync.WaitGroup{}
	for i := 0; i < maxLeasedNonceCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := victim.CloseTicket(lineageId, fmt.Sprintf("tx%d", i))
			if err != nil {
				t.Error("unhandled optimistic lock")
			}
		}(i)
	}
	wg.Wait()
}

func TestServicer_LeaseTicket_InvalidRequestErrorOnClosedExtId(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", resp.Nonce)
	}

	err = victim.CloseTicket(resp.LineageId, resp.ExtId)
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

func TestServicer_LeaseTicket_TooManyLeasedTicketsError(t *testing.T) {
	lineageId := createLineage(t)

	for i := 0; i < maxLeasedNonceCount; i++ {
		request := &api.TicketLeaseRequest{
			ExtId: fmt.Sprintf("tx%d", i),
		}

		_, err := victim.LeaseTicket(lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	request := &api.TicketLeaseRequest{
		ExtId: fmt.Sprintf("failing-tx"),
	}

	_, err := victim.LeaseTicket(lineageId, request)
	if err == nil || err != ticket.ErrTooManyLeasedTickets {
		t.Errorf("expected error to be ErrTooManyLeasedTickets")
	}
}

func TestServicer_LeaseTicket_Concurrency(t *testing.T) {
	lineageId := createLineage(t)

	wg := sync.WaitGroup{}
	for i := 0; i < maxLeasedNonceCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			request := &api.TicketLeaseRequest{
				ExtId: fmt.Sprintf("tx%d", i),
			}

			_, err := victim.LeaseTicket(lineageId, request)
			if err != nil {
				t.Errorf("can not lease ticket %s", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestServicer_LeaseTicket_ReleasedNonceReassignment(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtId: "tx1",
	}

	resp, err := victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	err = victim.ReleaseTicket(lineageId, resp.ExtId)

	request = &api.TicketLeaseRequest{
		ExtId: "tx2",
	}

	resp, err = victim.LeaseTicket(lineageId, request)
	if err != nil {
		t.Errorf("can not lease second ticket %s", err)
	}

	if resp.Nonce != 0 {
		t.Errorf("expected released nonce 0 to be reused on second tx, got %d", resp.Nonce)
	}
}

func TestServicer_ReleaseTicket_NoSuchLineage(t *testing.T) {
	lineageId, _ := uuid.NewUUID()

	err := victim.ReleaseTicket(lineageId.String(), "nonexistent")
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_ReleaseTicket_NoSuchTicket(t *testing.T) {
	lineageId := createLineage(t)

	err := victim.ReleaseTicket(lineageId, "nonexistent")
	if err == nil {
		t.Error("should not be able to close nonexistent ticket")
	}

	if err != ticket.ErrNoSuchTicket {
		t.Errorf("expected ErrNoSuchTicket, got %s", err)
	}
}

func TestServicer_ReleaseTicket_Concurrency(t *testing.T) {
	lineageId := createLineage(t)

	for i := 0; i < maxLeasedNonceCount; i++ {
		request := &api.TicketLeaseRequest{
			ExtId: fmt.Sprintf("tx%d", i),
		}

		_, err := victim.LeaseTicket(lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	wg := sync.WaitGroup{}
	for i := 0; i < maxLeasedNonceCount; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			err := victim.ReleaseTicket(lineageId, fmt.Sprintf("tx%d", i))
			if err != nil {
				t.Error("unhandled optimistic lock")
			}
		}(i)
	}
	wg.Wait()

}

func TestServicer_GetTicket_Leased(t *testing.T) {
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

	if resp.State != "leased" {
		t.Error("ticket should be in leased state")
	}
}

func TestServicer_GetTicket_Closed(t *testing.T) {
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

	if resp.State != "closed" {
		t.Error("ticket should be in leased state")
	}
}

func TestServicer_GetTicket_NoSuchTicket(t *testing.T) {
	lineageId := createLineage(t)

	_, err := victim.GetTicket(lineageId, "nonexistent")
	if err == nil || err != ticket.ErrNoSuchTicket {
		t.Errorf("expected ErrNoSuchTicket, got %s", err)
	}
}

func createLineage(t *testing.T) string {
	extIdUUID, _ := uuid.NewUUID()
	resp, err := victim.CreateLineage(&api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%s", extIdUUID.String()),
		MaxLeasedNonceCount: maxLeasedNonceCount,
	})
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}
	return resp.Id
}
