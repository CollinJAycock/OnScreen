//go:build integration

package userexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The export queries against real Postgres: every file is written from the
// user's rows only, items carry their external ids, a label-hidden item is
// reduced to its id, and no credential material reaches the archive.
func TestExport_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	const pwHash = "$argon2id$v=19$m=65536,t=3,p=4$SECRETHASHSALT$SECRETHASHVALUE"
	hash := pwHash
	email := "alice@example.com"
	alice, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "alice-" + uuid.NewString()[:6], Email: &email, PasswordHash: &hash})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "bob-" + uuid.NewString()[:6]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET totp_secret = 'TOTPSECRETVALUE', totp_enabled = true, pin = 'PINHASHVALUE' WHERE id = $1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Movies-" + uuid.NewString()[:6], Type: "movie", ScanPaths: []string{"/srv/movies"},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	tmdb := int32(603)
	matrix, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{LibraryID: lib.ID, Type: "movie", Title: "The Matrix",
		SortTitle: "matrix", TmdbID: &tmdb, Genres: []string{}, Tags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{LibraryID: lib.ID, Type: "movie", Title: "Hidden Title",
		SortTitle: "hidden", Genres: []string{}, Tags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET labels = ARRAY['hidden'] WHERE id = $1`, secret.ID); err != nil {
		t.Fatal(err)
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	now := time.Now()
	for i, u := range []uuid.UUID{alice.ID, bob.ID} {
		exec(`INSERT INTO watch_events (user_id, media_id, event_type, position_ms, occurred_at) VALUES ($1,$2,'stop',1000,$3)`,
			u, matrix.ID, now.Add(-time.Duration(i)*time.Minute))
	}
	exec(`INSERT INTO watch_events (user_id, media_id, event_type, position_ms, occurred_at) VALUES ($1,$2,'play',0,$3)`,
		alice.ID, secret.ID, now.Add(-time.Hour))
	exec(`INSERT INTO user_ratings (user_id, media_item_id, score) VALUES ($1,$2,9)`, alice.ID, matrix.ID)
	exec(`INSERT INTO user_favorites (user_id, media_id) VALUES ($1,$2)`, alice.ID, matrix.ID)
	exec(`INSERT INTO reading_progress (user_id, media_id, locator, progression, client_updated_at) VALUES ($1,$2,'epubcfi(/6/4)',0.5,now())`, alice.ID, matrix.ID)
	pl, err := q.CreateCollection(ctx, gen.CreateCollectionParams{UserID: pgtype.UUID{Bytes: alice.ID, Valid: true}, Name: "Road trip", Type: "playlist"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCollectionItems(ctx, gen.AppendCollectionItemsParams{CollectionID: pl.ID, ItemIds: []uuid.UUID{matrix.ID}}); err != nil {
		t.Fatal(err)
	}
	client := "Firefox on Linux"
	if _, err := q.CreateSession(ctx, gen.CreateSessionParams{UserID: alice.ID, TokenHash: "REFRESHTOKENHASHVALUE", ClientName: &client,
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, scope) VALUES ($1,'Kometa','APIKEYHASHVALUE','osk_abcdEFGH','full')`, alice.ID)
	exec(`INSERT INTO user_scrobble (user_id, lastfm_session_key, lastfm_username) VALUES ($1,'LASTFMSESSIONKEYVALUE','alice_fm')`, alice.ID)

	e := New(q, Visibility{BlockedLabels: []string{"hidden"}})
	snap, err := e.Collect(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := e.Write(ctx, &buf, snap); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	all := ""
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(b)
		all += string(b)
	}

	for _, secretText := range []string{"SECRETHASH", "TOTPSECRETVALUE", "PINHASHVALUE", "REFRESHTOKENHASHVALUE",
		"APIKEYHASHVALUE", "LASTFMSESSIONKEYVALUE", "Hidden Title"} {
		if strings.Contains(all, secretText) {
			t.Errorf("archive contains %q", secretText)
		}
	}
	var history []struct {
		Item  ItemRef `json:"item"`
		Event string  `json:"event"`
	}
	if err := json.Unmarshal([]byte(files["watch_history.json"]), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Item.Title != "The Matrix" || history[0].Item.ExternalIDs["tmdb"] != "603" ||
		!history[1].Item.Unavailable {
		t.Fatalf("history = %+v (bob's event must not appear; the hidden item is id-only)", history)
	}
	for name, want := range map[string]string{
		"ratings.json":          `"score": 9`,
		"favorites.json":        "The Matrix",
		"reading_progress.json": "epubcfi(/6/4)",
		"playlists.json":        "Road trip",
		"sessions.json":         "Firefox on Linux",
		"api_keys.json":         "osk_abcdEFGH",
		"scrobble.json":         "alice_fm",
		"profile.json":          "alice@example.com",
	} {
		if !strings.Contains(files[name], want) {
			t.Errorf("%s lacks %q:\n%s", name, want, files[name])
		}
	}
	if !strings.Contains(files["profile.json"], `"two_factor_enabled": true`) {
		t.Errorf("profile.json:\n%s", files["profile.json"])
	}
}
