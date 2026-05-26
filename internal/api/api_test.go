package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/matelang/dinonce/v3/internal/api"
	apigen "github.com/matelang/dinonce/v3/internal/api/generated"
	"github.com/matelang/dinonce/v3/internal/ticket"
)

// mockServicer is a hand-rolled stub. It is intentionally not generated
// because the Servicer surface is tiny and the per-test stubbing pattern
// (set the field you care about) is easier to reason about than a fully
// configured mock.
type mockServicer struct {
	createLineageFn func(ctx context.Context, req *apigen.LineageCreationRequest) (*apigen.LineageCreationResponse, error)
	getLineageFn    func(ctx context.Context, extId string) (*apigen.LineageGetResponse, error)
	leaseTicketFn   func(ctx context.Context, lineageId string, req *apigen.TicketLeaseRequest) (*apigen.TicketLeaseResponse, error)
	getTicketFn     func(ctx context.Context, lineageId, ticketExtId string) (*apigen.TicketLeaseResponse, error)
	releaseTicketFn func(ctx context.Context, lineageId, ticketExtId string) error
	closeTicketFn   func(ctx context.Context, lineageId, ticketExtId string) error
	getTicketsFn    func(ctx context.Context, lineageId string, ticketExtIds []string) (*apigen.TicketLeaseResponse, error)
}

func (m *mockServicer) CreateLineage(ctx context.Context, req *apigen.LineageCreationRequest) (*apigen.LineageCreationResponse, error) {
	return m.createLineageFn(ctx, req)
}

func (m *mockServicer) GetLineage(ctx context.Context, extId string) (*apigen.LineageGetResponse, error) {
	return m.getLineageFn(ctx, extId)
}

func (m *mockServicer) LeaseTicket(ctx context.Context, lineageId string, req *apigen.TicketLeaseRequest) (*apigen.TicketLeaseResponse, error) {
	return m.leaseTicketFn(ctx, lineageId, req)
}

func (m *mockServicer) GetTicket(ctx context.Context, lineageId, ticketExtId string) (*apigen.TicketLeaseResponse, error) {
	return m.getTicketFn(ctx, lineageId, ticketExtId)
}

func (m *mockServicer) ReleaseTicket(ctx context.Context, lineageId, ticketExtId string) error {
	return m.releaseTicketFn(ctx, lineageId, ticketExtId)
}

func (m *mockServicer) CloseTicket(ctx context.Context, lineageId, ticketExtId string) error {
	return m.closeTicketFn(ctx, lineageId, ticketExtId)
}

func (m *mockServicer) GetTickets(ctx context.Context, lineageId string, ticketExtIds []string) (*apigen.TicketLeaseResponse, error) {
	return m.getTicketsFn(ctx, lineageId, ticketExtIds)
}

func newTestHandler(t *testing.T, svc ticket.Servicer) (*api.Handler, *echo.Echo) {
	t.Helper()
	h := api.NewHandler(svc, api.BuildInfo{Version: "test", Commit: "abc", Date: "2026-01-01"})
	e := echo.New()
	apigen.RegisterHandlers(e, h)
	return h, e
}

