package observability

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// VersionQuerier is implemented by anything that can return the highest
// goose-applied migration version. The pgx pool satisfies this through a
// thin adapter in main.go so this package stays driver-agnostic.
type VersionQuerier interface {
	MaxAppliedVersion(ctx context.Context) (int64, error)
}

// MigrationStatus captures what the binary expects vs what the DB has applied.
// Pending > 0 means the DB is behind the code — code may reference tables or
// columns the schema doesn't have yet, which presents as 500s from any
// endpoint that touches the new schema (see v1.1.2 favorites incident).
type MigrationStatus struct {
	Expected int64 // highest version number found in the embedded migrations
	Applied  int64 // highest version_id in goose_db_version
	Pending  int64 // Expected - Applied (0 = caught up)
}

// CheckMigrations reads the embedded migration filenames, finds the highest
// NNNNN_*.sql version, and compares it against the DB's goose_db_version.
func CheckMigrations(ctx context.Context, vq VersionQuerier, migFS fs.FS) (MigrationStatus, error) {
	expected, err := highestEmbeddedVersion(migFS)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("scan embedded migrations: %w", err)
	}
	applied, err := vq.MaxAppliedVersion(ctx)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("query goose_db_version: %w", err)
	}
	pending := expected - applied
	if pending < 0 {
		pending = 0 // DB ahead of code (e.g. mid-rollback) — not our concern here
	}
	return MigrationStatus{Expected: expected, Applied: applied, Pending: pending}, nil
}

func highestEmbeddedVersion(migFS fs.FS) (int64, error) {
	entries, err := fs.ReadDir(migFS, ".")
	if err != nil {
		return 0, err
	}
	var max int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		// Filename pattern: NNNNN_description.sql
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			continue
		}
		v, err := strconv.ParseInt(e.Name()[:idx], 10, 64)
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}
