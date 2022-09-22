package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/welthee/dinonce/v2/internal/ticket"
	"net/http"
	"regexp"
	"strings"

	"github.com/deepmap/oapi-codegen/pkg/middleware"
	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	api "github.com/welthee/dinonce/v2/internal/api/generated"
	"github.com/ziflex/lecho/v3"
)

const port = 5010

const ErrorCodeNotFound = "not_found"
const ErrorCodeBadRequest = "bad_request"
const ErrorCodeTooManyLeasedTickets = "too_many_leased_tickets"
const ErrTooManyConcurrentRequests = "too_many_concurrent_requests"

type Handler struct {
	e        *echo.Echo
	servicer ticket.Servicer
}

func NewHandler(servicer ticket.Servicer) *Handler {
	var _ api.ServerInterface = &Handler{}
	e := echo.New()
	e.HideBanner = true

	return &Handler{
		e:        e,
		servicer: servicer,
	}
}

func (h *Handler) CreateLineage(ctx echo.Context) error {
	req := &api.LineageCreationRequest{}
	err := ctx.Bind(req)
	if err != nil {
		return err
	}

	if req.StartLeasingFrom == nil {
		zero := 0
		req.StartLeasingFrom = &zero
	}

	resp, err := h.servicer.CreateLineage(ctx.Request().Context(), req)
	if err != nil {
		switch err {
		case ticket.ErrInvalidRequest:
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) GetLineageByExtId(ctx echo.Context, params api.GetLineageByExtIdParams) error {
	resp, err := h.servicer.GetLineage(ctx.Request().Context(), params.ExtId)
	if err != nil {
		switch err {
		case ticket.ErrNoSuchLineage:
			return ctx.JSON(http.StatusNotFound, api.Error{
				Code:    ErrorCodeNotFound,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) LeaseTicket(ctx echo.Context, lineageId string) error {
	req := &api.TicketLeaseRequest{}
	if err := ctx.Bind(req); err != nil {
		return err
	}

	resp, err := h.servicer.LeaseTicket(ctx.Request().Context(), lineageId, req)
	if err != nil {
		switch err {
		case ticket.ErrInvalidRequest, ticket.ErrNoSuchLineage:
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		case ticket.ErrTooManyLeasedTickets:
			return ctx.JSON(http.StatusTooManyRequests, api.Error{
				Code:    ErrorCodeTooManyLeasedTickets,
				Message: err.Error(),
			})
		case ticket.ErrTooManyConcurrentRequests:
			return ctx.JSON(http.StatusConflict, api.Error{
				Code:    ErrTooManyConcurrentRequests,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) GetTicket(ctx echo.Context, lineageId string, ticketExtId string) error {
	resp, err := h.servicer.GetTicket(ctx.Request().Context(), lineageId, ticketExtId)
	if err != nil {
		switch err {
		case ticket.ErrNoSuchTicket:
			return ctx.NoContent(http.StatusNotFound)
		case ticket.ErrInvalidRequest:
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) UpdateTicket(ctx echo.Context, lineageId string, ticketExtId string) error {
	req := &api.TicketUpdateRequest{}
	err := ctx.Bind(req)
	if err != nil {
		return err
	}

	switch req.State {
	case api.TicketUpdateRequestStateReleased:
		err = h.servicer.ReleaseTicket(ctx.Request().Context(), lineageId, ticketExtId)
	case api.TicketUpdateRequestStateClosed:
		err = h.servicer.CloseTicket(ctx.Request().Context(), lineageId, ticketExtId)
	default:
		ctx.Error(errors.New("state must be one of:(released,closed)"))
	}
	if err != nil {
		switch err {
		case ticket.ErrInvalidRequest, ticket.ErrNoSuchLineage:
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		case ticket.ErrNoSuchTicket:
			return ctx.NoContent(http.StatusNotFound)
		case ticket.ErrTooManyConcurrentRequests:
			return ctx.JSON(http.StatusConflict, api.Error{
				Code:    ErrTooManyConcurrentRequests,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.NoContent(http.StatusNoContent)
}

func (h *Handler) GetTickets(ctx echo.Context, lineageId string, params api.GetTicketsParams) error {
	rCtx := ctx.Request().Context()
	resp, err := h.servicer.GetTickets(rCtx, lineageId, params.TicketExtIds)
	if err != nil {
		switch err {
		case ticket.ErrNoSuchTicket:
			return ctx.NoContent(http.StatusNotFound)
		case ticket.ErrInvalidRequest:
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		default:
			return err
		}
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) Start() error {
	h.e.Use(echomiddleware.Recover())
	h.e.Use(echomiddleware.RequestID())

	h.enableLoggingMiddleware()
	h.enablePrometheus()

	if err := h.enableOpenApiValidatorMiddleware(); err != nil {
		return err
	}

	api.RegisterHandlers(h.e, h)

	return h.e.Start(fmt.Sprintf(":%d", port))
}

func (h *Handler) Stop(ctx context.Context) error {
	err := h.e.Shutdown(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (h *Handler) enablePrometheus() {
	p := prometheus.NewPrometheus("dinonce", nil)
	p.Use(h.e)
}

func (h *Handler) enableLoggingMiddleware() {
	logger := lecho.New(
		log.Logger,
		lecho.WithTimestamp(),
		lecho.WithCaller(),
		lecho.WithField("component", "papi"),
	)

	h.e.Logger = logger

	skipper := func(e echo.Context) bool {
		userAgent := e.Request().UserAgent()
		re := regexp.MustCompile(`kube-probe|prometheus`)
		return re.MatchString(strings.ToLower(userAgent))
	}

	dumpConfig := echomiddleware.BodyDumpConfig{
		Skipper: skipper,
		Handler: func(c echo.Context, reqBody, resBody []byte) {
			log.Ctx(c.Request().Context()).Info().
				Str("requestBody", string(reqBody)).
				Str("responseBody", string(resBody)).
				Msg("")
		},
	}

	lechoConfig := lecho.Config{
		Skipper:      skipper,
		Logger:       logger,
		RequestIDKey: "traceId",
	}

	h.e.Use(echomiddleware.BodyDumpWithConfig(dumpConfig))
	h.e.Use(lecho.Middleware(lechoConfig))
}

func (h *Handler) enableOpenApiValidatorMiddleware() error {
	swagger, err := api.GetSwagger()
	if err != nil {
		return err
	}
	h.e.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
		Skipper: func(e echo.Context) bool {
			return e.Request().RequestURI == "/metrics"
		},
	}))

	return nil
}
