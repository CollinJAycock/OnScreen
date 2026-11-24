//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

const englishTrackDefaultsVersion = 66

// TestEnglishTrackDefaults_Integration: accounts with no audio or subtitle
// language get English (subtitles forced-only); a language someone chose,
// and their forced-only setting, are left alone; new accounts start in
// English; Down restores the old column defaults.
//
// Run with: go test -tags 'dev integration' -run EnglishTrackDefaults ./internal/db/migrations/
func TestEnglishTrackDefaults_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownToContext(ctx, db, ".", englishTrackDefaultsVersion-1); err != nil {
		t.Fatalf("down to %d: %v", englishTrackDefaultsVersion-1, err)
	}

	// Before: no choice (NULL), an older client's "", and real choices.
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (username, preferred_audio_lang, preferred_subtitle_lang, forced_subtitles_only) VALUES
		  ('none',    NULL,  NULL,  false),
		  ('blank',   '',    '',    false),
		  ('chosen',  'jpn', 'spa', false),
		  ('forced',  'fra', 'fra', true),
		  ('subonly', NULL,  'deu', false)`); err != nil {
		t.Fatal(err)
	}

	if err := goose.UpToContext(ctx, db, ".", englishTrackDefaultsVersion); err != nil {
		t.Fatalf("up to %d: %v", englishTrackDefaultsVersion, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (username) VALUES ('new')`); err != nil {
		t.Fatal(err)
	}

	type prefs struct {
		audio, sub string
		forced     bool
	}
	want := map[string]prefs{
		"none":    {"eng", "eng", true},
		"blank":   {"eng", "eng", true},
		"chosen":  {"jpn", "spa", false},
		"forced":  {"fra", "fra", true},
		"subonly": {"eng", "deu", false},
		"new":     {"eng", "eng", true},
	}
	rows, err := pool.Query(ctx, `
		SELECT username, COALESCE(preferred_audio_lang, '<null>'),
		       COALESCE(preferred_subtitle_lang, '<null>'), forced_subtitles_only
		  FROM users WHERE username = ANY($1)`, []string{"none", "blank", "chosen", "forced", "subonly", "new"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]prefs{}
	for rows.Next() {
		var name string
		var p prefs
		if err := rows.Scan(&name, &p.audio, &p.sub, &p.forced); err != nil {
			t.Fatal(err)
		}
		got[name] = p
	}
	rows.Close()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %+v, want %+v", name, got[name], w)
		}
	}

	// "File default" is still a choice: a NULL written after the upgrade stays NULL.
	if _, err := pool.Exec(ctx, `UPDATE users SET preferred_subtitle_lang = NULL WHERE username = 'new'`); err != nil {
		t.Fatal(err)
	}
	var sub sql.NullString
	if err := pool.QueryRow(ctx, `SELECT preferred_subtitle_lang FROM users WHERE username = 'new'`).Scan(&sub); err != nil {
		t.Fatal(err)
	}
	if sub.Valid {
		t.Errorf("cleared subtitle language reads %q, want NULL", sub.String)
	}

	if err := goose.DownToContext(ctx, db, ".", englishTrackDefaultsVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (username) VALUES ('after_down')`); err != nil {
		t.Fatal(err)
	}
	var audio, subAfter sql.NullString
	var forced bool
	if err := pool.QueryRow(ctx, `SELECT preferred_audio_lang, preferred_subtitle_lang, forced_subtitles_only FROM users WHERE username = 'after_down'`).
		Scan(&audio, &subAfter, &forced); err != nil {
		t.Fatal(err)
	}
	if audio.Valid || subAfter.Valid || forced {
		t.Errorf("after Down a new account has audio=%v sub=%v forced=%v, want NULL, NULL, false", audio, subAfter, forced)
	}
}
