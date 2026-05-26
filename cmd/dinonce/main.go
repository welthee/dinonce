package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/etherlabsio/healthcheck/v2"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/matelang/dinonce/v3/internal/api"
	"github.com/matelang/dinonce/v3/internal/ticket"
	"github.com/matelang/dinonce/v3/internal/ticket/psql"
)

// Build metadata injected at link time via -ldflags. Defaults make the
// uninjected binary still report useful information.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const (
	shutDownTimeout       = 30 * time.Second
	healthCheckTimeout    = 5 * time.Second
	postgresMigrationsDir = "file://./scripts/psql/migrations"
	backendKindPostgres   = "postgres"
)

type postgreSQLBackendConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	DatabaseName string
	// Optional pool tuning. Zero values fall back to safe defaults.
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

func main() {
	showVersion := flag.Bool("version", false, "print build information and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("dinonce %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/opt/dinonce/config")
	viper.AddConfigPath("$HOME/.dinonce/config")
	viper.AddConfigPath(".config")

	viper.SetEnvPrefix("DINONCE")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Fatal().Err(err).Msg("can not read config file")
	}

	configureLogger()

	healthCheckers := make(map[string]healthcheck.CheckerFunc)

	var svc ticket.Servicer
	var dbCloser func()

	switch viper.GetString("backendKind") {
	case backendKindPostgres:
		db, closer, err := openPostgres()
		if err != nil {
			log.Fatal().Err(err).Msg("can not initialise postgres backend")
		}
		dbCloser = closer

		healthCheckers["database"] = func(ctx context.Context) error {
			return db.PingContext(ctx)
		}
		svc = psql.NewServicer(db)
	default:
		log.Fatal().
			Str("backendKind", viper.GetString("backendKind")).
			Msg("unsupported backendKind; expected one of: postgres")
	}

	log.Info().
		Str("version", version).
		Str("commit", commit).
		Msg("starting ticketing service")

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	apiHandler := api.NewHandler(svc, api.BuildInfo{Version: version, Commit: commit, Date: date})

	apiErrCh := make(chan error, 1)
	go func() {
		if err := apiHandler.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiErrCh <- err
		}
		close(apiErrCh)
	}()

	healthServer := newHealthServer(healthCheckers)
	healthErrCh := make(chan error, 1)
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErrCh <- err
		}
		close(healthErrCh)
	}()

	select {
	case <-rootCtx.Done():
		log.Info().Msg("shutdown signal received; stopping ticketing service")
	case err := <-apiErrCh:
		if err != nil {
			log.Error().Err(err).Msg("API server exited unexpectedly")
		}
	case err := <-healthErrCh:
		if err != nil {
			log.Error().Err(err).Msg("healthcheck server exited unexpectedly")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutDownTimeout)
	defer cancel()

	if err := apiHandler.Stop(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("error on graceful shutdown of API")
	}
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("error on graceful shutdown of healthcheck server")
	}
	if dbCloser != nil {
		dbCloser()
	}

	log.Info().Msg("stopped ticketing service")
}

func configureLogger() {
	logLevelStr := viper.GetString("logger.level")
	if logLevelStr == "" {
		logLevelStr = zerolog.InfoLevel.String()
	}

	logLevel, err := zerolog.ParseLevel(logLevelStr)
	if err != nil {
		log.Fatal().Str("provided", logLevelStr).Msg("invalid log level")
	}

	switch viper.GetString("logger.kind") {
	case "console":
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
		log.Info().Str("level", logLevel.String()).Msg("using console logger")
	default:
		log.Info().Str("level", logLevel.String()).Msg("using JSON logger")
	}

	zerolog.SetGlobalLevel(logLevel)
	zerolog.DefaultContextLogger = &log.Logger
}

func openPostgres() (*sql.DB, func(), error) {
	var cfg postgreSQLBackendConfig
	if err := viper.UnmarshalKey("backendConfig", &cfg); err != nil {
		return nil, nil, fmt.Errorf("decode backendConfig: %w", err)
	}

	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DatabaseName)

	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	} else {
		db.SetMaxOpenConns(25)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	} else {
		db.SetMaxIdleConns(5)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	} else {
		db.SetConnMaxLifetime(30 * time.Minute)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("acquire migrator driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(postgresMigrationsDir, cfg.DatabaseName, driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		_ = db.Close()
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}

	closer := func() {
		if err := db.Close(); err != nil {
			log.Error().Err(err).Msg("can not close db")
		}
	}
	return db, closer, nil
}

func newHealthServer(checkers map[string]healthcheck.CheckerFunc) *http.Server {
	opts := []healthcheck.Option{healthcheck.WithTimeout(healthCheckTimeout)}
	for k, v := range checkers {
		opts = append(opts, healthcheck.WithChecker(k, v))
	}

	mux := http.NewServeMux()
	// /readyz checks dependencies (the registered checkers).
	mux.Handle("/readyz", healthcheck.Handler(opts...))
	// /livez signals only that the process is alive; it must never depend on
	// external systems so a flapping DB does not cause pod restarts.
	mux.HandleFunc("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// Back-compat: the original implementation served the dependency
	// healthcheck on the root path.
	mux.Handle("/", healthcheck.Handler(opts...))

	return &http.Server{
		Addr:              ":5001",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

