package psqlticket_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
	"github.com/welthee/dinonce/v2/pkg/psqlticket"
	"github.com/welthee/dinonce/v2/pkg/ticket"
)

const (
	host     = "localhost"
	port     = 5433
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

const maxLeasedNonceCount = 64

var victim ticket.Servicer
var ctx = context.Background()

func init() {
	logger := zerolog.New(os.Stdout).Level(zerolog.WarnLevel)
	ctx = logger.WithContext(ctx)

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

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		log.Fatal().Err(err).Msg("failed to reset database")
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

	_, err := victim.CreateLineage(ctx, req)
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}

	_, err = victim.CreateLineage(ctx, req)
	if err == nil {
		t.Errorf("second lineage creation with same extId should fail")
	}
	if err != ticket.ErrInvalidRequest {
		t.Errorf("expected error to be of type invalid request")
	}
}

func TestServicer_GetLineage(t *testing.T) {
	extIdUUID, _ := uuid.NewUUID()

	createLineageResponse, err := victim.CreateLineage(ctx, &api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%s", extIdUUID),
		MaxLeasedNonceCount: maxLeasedNonceCount,
	})
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}

	resp, err := victim.GetLineage(ctx, createLineageResponse.ExtId)
	if err != nil {
		t.Errorf("can not retrieve lineage %s", err)
	}

	if resp.ExtId != createLineageResponse.ExtId {
		t.Errorf("created and retrieved extId should be equal")
	}
}

func TestServicer_GetLineage_NoSuchLineageError(t *testing.T) {
	id, _ := uuid.NewUUID()

	_, err := victim.GetLineage(ctx, id.String())
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
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", nonce)
	}
}

func TestServicer_LeaseTicket_NoSuchLineage(t *testing.T) {
	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	lineageId, _ := uuid.NewUUID()

	_, err := victim.LeaseTicket(ctx, lineageId.String(), request)
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_LeaseTicket_WithSameNonce(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", nonce)
	}

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("fail on multiple lease operations, should be idempotent %s", err)
	}

	if nonce != 0 {
		t.Errorf("expected second ticket creation request to have no side effects and reuse initially "+
			"assigned nonce 0, got %d", nonce)
	}
}

func TestServicer_LeaseTicket_SameTicketDoubleLeaseWhenLineageFull(t *testing.T) {
	lineageId := createLineage(t)

	for i := 0; i < maxLeasedNonceCount-1; i++ {
		request := &api.TicketLeaseRequest{
			ExtIds: []string{fmt.Sprintf("tx%d", i)},
		}

		if _, err := victim.LeaseTicket(ctx, lineageId, request); err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	request := &api.TicketLeaseRequest{
		ExtIds: []string{fmt.Sprintf("test-tx")},
	}

	if _, err := victim.LeaseTicket(ctx, lineageId, request); err != nil {
		t.Errorf("can not lease last ticket %s", err)
	}

	if _, err := victim.LeaseTicket(ctx, lineageId, request); err != nil {
		t.Errorf("can not double lease last ticket. should be idempotent. %s", err)
	}
}

func TestServicer_CloseTicket(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", nonce)
	}

	err = victim.CloseTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}
}

func TestServicer_CloseTicket_NoSuchLineage(t *testing.T) {
	lineageId, _ := uuid.NewUUID()

	err := victim.CloseTicket(ctx, lineageId.String(), "nonexistent")
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_CloseTicket_Idempotency(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", nonce)
	}

	err = victim.CloseTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	err = victim.CloseTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not close already closed ticket %s", err)
	}
}

