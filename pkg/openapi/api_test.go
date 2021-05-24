package openapi_test

import (
	"database/sql"
	"fmt"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/rs/zerolog/log"
	"github.com/welthee/dinonce/v2/pkg/openapi"
	"github.com/welthee/dinonce/v2/pkg/psqlticket"
	"testing"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

const Version = 1

var h *openapi.ApiHandler

func init(){
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	defer db.Close()
	if err != nil {
		log.Fatal().Err(err).Msg("can not open db connection")
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	m, err := migrate.NewWithDatabaseInstance("file://pkg/psqlticket/migrations", "postgres", driver)
	if err != nil {
		log.Fatal().Err(err).Msg("can not migrate database schema")
	}

	if err := m.Migrate(Version); err != nil && err != migrate.ErrNoChange {
		log.Fatal().Err(err).Msg("failed to run migrations")
	}

	svc := psqlticket.NewServicer(db)
	h = openapi.NewApiHandler(svc)
}

func TestApiHandler_LeaseTicket_Initial(t *testing.T) {
	//e := echo.New()

}


//func setupEcho() (*echo.Echo, error) {
//	e := echo.New()
//
//}

