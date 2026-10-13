package externalratings

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/omdb"
)

type fakeDB struct {
	ids    []string
	params gen.ListExternalRatingCandidatesParams
	saved  map[string]gen.UpsertExternalRatingsParams
	used   int64 // lookups in the last 24 hours
	since  time.Time
}

func (f *fakeDB) CountExternalRatingLookupsSince(_ context.Context, since pgtype.Timestamptz) (int64, error) {
	f.since = since.Time
	return f.used, nil
}

func (f *fakeDB) ListExternalRatingCandidates(_ context.Context, p gen.ListExternalRatingCandidatesParams) ([]string, error) {
	f.params = p
	if int(p.MaxIds) < len(f.ids) {
		return f.ids[:p.MaxIds], nil
	}
	return f.ids, nil
}

func (f *fakeDB) UpsertExternalRatings(_ context.Context, p gen.UpsertExternalRatingsParams) error {
	f.saved[p.ImdbID] = p
	return nil
}

func (f *fakeDB) CountExternalRatingCandidates(context.Context) (gen.CountExternalRatingCandidatesRow, error) {
	return gen.CountExternalRatingCandidatesRow{WithImdbID: 10, Rated: int64(len(f.saved))}, nil
}

type keys struct {
	key   string
	limit int
}

func (k keys) OMDbAPIKey(context.Context) string { return k.key }
func (k keys) OMDbDailyLimit(context.Context) int {
	if k.limit == 0 {
		return 1000
	}
	return k.limit
}

// fakeOMDb answers per ID: ratings, or an error.
type fakeOMDb struct {
	answers map[string]any
	calls   []string
}

func (f *fakeOMDb) Get(_ context.Context, id string) (omdb.Ratings, error) {
	f.calls = append(f.calls, id)
	switch a := f.answers[id].(type) {
	case omdb.Ratings:
		return a, nil
	case error:
		return omdb.Ratings{}, a
	}
	return omdb.Ratings{}, omdb.ErrNotFound
}

func newService(db *fakeDB, key string, f *fakeOMDb) *Service {
	return newServiceWithLimit(db, keys{key: key}, f)
}

func newServiceWithLimit(db *fakeDB, k keys, f *fakeOMDb) *Service {
	s := New(db, k, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.fetcher = func(string) Fetcher { return f }
	s.interval = 0
	s.now = func() time.Time { return time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC) }
	return s
}

func f64(f float64) *float64 { return &f }
func i16(n int16) *int16     { return &n }

func TestRefresh(t *testing.T) {
	db := &fakeDB{ids: []string{"tt1", "tt2", "tt3"}, saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{answers: map[string]any{
		"tt1": omdb.Ratings{IMDb: f64(8.1), RottenTomatoes: i16(91)},
		"tt3": errors.New("connection reset"),
	}}
	summary, err := newService(db, "k", f).Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := db.saved["tt1"]; !got.Found || *got.ImdbRating != 8.1 || *got.RottenTomatoes != 91 {
		t.Errorf("tt1 saved %+v", got)
	}
	if got, ok := db.saved["tt2"]; !ok || got.Found {
		t.Errorf("tt2 (unknown to OMDb) saved %+v, %v; want found=false", got, ok)
	}
	if _, ok := db.saved["tt3"]; ok {
		t.Error("a failed lookup was saved")
	}
	if !strings.Contains(summary, "1 rated, 1 unknown to OMDb, 1 failed") ||
		!strings.Contains(summary, "2 of the key's 1000 daily lookups used in the last 24 hours") {
		t.Errorf("summary = %q", summary)
	}
	if !db.since.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("lookups counted since %v, want the last 24 hours", db.since)
	}
	p := db.params
	if p.RecentYear != 2025 || p.MaxIds != 1000 ||
		!p.RecentBefore.Time.Equal(time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("candidate params = %+v", p)
	}
}