func TestServicer_CloseTicket_NoSuchTicketError(t *testing.T) {
	lineageId := createLineage(t)

	err := victim.CloseTicket(ctx, lineageId, "nonexistent")
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
			ExtIds: []string{fmt.Sprintf("tx%d", i)},
		}

		_, err := victim.LeaseTicket(ctx, lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	wg := sync.WaitGroup{}
	for i := 0; i < maxLeasedNonceCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := victim.CloseTicket(ctx, lineageId, fmt.Sprintf("tx%d", i))
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
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected first leased nonce to be 0, got %d", nonce)
	}

	err = victim.CloseTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
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
			ExtIds: []string{fmt.Sprintf("tx%d", i)},
		}

		_, err := victim.LeaseTicket(ctx, lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	request := &api.TicketLeaseRequest{
		ExtIds: []string{fmt.Sprintf("failing-tx")},
	}

	_, err := victim.LeaseTicket(ctx, lineageId, request)
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
				ExtIds: []string{fmt.Sprintf("tx%d", i)},
			}

			_, err := victim.LeaseTicket(ctx, lineageId, request)
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
		ExtIds: []string{"tx1"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	err = victim.ReleaseTicket(ctx, lineageId, request.ExtIds[0])

	request = &api.TicketLeaseRequest{
		ExtIds: []string{"tx2"},
	}

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease second ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected released nonce 0 to be reused on second tx, got %d", nonce)
	}
}

func TestServicer_ReleaseTicket_WhenLineageFull(t *testing.T) {
	lineageId := createLineage(t)

	extIds := make([]string, maxLeasedNonceCount)
	for i := 0; i < maxLeasedNonceCount; i++ {
		extIds[i] = fmt.Sprintf("tx%d", i)
	}

	log.Info().Strs("extIds", extIds).Msg("")

	request := &api.TicketLeaseRequest{
		ExtIds: extIds,
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease %d tickets %s", maxLeasedNonceCount, err)
	}

	subsequentTicketLeaseRequest := &api.TicketLeaseRequest{ExtIds: []string{"subsequent-tx"}}
	_, err = victim.LeaseTicket(ctx, lineageId, subsequentTicketLeaseRequest)
	if err != ticket.ErrTooManyLeasedTickets {
		t.Errorf("should not be able to lease more tickets than %d", maxLeasedNonceCount)
	}

	err = victim.ReleaseTicket(ctx, lineageId, request.ExtIds[0])

	resp, err = victim.LeaseTicket(ctx, lineageId, subsequentTicketLeaseRequest)
	if err != nil {
		t.Errorf("can not subsequent ticket after release %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)

	if nonce != 0 {
		t.Errorf("expected released nonce 0 to be reused on subsequent tx, got %d", nonce)
	}
}

func TestServicer_ReleaseTicket_WhenLineageFullNoSuchTicket(t *testing.T) {
	lineageId := createLineage(t)

	extIds := make([]string, maxLeasedNonceCount)
	for i := 0; i < maxLeasedNonceCount; i++ {
		extIds[i] = fmt.Sprintf("tx%d", i)
	}

	request := &api.TicketLeaseRequest{
		ExtIds: extIds,
	}

	_, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease %d tickets %s", maxLeasedNonceCount, err)
	}

	subsequentTicketLeaseRequest := &api.TicketLeaseRequest{ExtIds: []string{"subsequent-tx"}}
	_, err = victim.LeaseTicket(ctx, lineageId, subsequentTicketLeaseRequest)
	if err != ticket.ErrTooManyLeasedTickets {
		t.Errorf("should not be able to lease more tickets than %d", maxLeasedNonceCount)
	}

	err = victim.ReleaseTicket(ctx, lineageId, subsequentTicketLeaseRequest.ExtIds[0])
	if err != ticket.ErrNoSuchTicket {
		t.Errorf("expected ErrNoSuchTicket")
	}
}

func TestServicer_ReleaseTicket_ReleaseAll(t *testing.T) {
	lineageId := createLineage(t)

	extIds := make([]string, maxLeasedNonceCount)
	for i := 0; i < maxLeasedNonceCount; i++ {
		extIds[i] = fmt.Sprintf("tx%d", i)
	}

	request := &api.TicketLeaseRequest{
		ExtIds: extIds,
	}

	_, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease %d tickets %s", maxLeasedNonceCount, err)
	}

	subsequentTicketLeaseRequest := &api.TicketLeaseRequest{ExtIds: []string{"subsequent-tx"}}
	_, err = victim.LeaseTicket(ctx, lineageId, subsequentTicketLeaseRequest)
	if err != ticket.ErrTooManyLeasedTickets {
		t.Errorf("should not be able to lease more tickets than %d", maxLeasedNonceCount)
	}

	for _, eId := range request.ExtIds {
		err = victim.ReleaseTicket(ctx, lineageId, eId)
		if err != nil {
			t.Errorf("can not release ticket %s %s", eId, err)
		}
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, subsequentTicketLeaseRequest)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	nonce := ensureAndGetSingleNonce(t, resp)
	if nonce != 0 {
		t.Errorf("expected nonce to be 0, since all tickets have been released")
	}
}

