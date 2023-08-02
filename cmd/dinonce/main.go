package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/etherlabsio/healthcheck/v2"
	"github.com/rs/zerolog/log"

	"github.com/welthee/dinonce/v2/internal/api"
	"github.com/welthee/dinonce/v2/internal/config"
	"github.com/welthee/dinonce/v2/internal/infra"
	"github.com/welthee/dinonce/v2/internal/ticket"
	"github.com/welthee/dinonce/v2/internal/ticket/psql"
)

const ShutDownTimeout = 30 * time.Second

func main() {
	cfg, err := config.NewConfigFromFile()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration file.")
	}

	config.ConfigureLogger(*cfg.Logger)

	var svc ticket.Servicer
	healthCheckers := make(map[string]healthcheck.CheckerFunc)

	switch cfg.BackendKind {
	case config.BackendKindPostgres:
		db, err := infra.NewPostgresClient(*cfg.BackendPostgres)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get a new client.")
		}
		defer func(db *sql.DB) {
			if err := db.Close(); err != nil {
				log.Error().Err(err).Msgf("Close connection to %s.", config.BackendKindPostgres)
			}
		}(db)

		if err := infra.ExecutePostgresMigrations(db, *cfg.BackendPostgres); err != nil {
			log.Fatal().Err(err).Msg("Failed to run migrations.")
		}

		healthCheckers["database"] = infra.PostgresHealthcheckFn(db)

		svc = psql.NewServicer(db)
	}

	log.Info().Msg("starting ticketing service")

	apiHandler := api.NewHandler(svc)

	go func() {
		if err := apiHandler.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("can not start API")
		}
		log.Info().Msg("API shut down")
	}()

	go func() {
		var opts []healthcheck.Option
		for k, v := range healthCheckers {
			opts = append(opts, healthcheck.WithChecker(k, v))
		}
		opts = append(opts, healthcheck.WithTimeout(5*time.Second))

		if err := http.ListenAndServe(":5001", healthcheck.Handler(opts...)); err != nil &&
			err != http.ErrServerClosed {

			log.Fatal().Err(err).Msg("can not start healthcheck handler")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	log.Info().Msg("stopping ticketing service")

	ctx, cancel := context.WithTimeout(context.Background(), ShutDownTimeout)
	defer cancel()

	if err := apiHandler.Stop(ctx); err != nil {
		log.Fatal().Err(err).Msg("error on graceful shutdown of API")
	}
	log.Info().Msg("stopped ticketing service")
}