func TestRefresh_NoKey(t *testing.T) {
	db := &fakeDB{ids: []string{"tt1"}, saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{}
	summary, err := newService(db, "", f).Refresh(context.Background())
	if err != nil || !strings.Contains(summary, "no OMDb API key") || len(f.calls) != 0 {
		t.Errorf("no key: %q %v, %d calls", summary, err, len(f.calls))
	}
}

func TestRefresh_StopsAtTheDailyLimit(t *testing.T) {
	db := &fakeDB{ids: []string{"tt1", "tt2", "tt3"}, saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{answers: map[string]any{"tt1": omdb.Ratings{}, "tt2": omdb.ErrLimitReached, "tt3": omdb.Ratings{}}}
	summary, err := newService(db, "k", f).Refresh(context.Background())
	if err != nil || len(f.calls) != 2 || !strings.Contains(summary, "daily limit") {
		t.Errorf("limit: %q %v, calls %v", summary, err, f.calls)
	}
}

func TestRefresh_BadKeyFails(t *testing.T) {
	db := &fakeDB{ids: []string{"tt1", "tt2"}, saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{answers: map[string]any{"tt1": omdb.ErrInvalidKey}}
	if _, err := newService(db, "k", f).Refresh(context.Background()); err == nil || len(f.calls) != 1 {
		t.Errorf("bad key: %v, %d calls", err, len(f.calls))
	}
}

func TestRefresh_GivesUpWhenOMDbIsDown(t *testing.T) {
	db := &fakeDB{saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{answers: map[string]any{}}
	for i := range 10 {
		id := "tt" + string(rune('a'+i))
		db.ids = append(db.ids, id)
		f.answers[id] = errors.New("timeout")
	}
	if _, err := newService(db, "k", f).Refresh(context.Background()); err == nil || len(f.calls) != maxFailures {
		t.Errorf("down: %v after %d calls, want an error after %d", err, len(f.calls), maxFailures)
	}
}

func TestRefresh_DailyLimit(t *testing.T) {
	ids := []string{"tt1", "tt2", "tt3", "tt4"}
	cases := []struct {
		name      string
		limit     int
		used      int64
		maxPerRun int
		calls     int
		skipped   bool
	}{
		{"what's left of the limit", 10, 8, maxPerRun, 2, false},
		{"limit used up", 10, 10, maxPerRun, 0, true},
		{"over the limit (a lowered setting)", 5, 9, maxPerRun, 0, true},
		{"capped per run", 1000, 0, 3, 3, false},
	}
	for _, c := range cases {
		db := &fakeDB{ids: ids, used: c.used, saved: map[string]gen.UpsertExternalRatingsParams{}}
		f := &fakeOMDb{}
		s := newServiceWithLimit(db, keys{key: "k", limit: c.limit}, f)
		s.maxPerRun = c.maxPerRun
		summary, err := s.Refresh(context.Background())
		if err != nil || len(f.calls) != c.calls || strings.HasPrefix(summary, "skipped") != c.skipped {
			t.Errorf("%s: %q %v, %d calls; want %d calls, skipped %v", c.name, summary, err, len(f.calls), c.calls, c.skipped)
		}
	}
}

func TestRefresh_OneRunAtATime(t *testing.T) {
	db := &fakeDB{ids: []string{"tt1"}, saved: map[string]gen.UpsertExternalRatingsParams{}}
	f := &fakeOMDb{}
	s := newService(db, "k", f)
	s.running.Lock() // a run in progress
	summary, err := s.Refresh(context.Background())
	s.running.Unlock()
	if err != nil || !strings.Contains(summary, "another run is in progress") || len(f.calls) != 0 {
		t.Errorf("overlapping run: %q %v, %d calls", summary, err, len(f.calls))
	}
	if _, err := s.Refresh(context.Background()); err != nil || len(f.calls) != 1 {
		t.Errorf("next run: %v, %d calls", err, len(f.calls))
	}
}
