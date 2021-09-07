package openapi

import (
	"context"
	"errors"
	"fmt"
	"github.com/deepmap/oapi-codegen/pkg/middleware"
	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog"
	"github.com/segmentio/ksuid"
	api "github.com/welthee/dinonce/v2/pkg/openapi/generated"
	"github.com/welthee/dinonce/v2/pkg/ticket"
	"github.com/ziflex/lecho/v2"
	"net/http"
	"os"
	"strings"
)

const port = 5010

const ErrorCodeNotFound = "not_found"
const ErrorCodeBadRequest = "bad_request"
const ErrorCodeTooManyLeasedTickets = "too_many_leased_tickets"
const ErrTooManyConcurrentRequests = "too_many_concurrent_requests"

type ApiHandler struct {
	e        *echo.Echo
	servicer ticket.Servicer
}

func NewApiHandler(servicer ticket.Servicer) *ApiHandler {
	e := echo.New()
	e.HideBanner = true

	return &ApiHandler{
		e:        e,
		servicer: servicer,
	}
}

func (h *ApiHandler) CreateLineage(ctx echo.Context) error {
	req := &api.LineageCreationRequest{}
	err := ctx.Bind(req)
	if err != nil {
		return err
	}

	if req.StartLeasingFrom == nil {
		zero := 0
		req.StartLeasingFrom = &zero
	}

	resp, err := h.servicer.CreateLineage(req)
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

func (h *ApiHandler) GetLineageByExtId(ctx echo.Context, params api.GetLineageByExtIdParams) error {
	resp, err := h.servicer.GetLineage(params.ExtId)
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

func (h *ApiHandler) LeaseTicket(ctx echo.Context, lineageId string) error {
	req := &api.TicketLeaseRequest{}
	err := ctx.Bind(req)
	if err != nil {
		return err
	}

	resp, err := h.servicer.LeaseTicket(lineageId, req)
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

func (h *ApiHandler) GetTicket(ctx echo.Context, lineageId string, ticketExtId string) error {
	resp, err := h.servicer.GetTicket(lineageId, ticketExtId)
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

func (h *ApiHandler) UpdateTicket(ctx echo.Context, lineageId string, ticketExtId string) error {
	req := &api.TicketUpdateRequest{}
	err := ctx.Bind(req)
	if err != nil {
		return err
	}

	switch req.State {
	case api.TicketUpdateRequestStateReleased:
		err = h.servicer.ReleaseTicket(lineageId, ticketExtId)
	case api.TicketUpdateRequestStateClosed:
		err = h.servicer.CloseTicket(lineageId, ticketExtId)
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

func (h *ApiHandler) Start() error {
	h.e.Use(echomiddleware.Recover())
	h.enablePrometheus()
	h.enableRequestIdMiddleware()
	h.enableLoggerMiddlewares()

	if err := h.enableOpenApiValidatorMiddleware(); err != nil {
		return err
	}

	api.RegisterHandlers(h.e, h)

	return h.e.Start(fmt.Sprintf(":%d", port))
}

func (h *ApiHandler) Stop(ctx context.Context) error {
	err := h.e.Shutdown(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (h *ApiHandler) enablePrometheus() {
	p := prometheus.NewPrometheus("dinonce", nil)
	p.Use(h.e)
}

func (h *ApiHandler) enableRequestIdMiddleware() {
	h.e.Use(echomiddleware.RequestIDWithConfig(echomiddleware.RequestIDConfig{
		Skipper: func(e echo.Context) bool {
			if e.Request().RequestURI == "/metrics" {
				return true
			}

			return false
		},
		Generator: func() string {
			return ksuid.New().String()
		},
	}))
}

func (h *ApiHandler) enableLoggerMiddlewares() {
	logger := lecho.New(
		os.Stdout,
		lecho.WithTimestamp(),
		lecho.WithField("component", "api"),
	)
	h.e.Logger = logger

	h.e.Use(lecho.Middleware(lecho.Config{
		Skipper: func(e echo.Context) bool {
			if e.Request().RequestURI == "/metrics" {
				return true
			}

			userAgent := e.Request().UserAgent()
			if strings.Contains(userAgent, "kube-probe") {
				return true
			}

			return false
		},
		Logger: logger,
	}))

	requestBodyLogger := zerolog.New(os.Stderr).With().
		Timestamp().
		Logger()

	h.e.Use(echomiddleware.BodyDumpWithConfig(echomiddleware.BodyDumpConfig{
		Skipper: func(e echo.Context) bool {
			if e.Request().RequestURI == "/metrics" {
				return true
			}

			return false
		},
		Handler: func(e echo.Context, req []byte, resp []byte) {
			requestBodyLogger.WithLevel(zerolog.DebugLevel).
				Str("component", "api").
				Bytes("req", req).
				Bytes("resp", resp).
				Msg("")
		},
	}))
}

func (h *ApiHandler) enableOpenApiValidatorMiddleware() error {
	swagger, err := api.GetSwagger()
	if err != nil {
		return err
	}
	h.e.Use(middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{
		Skipper: func(e echo.Context) bool {
			if e.Request().RequestURI == "/metrics" {
				return true
			}

			return false
		},
	}))

	return nil
}
