package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/DIMO-Network/shared/pkg/db"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testPostgres is one throwaway Postgres for the package's DB-backed tests,
// started on first use and migrated the way the migrate subcommand does it.
var testPostgres struct {
	once      sync.Once
	container *postgres.PostgresContainer
	settings  db.Settings
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if c := testPostgres.container; c != nil {
		_ = c.Terminate(context.Background())
	}
	os.Exit(code)
}

// migratedStore returns a store on the package's test Postgres. It skips the
// test when there is no Docker to run Postgres in.
func migratedStore(t *testing.T) *db.Store {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	testPostgres.once.Do(startTestPostgres)
	require.NoError(t, testPostgres.err, "starting the test Postgres")
	store := db.NewDbConnectionFromSettings(context.Background(), &testPostgres.settings, true)
	store.WaitForDB(zerolog.Nop())
	return &store
}

func startTestPostgres() {
	ctx := context.Background()
	// The schema has the database's name, as in every environment
	// (search_path in db.Settings.BuildConnectionString).
	const name = "fleet_lite_app"
	// The official image via AWS's public mirror: anonymous Docker Hub pulls
	// are rate limited, and CI runs this without a Docker Hub login.
	c, err := postgres.Run(ctx, "public.ecr.aws/docker/library/postgres:16-alpine",
		postgres.WithDatabase(name),
		postgres.WithUsername("dimo"),
		postgres.WithPassword("dimo"),
		postgres.BasicWaitStrategies(),
	)
	testPostgres.container = c
	if err != nil {
		testPostgres.err = err
		return
	}
	host, err := c.Host(ctx)
	if err != nil {
		testPostgres.err = err
		return
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		testPostgres.err = err
		return
	}
	settings := db.Settings{
		User: "dimo", Password: "dimo", Host: host, Port: port.Port(),
		Name: name, SSLMode: "disable", MaxOpenConnections: 10, MaxIdleConnections: 2,
	}
	testPostgres.settings = settings
	testPostgres.err = migrateTestDB(ctx, settings)
}

func migrateTestDB(ctx context.Context, settings db.Settings) error {
	conn, err := sql.Open("postgres", settings.BuildConnectionString(true))
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", settings.Name)); err != nil {
		return err
	}
	goose.SetTableName(settings.Name + ".migrations")
	return goose.UpContext(ctx, conn, "../db/migrations")
}