func TestServicer_ReleaseTicket_NoSuchLineage(t *testing.T) {
	lineageId, _ := uuid.NewUUID()

	err := victim.ReleaseTicket(ctx, lineageId.String(), "nonexistent")
	if err == nil || err != ticket.ErrNoSuchLineage {
		t.Errorf("expected ErrNoSuchLineage, got %s", err)
	}
}

func TestServicer_ReleaseTicket_NoSuchTicket(t *testing.T) {
	lineageId := createLineage(t)

	err := victim.ReleaseTicket(ctx, lineageId, "nonexistent")
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
			ExtIds: []string{fmt.Sprintf("tx%d", i)},
		}

		_, err := victim.LeaseTicket(ctx, lineageId, request)
		if err != nil {
			t.Errorf("can not lease ticket %s", err)
		}
	}

	wg := sync.WaitGroup{}
	for i := 0; i < maxLeasedNonceCount; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			err := victim.ReleaseTicket(ctx, lineageId, fmt.Sprintf("tx%d", i))
			if err != nil {
				t.Errorf("unhandled optimistic lock %s", err)
			}
		}(i)
	}
	wg.Wait()

}

func TestServicer_GetTicket_Leased(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	_, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	resp, err := victim.GetTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not get ticket %s", err)
	}

	if len(*resp.Leases) != 1 {
		t.Errorf("expected a single lease")
	}

	if (*resp.Leases)[0].State != "leased" {
		t.Error("ticket should be in leased state")
	}
}

func TestServicer_GetTicket_Closed(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1"},
	}

	_, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease initial ticket %s", err)
	}

	err = victim.CloseTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not close leased ticket %s", err)
	}

	resp, err := victim.GetTicket(ctx, lineageId, request.ExtIds[0])
	if err != nil {
		t.Errorf("can not get ticket %s", err)
	}

	if len(*resp.Leases) != 1 {
		t.Errorf("expected a single lease")
	}

	if (*resp.Leases)[0].State != "closed" {
		t.Error("ticket should be in closed state")
	}
}

func TestServicer_GetTicket_NoSuchTicket(t *testing.T) {
	lineageId := createLineage(t)

	_, err := victim.GetTicket(ctx, lineageId, "nonexistent")
	if err == nil || err != ticket.ErrNoSuchTicket {
		t.Errorf("expected ErrNoSuchTicket, got %s", err)
	}
}

func TestServicer_LeaseTicketsInBulk(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1", "tx2", "tx3"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	expectedNonces := []int{0, 1, 2}

	if len(*resp.Leases) != len(request.ExtIds) {
		t.Errorf("expected %d of leases, got %d", len(request.ExtIds), len(*resp.Leases))
	}

	for i, lease := range *resp.Leases {
		if lease.State != api.TicketLeaseStateLeased {
			t.Errorf("ticket with extId=%s expected to be in state leased, got=%s", lease.ExtId, lease.State)
		}

		if lease.Nonce != expectedNonces[i] {
			t.Errorf("ticket with extId=%s expected to have nonce=%d, got=%d", lease.ExtId, expectedNonces[i], lease.Nonce)
		}
	}
}

