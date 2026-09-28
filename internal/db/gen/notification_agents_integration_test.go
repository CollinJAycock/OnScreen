//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00029
// (notification_agents) and notification_agents.sql: the CHECK constraints,
// create / update / delete, the per-event subscriber list, delivery status
// columns, and the batched new-content read (public libraries only, episodes
// resolved to their show, the watermark and the row cap).
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run NotificationAgents
package gen_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func agentParams(kind, name string, events ...string) gen.CreateNotificationAgentParams {
	return gen.CreateNotificationAgentParams{
		ID: uuid.New(), Kind: kind, Name: name, Enabled: true, Events: events, Config: []byte(`{}`),
	}
}

func TestNotificationAgents_Integration_ConstraintsAndCRUD(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	checkViolation := func(label string, p gen.CreateNotificationAgentParams) {
		t.Helper()
		_, err := q.CreateNotificationAgent(ctx, p)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("%s: err = %v, want a CHECK violation (23514)", label, err)
		}
	}
	checkViolation("unknown kind", agentParams("slack", "x", "test"))
	checkViolation("empty name", agentParams("discord", "", "test"))
	checkViolation("long name", agentParams("discord", strings.Repeat("n", 101), "test"))
	bad := agentParams("ntfy", "x", "test")
	bad.Config = []byte(`["not","an","object"]`)
	checkViolation("array config", bad)
	priv := agentParams("discord", "x", "test")
	priv.AllowPrivateNetwork = true
	checkViolation("private network on discord", priv)

	secret := "ciphertext"
	d := agentParams("discord", "Family", "request_pending", "request_available")
	d.Secret = &secret
	discord, err := q.CreateNotificationAgent(ctx, d)
	if err != nil {
		t.Fatalf("create discord: %v", err)
	}
	if discord.ID != d.ID || !discord.CreatedAt.Valid || discord.LastSuccessAt.Valid || discord.LastError != nil || *discord.Secret != secret {
		t.Errorf("created = %+v", discord)
	}
	n := agentParams("ntfy", "Phones", "request_pending", "task_failed")
	n.AllowPrivateNetwork = true
	n.Config = []byte(`{"server_url":"http://10.0.0.5","topic":"house"}`)
	ntfy, err := q.CreateNotificationAgent(ctx, n)
	if err != nil {
		t.Fatalf("create ntfy (private allowed): %v", err)
	}
	off := agentParams("gotify", "Off", "request_pending")
	off.Enabled = false
	if _, err := q.CreateNotificationAgent(ctx, off); err != nil {
		t.Fatal(err)
	}

	subs, err := q.ListEnabledNotificationAgentsForEvent(ctx, "request_pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 || subs[0].ID != discord.ID || subs[1].ID != ntfy.ID {
		t.Errorf("request_pending subscribers = %+v, want discord then ntfy (enabled only, creation order)", subs)
	}
	if subs, _ := q.ListEnabledNotificationAgentsForEvent(ctx, "task_failed"); len(subs) != 1 || subs[0].ID != ntfy.ID {
		t.Errorf("task_failed subscribers = %+v", subs)
	}
	if all, _ := q.ListNotificationAgents(ctx); len(all) != 3 {
		t.Errorf("list = %d rows, want 3", len(all))
	}

	upd, err := q.UpdateNotificationAgent(ctx, gen.UpdateNotificationAgentParams{
		ID: discord.ID, Name: "Family chat", Enabled: false, Events: []string{"new_content"},
		Config: []byte(`{}`), Secret: nil, AllowPrivateNetwork: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Name != "Family chat" || upd.Enabled || upd.Secret != nil || !upd.UpdatedAt.Time.After(discord.UpdatedAt.Time.Add(-time.Second)) {
		t.Errorf("updated = %+v", upd)
	}
	if _, err := q.UpdateNotificationAgent(ctx, gen.UpdateNotificationAgentParams{ID: uuid.New(), Name: "x", Events: []string{}, Config: []byte(`{}`)}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update missing: %v", err)
	}

	if err := q.RecordNotificationAgentFailure(ctx, gen.RecordNotificationAgentFailureParams{ID: ntfy.ID, LastError: "HTTP 500"}); err != nil {
		t.Fatal(err)
	}
	if err := q.RecordNotificationAgentSuccess(ctx, ntfy.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := q.GetNotificationAgent(ctx, ntfy.ID)
	if got.LastError == nil || *got.LastError != "HTTP 500" || !got.LastErrorAt.Valid || !got.LastSuccessAt.Valid {
		t.Errorf("status columns = %+v", got)
	}
	err = q.RecordNotificationAgentFailure(ctx, gen.RecordNotificationAgentFailureParams{ID: ntfy.ID, LastError: strings.Repeat("e", 501)})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("oversize last_error: %v", err)
	}

	if n, err := q.DeleteNotificationAgent(ctx, discord.ID); err != nil || n != 1 {
		t.Errorf("delete = %d, %v", n, err)
	}
	if n, _ := q.DeleteNotificationAgent(ctx, discord.ID); n != 0 {
		t.Errorf("second delete = %d", n)
	}
	if _, err := q.GetNotificationAgent(ctx, discord.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get deleted: %v", err)
	}
}

func TestNotificationAgents_Integration_NewContent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	sfx := uuid.New().String()[:8]

	newLib := func(name string, private bool) uuid.UUID {
		lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: name + sfx, Type: "show", ScanPaths: []string{"/tmp/" + name + sfx}, Agent: "tmdb", Language: "en",
			ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour, IsPrivate: private,
		})
		if err != nil {
			t.Fatal(err)
		}
		return lib.ID
	}
	pub, private := newLib("pub", false), newLib("priv", true)
	base := time.Now().Add(-time.Hour)
	insert := func(lib uuid.UUID, typ, title string, parent *uuid.UUID, at time.Time) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title, parent_id, created_at, year)
			VALUES ($1, $2, $3, $3, $4, $5, 2024) RETURNING id`, lib, typ, title, parent, at).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}
	old := insert(pub, "movie", "Old Movie", nil, base.Add(-time.Hour))
	_ = old
	show := insert(pub, "show", "Severance", nil, base.Add(time.Minute))
	season := insert(pub, "season", "Season 2", &show, base.Add(2*time.Minute))
	insert(pub, "episode", "Hello, Ms. Cobel", &season, base.Add(3*time.Minute))
	movie := insert(pub, "movie", "Heat", nil, base.Add(4*time.Minute))
	gone := insert(pub, "movie", "Deleted", nil, base.Add(5*time.Minute))
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	insert(private, "movie", "Secret Movie", nil, base.Add(4*time.Minute))

	rows, err := q.ListNewContentForNotificationAgents(ctx, gen.ListNewContentForNotificationAgentsParams{
		LibraryID: pub, Since: pgtype.Timestamptz{Time: base, Valid: true}, MaxRows: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, r := range rows {
		titles = append(titles, r.Type+":"+r.Title)
	}
	want := "movie:Heat,episode:Hello, Ms. Cobel,show:Severance"
	if strings.Join(titles, ",") != want {
		t.Fatalf("new content = %v, want %s (newest first; no season, deleted or pre-watermark rows)", titles, want)
	}
	ep := rows[1]
	if !ep.ShowID.Valid || uuid.UUID(ep.ShowID.Bytes) != show || ep.ShowTitle == nil || *ep.ShowTitle != "Severance" {
		t.Errorf("episode's show = %+v / %v", ep.ShowID, ep.ShowTitle)
	}
	if rows[0].ID != movie || rows[0].Year == nil || *rows[0].Year != 2024 || rows[0].ShowID.Valid {
		t.Errorf("movie row = %+v", rows[0])
	}

	// Watermark and cap.
	rows, _ = q.ListNewContentForNotificationAgents(ctx, gen.ListNewContentForNotificationAgentsParams{
		LibraryID: pub, Since: pgtype.Timestamptz{Time: base.Add(3 * time.Minute), Valid: true}, MaxRows: 50,
	})
	if len(rows) != 1 || rows[0].ID != movie {
		t.Errorf("after watermark = %+v", rows)
	}
	rows, _ = q.ListNewContentForNotificationAgents(ctx, gen.ListNewContentForNotificationAgentsParams{
		LibraryID: pub, Since: pgtype.Timestamptz{Time: base, Valid: true}, MaxRows: 1,
	})
	if len(rows) != 1 {
		t.Errorf("capped = %d rows", len(rows))
	}

	// A private library announces nothing.
	rows, _ = q.ListNewContentForNotificationAgents(ctx, gen.ListNewContentForNotificationAgentsParams{
		LibraryID: private, Since: pgtype.Timestamptz{Time: base, Valid: true}, MaxRows: 50,
	})
	if len(rows) != 0 {
		t.Errorf("private library rows = %+v", rows)
	}
}
