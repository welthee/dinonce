package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	oapimiddleware "github.com/oapi-codegen/echo-middleware"
	"github.com/rs/zerolog/log"
	"github.com/ziflex/lecho/v3"

	api "github.com/matelang/dinonce/v3/internal/api/generated"
	"github.com/matelang/dinonce/v3/internal/ticket"
)

const port = 5010

const ErrorCodeNotFound = "not_found"
const ErrorCodeBadRequest = "bad_request"
const ErrorCodeTooManyLeasedTickets = "too_many_leased_tickets"
const ErrTooManyConcurrentRequests = "too_many_concurrent_requests"

// BuildInfo describes the binary's build metadata. The main package
// populates it from -ldflags-injected variables and passes it in here so
// /version can be served without reaching back into main.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type Handler struct {
	e        *echo.Echo
	servicer ticket.Servicer
	build    BuildInfo
}

func NewHandler(servicer ticket.Servicer, build BuildInfo) *Handler {
	var _ api.ServerInterface = &Handler{}
	e := echo.New()
	e.HideBanner = true

	return &Handler{
		e:        e,
		servicer: servicer,
		build:    build,
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
		if errors.Is(err, ticket.ErrInvalidRequest) {
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		}
		return err
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (h *Handler) GetLineageByExtId(ctx echo.Context, params api.GetLineageByExtIdParams) error {
	resp, err := h.servicer.GetLineage(ctx.Request().Context(), params.ExtId)
	if err != nil {
		if errors.Is(err, ticket.ErrNoSuchLineage) {
			return ctx.JSON(http.StatusNotFound, api.Error{
				Code:    ErrorCodeNotFound,
				Message: err.Error(),
			})
		}
		return err
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
		switch {
		case errors.Is(err, ticket.ErrInvalidRequest), errors.Is(err, ticket.ErrNoSuchLineage):
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		case errors.Is(err, ticket.ErrTooManyLeasedTickets):
			return ctx.JSON(http.StatusTooManyRequests, api.Error{
				Code:    ErrorCodeTooManyLeasedTickets,
				Message: err.Error(),
			})
		case errors.Is(err, ticket.ErrTooManyConcurrentRequests):
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
		switch {
		case errors.Is(err, ticket.ErrNoSuchTicket):
			return ctx.NoContent(http.StatusNotFound)
		case errors.Is(err, ticket.ErrInvalidRequest):
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
		switch {
		case errors.Is(err, ticket.ErrInvalidRequest), errors.Is(err, ticket.ErrNoSuchLineage):
			return ctx.JSON(http.StatusBadRequest, api.Error{
				Code:    ErrorCodeBadRequest,
				Message: err.Error(),
			})
		case errors.Is(err, ticket.ErrNoSuchTicket):
			return ctx.NoContent(http.StatusNotFound)
		case errors.Is(err, ticket.ErrTooManyConcurrentRequests):
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
		switch {
		case errors.Is(err, ticket.ErrNoSuchTicket):
			return ctx.NoContent(http.StatusNotFound)
		case errors.Is(err, ticket.ErrInvalidRequest):
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

	// /version sits outside the OpenAPI contract so it has no spec entry; the
	// validator middleware's Skipper covers it (see
	// enableOpenApiValidatorMiddleware).
	h.e.GET("/version", h.getVersion)

	return h.e.Start(fmt.Sprintf(":%d", port))
}

func (h *Handler) getVersion(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, h.build)
}

func (h *Handler) Stop(ctx context.Context) error {
	err := h.e.Shutdown(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (h *Handler) enablePrometheus() {
	h.e.Use(echoprometheus.NewMiddleware("dinonce"))
	h.e.GET("/metrics", echoprometheus.NewHandler())
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
	swagger, err := api.GetSpec()
	if err != nil {
		return err
	}
	h.e.Use(oapimiddleware.OapiRequestValidatorWithOptions(swagger, &oapimiddleware.Options{
		Skipper: func(e echo.Context) bool {
			uri := e.Request().RequestURI
			return uri == "/metrics" || uri == "/version"
		},
	}))

	return nil
}
