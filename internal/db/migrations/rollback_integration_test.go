//go:build integration

// Down-block sanity check: every migration's Down block must at least
// parse and run on an empty schema. This isn't a full data-preservation
// proof — that needs per-migration seed fixtures we haven't written —
// but it catches the most common bug class: a Down block that doesn't
// even compile, references columns that no longer exist, or omits a
// dropped object.
//
// Gated by the `integration` tag because it needs Docker.
//
// Run with: go test -tags=integration ./internal/db/migrations/
package migrations_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestMigrations_RoundTrip_Integration runs `up → down-to 0 → up` against
// a real Postgres testcontainer and verifies the final applied version
// matches the highest embedded migration. testdb.NewWithDSN already runs
// `up` once during setup; we then roll everything back and re-apply.
func TestMigrations_RoundTrip_Integration(t *testing.T) {
	_, dsn := testdb.NewWithDSN(t)

	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	ctx := context.Background()

	// Roll all the way back. Any Down block that fails to parse or runs
	// against a column it expects to find but doesn't will surface here.
	if err := goose.DownToContext(ctx, db, ".", 0); err != nil {
		t.Fatalf("goose down-to 0: %v", err)
	}

	// Then forward to head. This re-runs every Up block on the now-empty
	// schema, catching cases where the Down block left an object behind
	// that the Up block then collides with.
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("goose up after down: %v", err)
	}

	// Sanity: the final applied version should match what the embedded
	// migrations claim. If they diverge, either the round trip skipped
	// a file or HighestVersion has a bug.
	expected, err := migrations.Highest()
	if err != nil {
		t.Fatalf("Highest: %v", err)
	}
	current, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("get db version: %v", err)
	}
	if current != expected {
		t.Errorf("after round trip: db version %d, expected highest embedded %d", current, expected)
	}
}
