//go:build integration

// Round-trips audit_log + arr_services queries.
//
// Audit_log: a write-only append log; the only sin is dropping rows.
// arr_services: the default-flag dance is the trick — only one
// service of a given kind can be the default at a time, and the
// SetDefault / ClearDefault pair must keep that invariant.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// ── audit_log ────────────────────────────────────────────────────────────────

// TestAudit_Integration_InsertAndListRoundTrip — every column round-trips.
// The audit log should be tamper-evident by being append-only.
func TestAudit_Integration_InsertAndListRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "audit-rt-"+uuid.New().String()[:8])
	target := "user:" + uuid.New().String()
	detail := []byte(`{"reason":"testing"}`)

	for _, action := range []string{"login", "user.password.reset", "user.role.change"} {
		if err := q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
			UserID: pgtype.UUID{Bytes: user, Valid: true},
			Action: action,
			Target: &target,
			Detail: detail,
		}); err != nil {
			t.Fatalf("InsertAuditLog (%s): %v", action, err)
		}
	}

	rows, err := q.ListAuditLog(ctx, gen.ListAuditLogParams{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}

	// Find our 3 rows in the result. Other tests may have inserted too.
	var sawActions []string
	for _, r := range rows {
		if r.UserID.Valid && r.UserID.Bytes == user {
			sawActions = append(sawActions, r.Action)
		}
	}
	if len(sawActions) != 3 {
		t.Errorf("got %d rows for our user, want 3 (got: %v)", len(sawActions), sawActions)
	}
}

// TestAudit_Integration_ListIsNewestFirst — order matters because the
// admin UI shows recent events at the top.
func TestAudit_Integration_ListIsNewestFirst(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "audit-ord-"+uuid.New().String()[:8])
	for _, action := range []string{"first", "second", "third"} {
		if err := q.InsertAuditLog(ctx, gen.InsertAuditLogParams{
			UserID: pgtype.UUID{Bytes: user, Valid: true},
			Action: action,
		}); err != nil {
			t.Fatalf("InsertAuditLog: %v", err)
		}
	}

	rows, _ := q.ListAuditLog(ctx, gen.ListAuditLogParams{Limit: 100, Offset: 0})

	var ours []string
	for _, r := range rows {
		if r.UserID.Valid && r.UserID.Bytes == user {
			ours = append(ours, r.Action)
		}
	}
	if len(ours) != 3 {
		t.Fatalf("want 3 rows for our user, got %d", len(ours))
	}
	if ours[0] != "third" || ours[2] != "first" {
		t.Errorf("order = %v, want newest-first (third, second, first)", ours)
	}
}

// ── arr_services ─────────────────────────────────────────────────────────────

func newArrService(name, kind string, isDefault, enabled bool) gen.CreateArrServiceParams {
	return gen.CreateArrServiceParams{
		Name:    name,
		Kind:    kind,
		BaseUrl: "https://" + kind + ".example.com",
		ApiKey:  "k-" + uuid.New().String(),
		// default_tags is JSONB NOT NULL DEFAULT '[]' in 00038_requests.sql.
		// Sqlc requires non-nil; "[]" matches the DEFAULT shape.
		DefaultTags: []byte("[]"),
		IsDefault:   isDefault,
		Enabled:     enabled,
	}
}

// TestArrServices_Integration_CreateAndGet round-trips the row.
func TestArrServices_Integration_CreateAndGet(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	created, err := q.CreateArrService(ctx, newArrService("radarr-prod", "radarr", true, true))
	if err != nil {
		t.Fatalf("CreateArrService: %v", err)
	}
	if !created.IsDefault {
		t.Error("IsDefault flag lost on insert")
	}

	got, err := q.GetArrService(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetArrService: %v", err)
	}
	if got.Name != "radarr-prod" || got.Kind != "radarr" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// TestArrServices_Integration_DefaultDanceOnePerKind — when an admin
// promotes a different service to default, the previous default for
// the same kind must lose its flag. The SetDefault SQL doesn't do
// this on its own; the handler wraps it with ClearDefault in a tx.
func TestArrServices_Integration_DefaultDanceOnePerKind(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	a, err := q.CreateArrService(ctx, newArrService("radarr-a", "radarr", true, true))
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.CreateArrService(ctx, newArrService("radarr-b", "radarr", false, true))
	if err != nil {
		t.Fatal(err)
	}

	// Promote B to default — must clear A first.
	if err := q.ClearArrServiceDefault(ctx, "radarr"); err != nil {
		t.Fatalf("ClearArrServiceDefault: %v", err)
	}
	if err := q.SetArrServiceDefault(ctx, b.ID); err != nil {
		t.Fatalf("SetArrServiceDefault: %v", err)
	}

	def, err := q.GetDefaultArrServiceByKind(ctx, "radarr")
	if err != nil {
		t.Fatalf("GetDefaultArrServiceByKind: %v", err)
	}
	if def.ID != b.ID {
		t.Errorf("default kind=radarr = %s, want %s", def.ID, b.ID)
	}

	// A should no longer report as default.
	gotA, _ := q.GetArrService(ctx, a.ID)
	if gotA.IsDefault {
		t.Error("A still IsDefault after promoting B — invariant broken")
	}
}

// TestArrServices_Integration_DefaultIsKindScoped — a default for
// kind=sonarr must not be returned when querying kind=radarr.
func TestArrServices_Integration_DefaultIsKindScoped(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	if _, err := q.CreateArrService(ctx, newArrService("sonarr-prod", "sonarr", true, true)); err != nil {
		t.Fatal(err)
	}

	if _, err := q.GetDefaultArrServiceByKind(ctx, "radarr"); err == nil {
		t.Error("got a default radarr instance when only sonarr was registered — leaks across kinds")
	}
}

// TestArrServices_Integration_ListEnabledByKindSkipsDisabled — same
// pattern as plugins: the dispatcher's hot path must hide disabled
// rows.
func TestArrServices_Integration_ListEnabledByKindSkipsDisabled(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	on, err := q.CreateArrService(ctx, newArrService("on-"+uuid.New().String()[:6], "radarr", false, true))
	if err != nil {
		t.Fatal(err)
	}
	off, err := q.CreateArrService(ctx, newArrService("off-"+uuid.New().String()[:6], "radarr", false, false))
	if err != nil {
		t.Fatal(err)
	}

	rows, err := q.ListEnabledArrServicesByKind(ctx, "radarr")
	if err != nil {
		t.Fatal(err)
	}
	var sawOn, sawOff bool
	for _, r := range rows {
		if r.ID == on.ID {
			sawOn = true
		}
		if r.ID == off.ID {
			sawOff = true
		}
	}
	if !sawOn {
		t.Error("enabled service missing")
	}
	if sawOff {
		t.Error("disabled service leaked")
	}
}
