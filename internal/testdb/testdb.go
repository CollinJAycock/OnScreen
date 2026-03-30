// Package testdb spins up a real PostgreSQL testcontainer, runs all goose
// migrations, and returns a pgxpool.Pool for integration tests.
//
// Usage:
//
//	func TestSomething(t *testing.T) {
//	    pool := testdb.New(t)
//	    q := gen.New(pool)
//	    // ... use q as you would in production
//	}
//
// Each call creates a fresh database — tests are fully isolated. The container
// is torn down by t.Cleanup when the test finishes.
package testdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/jackc/pgx/v5/stdlib" // goose needs the stdlib driver
)

// New starts a PostgreSQL container, runs all migrations, and returns a pool.
// The container is terminated and the pool closed via t.Cleanup.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	container, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("pgvector/pgvector:pg16"),
		postgres.WithDatabase("onscreen_test"),
		postgres.WithUsername("onscreen"),
		postgres.WithPassword("onscreen"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("testdb: start container: %v", err)
	}

	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("testdb: terminate container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("testdb: connection string: %v", err)
	}

	// Run goose migrations.
	if err := runMigrations(connStr); err != nil {
		t.Fatalf("testdb: migrations: %v", err)
	}

	// Open pgxpool.
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func runMigrations(connStr string) error {
	db, err := goose.OpenDBWithDriver("pgx", connStr)
	if err != nil {
		return fmt.Errorf("open db for migrations: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(nil) // use filesystem (migrations dir)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := goose.UpContext(ctx, db, "internal/db/migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
