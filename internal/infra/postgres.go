package infra

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/welthee/dinonce/v2/internal/config"
)

func NewPostgresClient(cfg config.PostgreSQLBackendConfig) (*sql.DB, error) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Host, cfg.Port, cfg.User,
		cfg.Password, cfg.DatabaseName)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open connection to %s: %w", config.BackendKindPostgres, err)
	}

	return db, nil
}

func ExecutePostgresMigrations(db *sql.DB, cfg config.PostgreSQLBackendConfig) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithDatabaseInstance(cfg.MigrationsDir, cfg.DatabaseName, driver)
	if err != nil {
		return fmt.Errorf("create a database migrator: %w", err)
	}

	if err := m.Up(); err != nil {
		if err != migrate.ErrNoChange {
			return fmt.Errorf("%s migrations: %w", config.BackendKindPostgres, err)
		}
	}

	return nil
}

func PostgresHealthcheckFn(db *sql.DB) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return db.PingContext(ctx)
	}
}