func TestServicer_LeaseTicketsInBulk_Idempotency(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1", "tx2", "tx3"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	ensureTicketsInStateAndCorrectlyOrdered(t, request, resp, api.TicketLeaseStateLeased)

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket on subsequent try %s", err)
	}

	ensureTicketsInStateAndCorrectlyOrdered(t, request, resp, api.TicketLeaseStateLeased)
}

func TestServicer_LeaseTicketsInBulk_AfterPartialSequenceRelease(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1", "tx2", "tx3"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	ensureTicketsInStateAndCorrectlyOrdered(t, request, resp, api.TicketLeaseStateLeased)

	ticketExtIdToBeReleased := request.ExtIds[1]
	err = victim.ReleaseTicket(ctx, lineageId, ticketExtIdToBeReleased)
	if err != nil {
		t.Errorf("could not release ticket with extId=%s", ticketExtIdToBeReleased)
	}

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket on subsequent try %s", err)
	}

	newTicketOrder := []string{"tx1", "tx3", "tx2"}
	ensureTicketsInStateAndCorrectlyOrdered(t, &api.TicketLeaseRequest{ExtIds: newTicketOrder}, resp, api.TicketLeaseStateLeased)
}

func TestServicer_LeaseTicketsInBulk_AfterAllReleased(t *testing.T) {
	lineageId := createLineage(t)

	request := &api.TicketLeaseRequest{
		ExtIds: []string{"tx1", "tx2", "tx3"},
	}

	resp, err := victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket %s", err)
	}

	ensureTicketsInStateAndCorrectlyOrdered(t, request, resp, api.TicketLeaseStateLeased)

	for _, e := range request.ExtIds {
		err = victim.ReleaseTicket(ctx, lineageId, e)
		if err != nil {
			t.Errorf("could not release ticket with extId=%s", e)
		}
	}

	resp, err = victim.LeaseTicket(ctx, lineageId, request)
	if err != nil {
		t.Errorf("can not lease ticket on subsequent try %s", err)
	}

	newTicketOrder := []string{"tx1", "tx3", "tx2"}
	ensureTicketsInStateAndCorrectlyOrdered(t, &api.TicketLeaseRequest{ExtIds: newTicketOrder}, resp, api.TicketLeaseStateLeased)
}

func createLineage(t *testing.T) string {
	extIdUUID, _ := uuid.NewUUID()
	resp, err := victim.CreateLineage(ctx, &api.LineageCreationRequest{
		ExtId:               fmt.Sprintf("test-%s", extIdUUID.String()),
		MaxLeasedNonceCount: maxLeasedNonceCount,
	})
	if err != nil {
		t.Errorf("can not create lineage %s", err)
	}
	return resp.Id
}

func ensureAndGetSingleNonce(t *testing.T, resp *api.TicketLeaseResponse) int {
	if len(*resp.Leases) != 1 {
		t.Errorf("expected a single ticket")
	}

	nonce := (*resp.Leases)[0].Nonce

	return nonce
}

func ensureTicketsInStateAndCorrectlyOrdered(t *testing.T, req *api.TicketLeaseRequest, resp *api.TicketLeaseResponse,
	state api.TicketLeaseState) {

	expectedNonces := make([]int, len(req.ExtIds))

	for i := 0; i < len(req.ExtIds); i++ {
		expectedNonces[i] = i
	}

	if len(*resp.Leases) != len(req.ExtIds) {
		t.Errorf("expected %d of leases, got %d", len(req.ExtIds), len(*resp.Leases))
	}

	for i, lease := range *resp.Leases {
		if lease.State != state {
			t.Errorf("ticket with extId=%s expected to be in state leased, got=%s", lease.ExtId, lease.State)
		}

		if lease.Nonce != expectedNonces[i] {
			t.Errorf("ticket with extId=%s expected to have nonce=%d, got=%d", lease.ExtId, expectedNonces[i], lease.Nonce)
		}
	}
}
