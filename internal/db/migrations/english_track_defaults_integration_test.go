//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

const englishTrackDefaultsVersion = 66

// TestEnglishTrackDefaults_Integration: accounts with no audio or subtitle
// language get English, subtitles in the "foreign" mode; a subtitle language
// someone chose keeps the mode its forced-only switch meant (on → forced,
// off → always); new accounts start in English + foreign; the quality-profile
// UPDATE keeps "foreign" when an older app sends only the boolean; Down
// restores the old column defaults.
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
		audio, sub, mode string
		forced           bool
	}
	read := func() map[string]prefs {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT username, COALESCE(preferred_audio_lang, '<null>'),
			       COALESCE(preferred_subtitle_lang, '<null>'), subtitle_mode, forced_subtitles_only
			  FROM users WHERE username = ANY($1)`, []string{"none", "blank", "chosen", "forced", "subonly", "new"})
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]prefs{}
		for rows.Next() {
			var name string
			var p prefs
			if err := rows.Scan(&name, &p.audio, &p.sub, &p.mode, &p.forced); err != nil {
				t.Fatal(err)
			}
			got[name] = p
		}
		return got
	}
	want := map[string]prefs{
		"none":    {"eng", "eng", "foreign", true},
		"blank":   {"eng", "eng", "foreign", true},
		"chosen":  {"jpn", "spa", "always", false},
		"forced":  {"fra", "fra", "forced", true},
		"subonly": {"eng", "deu", "always", false},
		"new":     {"eng", "eng", "foreign", true},
	}
	got := read()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %+v, want %+v", name, got[name], w)
		}
	}

	// The quality-profile write. An older app sends only the boolean:
	// true keeps "foreign" (and makes "always" "forced"), false means
	// "always". A newer app's subtitle_mode wins, and the boolean follows it.
	q := gen.New(pool)
	idOf := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE username = $1`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mode := func(s string) *string { return &s }
	for _, step := range []struct {
		user       string
		forced     bool
		mode       *string
		wantMode   string
		wantForced bool
	}{
		{"new", true, nil, "foreign", true},     // old app, forced on: foreign survives
		{"chosen", true, nil, "forced", true},   // old app turns forced-only on over "always"
		{"forced", false, nil, "always", false}, // old app turns it off
		{"none", false, mode("foreign"), "foreign", true},
		{"blank", true, mode("always"), "always", false},
	} {
		if err := q.UpdateUserQualityProfile(ctx, gen.UpdateUserQualityProfileParams{
			ID: idOf(step.user), ForcedSubtitlesOnly: step.forced, SubtitleMode: step.mode,
		}); err != nil {
			t.Fatalf("%s: %v", step.user, err)
		}
		if p := read()[step.user]; p.mode != step.wantMode || p.forced != step.wantForced {
			t.Errorf("%s after the write: mode %q forced %v, want %q %v", step.user, p.mode, p.forced, step.wantMode, step.wantForced)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET subtitle_mode = 'sometimes' WHERE username = 'new'`); err == nil {
		t.Error("subtitle_mode accepted a value outside always/forced/foreign")
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
