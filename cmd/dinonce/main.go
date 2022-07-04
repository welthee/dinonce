package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/etherlabsio/healthcheck/v2"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/welthee/dinonce/v2/pkg/openapi"
	"github.com/welthee/dinonce/v2/pkg/psqlticket"
	"github.com/welthee/dinonce/v2/pkg/ticket"
)

const ShutDownTimeout = 30 * time.Second

type postgreSQLBackendConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	DatabaseName string
}

const backendKindPostgres = "postgres"
const postgresMigrationsDir = "file://./scripts/psql/migrations"

func main() {
	log.Info().Msg("starting ticketing service")

	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defaultContextLogger := zerolog.New(os.Stdout)
	defaultContextLogger.With().Str("logger", "default-context-logger")
	zerolog.DefaultContextLogger = &defaultContextLogger

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/opt/dinonce/config")
	viper.AddConfigPath("$HOME/.dinonce/config")
	viper.AddConfigPath(".config")

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("can not read config file")
	}

	healthCheckers := make(map[string]healthcheck.CheckerFunc)

	var svc ticket.Servicer
	switch viper.GetString("backendKind") {
	case backendKindPostgres:
		{
			var backendCfg postgreSQLBackendConfig
			if err := viper.UnmarshalKey("backendConfig", &backendCfg); err != nil {
				log.Fatal().Err(err).Msg("can not construct postgres backend")
			}

			psqlConnectionString := fmt.Sprintf("host=%s port=%d user=%s "+
				"password=%s dbname=%s sslmode=disable",
				backendCfg.Host, backendCfg.Port, backendCfg.User,
				backendCfg.Password, backendCfg.DatabaseName)

			db, err := sql.Open("postgres", psqlConnectionString)
			if err != nil {
				log.Fatal().Err(err).Msg("can not open db connection")
			}
			defer func(db *sql.DB) {
				if err := db.Close(); err != nil {
					log.Error().Err(err).Msg("can not close db")
				}
			}(db)

			driver, err := postgres.WithInstance(db, &postgres.Config{})
			if err != nil {
				log.Fatal().Err(err).Msg("can not get database driver")
			}

			m, err := migrate.NewWithDatabaseInstance(postgresMigrationsDir, backendCfg.DatabaseName, driver)
			if err != nil {
				log.Fatal().Err(err).Msg("can not create database migrator")
			}

			if err := m.Up(); err != nil && err != migrate.ErrNoChange {
				log.Fatal().Err(err).Msg("failed to run migrations")
			}

			healthCheckers["database"] = func(ctx context.Context) error {
				return db.PingContext(ctx)
			}

			svc = psqlticket.NewServicer(db)
		}

		apiHandler := openapi.NewApiHandler(svc)

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
}
