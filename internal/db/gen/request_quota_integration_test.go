//go:build integration

// Round-trips against a real Postgres testcontainer for migrations 00021
// (notification type format check) and 00022 (per-user request quotas +
// can_request): the new columns and their CHECKs, the partial
// SetUserRequestPermissions update, the quota count query's window and
// status rules, the admin-recipient query, and both migrations' Down/Up.
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run 'RequestQuota|NotificationTypes'
package gen_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestRequestQuota_Integration_ColumnsAndPartialUpdate(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	u := seedUser(ctx, t, q, "quota-"+suffix)
	p, err := q.GetUserRequestPermissions(ctx, u)
	if err != nil {
		t.Fatalf("GetUserRequestPermissions: %v", err)
	}
	// Existing/new accounts can request, with no per-user quota (= default).
	if !p.CanRequest || p.RequestQuotaMovies != nil || p.RequestQuotaTv != nil || p.Username != "quota-"+suffix {
		t.Errorf("defaults = %+v, want can_request, NULL quotas, username", p)
	}

	// The CHECKs refuse a negative quota.
	if _, err := pool.Exec(ctx, `UPDATE users SET request_quota_movies = -1 WHERE id = $1`, u); err == nil {
		t.Error("negative request_quota_movies accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET request_quota_tv = -1 WHERE id = $1`, u); err == nil {
		t.Error("negative request_quota_tv accepted")
	}

	// Seed toggles, then a partial update touching only can_request + movie
	// quota must leave the toggles and the TV quota alone.
	on, off := true, false
	three, seven := int32(3), int32(7)
	if n, err := q.SetUserRequestPermissions(ctx, gen.SetUserRequestPermissionsParams{
		ID: u, AutoApproveMovies: &on, AutoApproveTv: &on, SetQuotaTv: true, RequestQuotaTv: &seven,
	}); err != nil || n != 1 {
		t.Fatalf("seed update = (%d, %v)", n, err)
	}
	if _, err := q.SetUserRequestPermissions(ctx, gen.SetUserRequestPermissionsParams{
		ID: u, CanRequest: &off, SetQuotaMovies: true, RequestQuotaMovies: &three,
	}); err != nil {
		t.Fatalf("partial update: %v", err)
	}
	p, _ = q.GetUserRequestPermissions(ctx, u)
	if p.CanRequest || !p.AutoApproveMovies || !p.AutoApproveTv ||
		p.RequestQuotaMovies == nil || *p.RequestQuotaMovies != 3 || p.RequestQuotaTv == nil || *p.RequestQuotaTv != 7 {
		t.Errorf("after partial update = %+v, want can_request off, toggles kept, quotas 3/7", p)
	}

	// set_quota_tv with NULL resets to the server default; an unset flag
	// leaves the movie quota as it was.
	if _, err := q.SetUserRequestPermissions(ctx, gen.SetUserRequestPermissionsParams{
		ID: u, SetQuotaTv: true, RequestQuotaTv: nil,
	}); err != nil {
		t.Fatalf("reset update: %v", err)
	}
	p, _ = q.GetUserRequestPermissions(ctx, u)
	if p.RequestQuotaTv != nil || p.RequestQuotaMovies == nil || *p.RequestQuotaMovies != 3 {
		t.Errorf("after reset = movies %v tv %v, want 3 / NULL", p.RequestQuotaMovies, p.RequestQuotaTv)
	}

	// The admin list carries the new fields.
	rows, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	for _, r := range rows {
		if r.ID == u && (r.CanRequest || r.RequestQuotaMovies == nil || *r.RequestQuotaMovies != 3 || r.RequestQuotaTv != nil) {
			t.Errorf("ListUsers row = %+v", r)
		}
	}
}

func TestRequestQuota_Integration_CountRecent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	u := seedUser(ctx, t, q, "count-"+suffix)
	other := seedUser(ctx, t, q, "other-"+suffix)

	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-7 * 24 * time.Hour)
	tmdb := int32(1000)
	insert := func(user uuid.UUID, typ, status string, created time.Time) {
		t.Helper()
		tmdb++
		r, err := q.CreateMediaRequest(ctx, gen.CreateMediaRequestParams{
			UserID: user, Type: typ, TmdbID: tmdb, Title: "t", Status: status,
		})
		if err != nil {
			t.Fatalf("CreateMediaRequest: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE media_requests SET created_at = $2 WHERE id = $1`, r.ID, created); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}
	insert(u, "movie", "pending", now.Add(-time.Hour))      // counts
	insert(u, "movie", "available", since.Add(time.Second)) // counts (just inside)
	insert(u, "movie", "downloading", since)                // exactly at the start: out
	insert(u, "movie", "approved", since.Add(-time.Hour))   // older: out
	insert(u, "movie", "declined", now.Add(-time.Hour))     // declined / cancelled: out
	insert(u, "movie", "failed", now.Add(-2*time.Hour))     // failed still used a slot: counts
	insert(u, "show", "pending", now.Add(-time.Hour))       // other type: out
	insert(other, "movie", "pending", now.Add(-time.Hour))  // other user: out

	n, err := q.CountRecentMediaRequestsForUser(ctx, gen.CountRecentMediaRequestsForUserParams{
		UserID: u, Type: "movie", Since: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		t.Fatalf("CountRecentMediaRequestsForUser: %v", err)
	}
	if n != 3 {
		t.Errorf("movie count = %d, want 3 (pending, available just inside, failed)", n)
	}
	n, _ = q.CountRecentMediaRequestsForUser(ctx, gen.CountRecentMediaRequestsForUserParams{
		UserID: u, Type: "show", Since: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if n != 1 {
		t.Errorf("show count = %d, want 1", n)
	}
}

func TestNotificationTypes_Integration_ConstraintAndAdmins(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	suffix := uuid.New().String()[:8]
	u := seedUser(ctx, t, q, "notif-"+suffix)

	// Every declared type is accepted — the request_* ones used to fail.
	for _, typ := range notification.KnownTypes() {
		if _, err := q.CreateNotification(ctx, gen.CreateNotificationParams{
			UserID: u, Type: typ, Title: "t",
		}); err != nil {
			t.Errorf("CreateNotification(%q): %v", typ, err)
		}
	}
	// Junk is still refused.
	for _, bad := range []string{"", "Request", "request pending", "request-pending", strings.Repeat("a", 65)} {
		if _, err := q.CreateNotification(ctx, gen.CreateNotificationParams{
			UserID: u, Type: bad, Title: "t",
		}); err == nil {
			t.Errorf("CreateNotification(%q) accepted", bad)
		}
	}

	// ListAdminUserIDs: admins only — not regular users, not profiles.
	admin := seedUser(ctx, t, q, "adm-"+suffix)
	if err := q.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: admin, IsAdmin: true}); err != nil {
		t.Fatalf("SetUserAdmin: %v", err)
	}
	if _, err := q.CreateManagedProfile(ctx, gen.CreateManagedProfileParams{
		Username: "kid-" + suffix, ParentUserID: pgtype.UUID{Bytes: admin, Valid: true},
	}); err != nil {
		t.Fatalf("CreateManagedProfile: %v", err)
	}
	ids, err := q.ListAdminUserIDs(ctx)
	if err != nil {
		t.Fatalf("ListAdminUserIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != admin {
		t.Errorf("admin ids = %v, want [%s]", ids, admin)
	}
}

// migrationSections returns a goose SQL file's Up and Down halves.
func migrationSections(t *testing.T, name string) (up, down string) {
	t.Helper()
	raw, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatalf("%s has no Down section", name)
	}
	return parts[0], parts[1]
}

func execSQL(ctx context.Context, t *testing.T, pool *pgxpool.Pool, label, sql string) {
	t.Helper()
	// The goose markers are SQL comments; with no arguments pgx runs the
	// multi-statement text over the simple protocol.
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
}

// Both migrations' Down blocks run against the migrated schema and restore
// the previous shape, and Up re-applies cleanly after them. Executed directly
// (not via goose down-to) so later migrations in the tree don't have to roll
// back for this to be checked.
func TestRequestQuota_Integration_MigrationsDownUp(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	u := seedUser(ctx, t, q, "mig-"+uuid.New().String()[:8])

	// 00021 Down drops rows the old constraint can't hold, then restores it.
	for _, typ := range []string{notification.TypeRequestPending, notification.TypeSystem} {
		if _, err := q.CreateNotification(ctx, gen.CreateNotificationParams{UserID: u, Type: typ, Title: "t"}); err != nil {
			t.Fatalf("seed %s: %v", typ, err)
		}
	}
	up21, down21 := migrationSections(t, "00021_notification_types.sql")
	up22, down22 := migrationSections(t, "00022_request_quotas.sql")

	execSQL(ctx, t, pool, "00022 down", down22)
	var cols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'users' AND column_name IN ('can_request','request_quota_movies','request_quota_tv')`).Scan(&cols); err != nil {
		t.Fatalf("columns: %v", err)
	}
	if cols != 0 {
		t.Errorf("after 00022 down: %d quota columns remain", cols)
	}

	execSQL(ctx, t, pool, "00021 down", down21)
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1`, u).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 1 {
		t.Errorf("after 00021 down: %d notifications, want 1 (the request_pending row dropped)", left)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notifications (user_id, type, title) VALUES ($1, 'request_pending', 't')`, u); err == nil {
		t.Error("after 00021 down: the old constraint should refuse request_pending")
	}

	execSQL(ctx, t, pool, "00021 up", up21)
	execSQL(ctx, t, pool, "00022 up", up22)
	if _, err := pool.Exec(ctx, `INSERT INTO notifications (user_id, type, title) VALUES ($1, 'request_pending', 't')`, u); err != nil {
		t.Errorf("after 00021 up: request_pending refused: %v", err)
	}
	var can bool
	if err := pool.QueryRow(ctx, `SELECT can_request FROM users WHERE id = $1`, u).Scan(&can); err != nil || !can {
		t.Errorf("after 00022 up: can_request = %v (%v), want true for the existing row", can, err)
	}
}