func doJSON(e *echo.Echo, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestCreateLineage_OK(t *testing.T) {
	svc := &mockServicer{
		createLineageFn: func(_ context.Context, req *apigen.LineageCreationRequest) (*apigen.LineageCreationResponse, error) {
			if req.ExtId != "acct-1" {
				t.Fatalf("unexpected extId: %s", req.ExtId)
			}
			return &apigen.LineageCreationResponse{Id: "lin-1", ExtId: req.ExtId}, nil
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodPost, "/lineages", `{"extId":"acct-1","maxLeasedNonceCount":64}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateLineage_DefaultsStartLeasingFrom(t *testing.T) {
	called := false
	svc := &mockServicer{
		createLineageFn: func(_ context.Context, req *apigen.LineageCreationRequest) (*apigen.LineageCreationResponse, error) {
			called = true
			if req.StartLeasingFrom == nil || *req.StartLeasingFrom != 0 {
				t.Fatalf("expected handler to default StartLeasingFrom to 0, got %v", req.StartLeasingFrom)
			}
			return &apigen.LineageCreationResponse{Id: "lin", ExtId: req.ExtId}, nil
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodPost, "/lineages", `{"extId":"acct-2","maxLeasedNonceCount":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("mock servicer was not called")
	}
}

func TestCreateLineage_InvalidRequestMapsTo400(t *testing.T) {
	svc := &mockServicer{
		createLineageFn: func(context.Context, *apigen.LineageCreationRequest) (*apigen.LineageCreationResponse, error) {
			return nil, ticket.ErrInvalidRequest
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodPost, "/lineages", `{"extId":"x","maxLeasedNonceCount":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var body apigen.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %s", err)
	}
	if body.Code != "bad_request" {
		t.Fatalf("expected error code bad_request, got %q", body.Code)
	}
}

func TestGetLineageByExtId_NoSuchLineageMapsTo404(t *testing.T) {
	svc := &mockServicer{
		getLineageFn: func(context.Context, string) (*apigen.LineageGetResponse, error) {
			return nil, ticket.ErrNoSuchLineage
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodGet, "/lineages?extId=missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestLeaseTicket_StatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		retErr   error
		wantCode int
	}{
		{"invalid request", ticket.ErrInvalidRequest, http.StatusBadRequest},
		{"no such lineage", ticket.ErrNoSuchLineage, http.StatusBadRequest},
		{"too many leased", ticket.ErrTooManyLeasedTickets, http.StatusTooManyRequests},
		{"too many concurrent", ticket.ErrTooManyConcurrentRequests, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockServicer{
				leaseTicketFn: func(context.Context, string, *apigen.TicketLeaseRequest) (*apigen.TicketLeaseResponse, error) {
					return nil, tc.retErr
				},
			}
			_, e := newTestHandler(t, svc)

			rec := doJSON(e, http.MethodPost, "/lineages/lin-1/tickets", `{"extIds":["tx-1"]}`)
			if rec.Code != tc.wantCode {
				t.Fatalf("got %d, want %d (%s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestLeaseTicket_OK(t *testing.T) {
	leases := []apigen.TicketLease{{
		LineageId: "lin-1",
		ExtId:     "tx-1",
		Nonce:     42,
		State:     apigen.TicketLeaseStateLeased,
	}}
	svc := &mockServicer{
		leaseTicketFn: func(_ context.Context, lineageId string, req *apigen.TicketLeaseRequest) (*apigen.TicketLeaseResponse, error) {
			if lineageId != "lin-1" {
				t.Fatalf("unexpected lineageId %q", lineageId)
			}
			if len(req.ExtIds) != 1 || req.ExtIds[0] != "tx-1" {
				t.Fatalf("unexpected extIds %v", req.ExtIds)
			}
			return &apigen.TicketLeaseResponse{Leases: &leases}, nil
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodPost, "/lineages/lin-1/tickets", `{"extIds":["tx-1"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp apigen.TicketLeaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if resp.Leases == nil || len(*resp.Leases) != 1 || (*resp.Leases)[0].Nonce != 42 {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestGetTicket_StatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		retErr   error
		wantCode int
	}{
		{"no such ticket", ticket.ErrNoSuchTicket, http.StatusNotFound},
		{"invalid request", ticket.ErrInvalidRequest, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockServicer{
				getTicketFn: func(context.Context, string, string) (*apigen.TicketLeaseResponse, error) {
					return nil, tc.retErr
				},
			}
			_, e := newTestHandler(t, svc)

			rec := doJSON(e, http.MethodGet, "/lineages/lin/tickets/tx-1", "")
			if rec.Code != tc.wantCode {
				t.Fatalf("got %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestUpdateTicket_RoutesToReleaseOrClose(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		expectFn string
	}{
		{"released routes to ReleaseTicket", "released", "release"},
		{"closed routes to CloseTicket", "closed", "close"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var called string
			svc := &mockServicer{
				releaseTicketFn: func(context.Context, string, string) error { called = "release"; return nil },
				closeTicketFn:   func(context.Context, string, string) error { called = "close"; return nil },
			}
			_, e := newTestHandler(t, svc)

			rec := doJSON(e, http.MethodPatch, "/lineages/lin/tickets/tx-1",
				`{"state":"`+tc.state+`"}`)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d", rec.Code)
			}
			if called != tc.expectFn {
				t.Fatalf("expected %s servicer call, got %s", tc.expectFn, called)
			}
		})
	}
}

func TestUpdateTicket_StatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		retErr   error
		wantCode int
	}{
		{"invalid request", ticket.ErrInvalidRequest, http.StatusBadRequest},
		{"no such lineage", ticket.ErrNoSuchLineage, http.StatusBadRequest},
		{"no such ticket", ticket.ErrNoSuchTicket, http.StatusNotFound},
		{"too many concurrent", ticket.ErrTooManyConcurrentRequests, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockServicer{
				releaseTicketFn: func(context.Context, string, string) error { return tc.retErr },
			}
			_, e := newTestHandler(t, svc)
			rec := doJSON(e, http.MethodPatch, "/lineages/lin/tickets/tx-1", `{"state":"released"}`)
			if rec.Code != tc.wantCode {
				t.Fatalf("got %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestGetTickets_NoSuchTicketMapsTo404(t *testing.T) {
	svc := &mockServicer{
		getTicketsFn: func(context.Context, string, []string) (*apigen.TicketLeaseResponse, error) {
			return nil, ticket.ErrNoSuchTicket
		},
	}
	_, e := newTestHandler(t, svc)

	rec := doJSON(e, http.MethodGet, "/lineages/lin/tickets?ticketExtIds=tx-1&ticketExtIds=tx-2", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
