package serverimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeDB records the service's writes. A write "changes a row" the first
// time it's seen, as the real queries' conflict rules do for a re-run.
type fakeDB struct {
	mu        sync.Mutex
	cands     Candidates
	jobs      map[uuid.UUID]gen.ImportJob
	running   bool
	seen      map[string]bool
	events    []gen.ImportWatchEventParams
	marks     []gen.ImportWatchedMarkParams
	playlists map[string]uuid.UUID
	items     []gen.ImportPlaylistItemParams
	ratings   int
	favorites int
}

func newFakeDB(c Candidates) *fakeDB {
	return &fakeDB{cands: c, jobs: map[uuid.UUID]gen.ImportJob{}, seen: map[string]bool{}, playlists: map[string]uuid.UUID{}}
}

func (f *fakeDB) first(key string) int64 {
	if f.seen[key] {
		return 0
	}
	f.seen[key] = true
	return 1
}

func (f *fakeDB) CreateImportJob(_ context.Context, p gen.CreateImportJobParams) (gen.ImportJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return gen.ImportJob{}, &pgconn.PgError{Code: "23505"}
	}
	f.running = true
	j := gen.ImportJob{ID: uuid.New(), SourceKind: p.SourceKind, SourceUrl: p.SourceUrl, ServerName: p.ServerName,
		Status: StatusRunning, DryRun: p.DryRun, Options: p.Options, Mapping: p.Mapping, Phase: p.Phase}
	f.jobs[j.ID] = j
	return j, nil
}

func (f *fakeDB) GetImportJob(_ context.Context, id uuid.UUID) (gen.ImportJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return gen.ImportJob{}, pgx.ErrNoRows
	}
	return j, nil
}

func (f *fakeDB) ListImportJobs(context.Context, int32) ([]gen.ImportJob, error) { return nil, nil }

func (f *fakeDB) UpdateImportJobProgress(_ context.Context, p gen.UpdateImportJobProgressParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[p.ID]
	j.Phase, j.Stats = p.Phase, p.Stats
	f.jobs[p.ID] = j
	return nil
}

func (f *fakeDB) FinishImportJob(_ context.Context, p gen.FinishImportJobParams) (gen.ImportJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[p.ID]
	j.Status, j.Phase, j.Stats, j.Unmatched, j.Error = p.Status, p.Phase, p.Stats, p.Unmatched, p.Error
	f.jobs[p.ID] = j
	f.running = false
	return j, nil
}

func (f *fakeDB) FailStaleImportJobs(context.Context, pgtype.Timestamptz) (int64, error) {
	return 0, nil
}

func (f *fakeDB) ListImportCandidateItems(context.Context) ([]gen.ListImportCandidateItemsRow, error) {
	return f.cands.Items, nil
}

func (f *fakeDB) ListImportCandidateEpisodes(context.Context) ([]gen.ListImportCandidateEpisodesRow, error) {
	return f.cands.Episodes, nil
}

func (f *fakeDB) ListImportCandidateFiles(context.Context) ([]gen.ListImportCandidateFilesRow, error) {
	return f.cands.Files, nil
}

func (f *fakeDB) ImportWatchedMark(_ context.Context, p gen.ImportWatchedMarkParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marks = append(f.marks, p)
	return f.first("mark" + p.UserID.String() + p.MediaID.String()), nil
}

func (f *fakeDB) ImportWatchEvent(_ context.Context, p gen.ImportWatchEventParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, p)
	return f.first("event" + p.UserID.String() + p.MediaID.String() + p.OccurredAt.Time.String() + p.EventType), nil
}

func (f *fakeDB) ImportUserRating(_ context.Context, p gen.ImportUserRatingParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ratings++
	return f.first("rating" + p.UserID.String() + p.MediaItemID.String()), nil
}

func (f *fakeDB) ImportFavorite(_ context.Context, p gen.ImportFavoriteParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.favorites++
	return f.first("fav" + p.UserID.String() + p.MediaID.String()), nil
}

func (f *fakeDB) FindOrCreateImportedPlaylist(_ context.Context, p gen.FindOrCreateImportedPlaylistParams) (gen.FindOrCreateImportedPlaylistRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.playlists[*p.SourceKey]; ok {
		return gen.FindOrCreateImportedPlaylistRow{ID: id}, nil
	}
	id := uuid.New()
	f.playlists[*p.SourceKey] = id
	return gen.FindOrCreateImportedPlaylistRow{ID: id, Created: true}, nil
}

func (f *fakeDB) ImportPlaylistItem(_ context.Context, p gen.ImportPlaylistItemParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = append(f.items, p)
	return f.first("item" + p.CollectionID.String() + p.MediaItemID.String()), nil
}

// fakeSource serves a fixed snapshot.
type fakeSource struct {
	snap     *Snapshot
	fetchErr error
	block    chan struct{} // Fetch waits on it (or ctx) when set
	fetched  []string
}

func (s *fakeSource) Probe(context.Context) (ServerInfo, []Account, error) {
	return s.snap.Server, s.snap.Accounts, nil
}

func (s *fakeSource) Fetch(ctx context.Context, accounts []string, _ Options, progress func(string)) (*Snapshot, error) {
	s.fetched = accounts
	progress("reading")
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if s.fetchErr != nil {
		return nil, s.fetchErr
	}
	return s.snap, nil
}

type harness struct {
	svc    *Service
	db     *fakeDB
	src    *fakeSource
	lib    *testLibrary
	months []string
}

func newServiceHarness(t *testing.T) *harness {
	t.Helper()
	lib := newTestLibrary()
	h := &harness{lib: lib, src: &fakeSource{snap: planSnapshot()}}
	h.db = newFakeDB(Candidates{})
	// Reuse the test library's candidates through a fresh index build.
	h.db.cands = testCandidates(lib)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.svc = New(t.Context(), h.db, func(_ context.Context, month time.Time) error {
		h.months = append(h.months, month.Format("2006-01"))
		return nil
	}, func() int { return 24 }, logger).WithSources(func(Kind, string, string) Source { return h.src })
	h.svc.now = func() time.Time { return planNow }
	return h
}

// testCandidates rebuilds the candidate rows newTestLibrary indexed.
func testCandidates(l *testLibrary) Candidates {
	return Candidates{
		Items: []gen.ListImportCandidateItemsRow{
			{ID: l.alienHD, Type: "movie", TmdbID: i32(348), ImdbID: str("tt0078748"), DurationMs: i64(7000000)},
			{ID: l.alien4K, Type: "movie", TmdbID: i32(348)},
			{ID: l.heat, Type: "movie", ImdbID: str("tt0113277")},
			{ID: l.severance, Type: "show", TvdbID: i32(371980)},
		},
		Episodes: []gen.ListImportCandidateEpisodesRow{
			{ID: l.sev101, ShowID: l.severance, SeasonNumber: 1, EpisodeNumber: 1, DurationMs: i64(3000000)},
			{ID: l.sev102, ShowID: l.severance, SeasonNumber: 1, EpisodeNumber: 2},
		},
		Files: []gen.ListImportCandidateFilesRow{
			{MediaItemID: l.track, FilePath: "/media/Media/Music/Artist/Album/01 Song.flac", Type: "track", FileDurationMs: i64(200000)},
		},
	}
}

func validStart(owner uuid.UUID) StartParams {
	return StartParams{
		ConnectParams: ConnectParams{Kind: KindPlex, URL: "http://10.0.0.5:32400/", Token: "t"},
		Mapping:       []AccountMapping{{AccountID: "1", UserID: owner}},
		Options:       Options{Watched: true, Ratings: true, Favorites: true, Playlists: true, History: true},
	}
}

func (h *harness) finished(t *testing.T, id uuid.UUID) (gen.ImportJob, Stats) {
	t.Helper()
	h.svc.Wait()
	job, err := h.svc.Job(context.Background(), id)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	var st Stats
	if err := json.Unmarshal(job.Stats, &st); err != nil {
		t.Fatalf("stats %s: %v", job.Stats, err)
	}
	return job, st
}

func TestStart_Validation(t *testing.T) {
	h := newServiceHarness(t)
	owner := uuid.New()
	cases := []struct {
		name string
		edit func(*StartParams)
		want error
	}{
		{"kind", func(p *StartParams) { p.Kind = "kodi" }, ErrInvalidKind},
		{"url", func(p *StartParams) { p.URL = "ftp://x" }, ErrInvalidURL},
		{"url with credentials", func(p *StartParams) { p.URL = "http://a:b@host" }, ErrInvalidURL},
		{"url with a query", func(p *StartParams) { p.URL = "http://host/?X-Plex-Token=abc" }, ErrInvalidURL},
		{"token", func(p *StartParams) { p.Token = "" }, ErrNoToken},
		{"nothing", func(p *StartParams) { p.Options = Options{} }, ErrNothingSelected},
		{"no mapping", func(p *StartParams) { p.Mapping = nil }, ErrNoMapping},
		{"unknown account", func(p *StartParams) { p.Mapping[0].AccountID = "99" }, ErrUnknownAccount},
		{"no user", func(p *StartParams) { p.Mapping[0].UserID = uuid.Nil }, ErrUnknownAccount},
	}
	for _, c := range cases {
		p := validStart(owner)
		c.edit(&p)
		if _, err := h.svc.Start(context.Background(), p); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if len(h.db.jobs) != 0 {
		t.Errorf("%d jobs created by invalid starts", len(h.db.jobs))
	}
}

func TestStart_DryRunWritesNothing(t *testing.T) {
	h := newServiceHarness(t)
	p := validStart(uuid.New())
	p.DryRun = true
	job, err := h.svc.Start(context.Background(), p)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var mapping []AccountMapping
	if err := json.Unmarshal(job.Mapping, &mapping); err != nil || len(mapping) != 1 || mapping[0].AccountName != "Owner" {
		t.Errorf("mapping = %s (the account's name is recorded)", job.Mapping)
	}
	if job.SourceUrl != "http://10.0.0.5:32400" {
		t.Errorf("source url = %s", job.SourceUrl)
	}
	job, st := h.finished(t, job.ID)
	if job.Status != StatusSucceeded || job.Phase != "Preview ready" {
		t.Errorf("status %s / %s", job.Status, job.Phase)
	}
	if len(h.db.marks)+len(h.db.events)+h.db.ratings+h.db.favorites+len(h.db.items) != 0 || len(h.months) != 0 {
		t.Error("a dry run wrote")
	}
	if st.Planned.Watched == 0 || st.Planned.Plays == 0 || st.Written != (Counts{}) {
		t.Errorf("stats = %+v", st)
	}
	var unmatched []string
	if err := json.Unmarshal(job.Unmatched, &unmatched); err != nil || len(unmatched) != 1 {
		t.Errorf("unmatched = %s", job.Unmatched)
	}
}

func TestStart_WritesAndRerunsWriteOnlyWhatsNew(t *testing.T) {
	h := newServiceHarness(t)
	owner := uuid.New()
	job, err := h.svc.Start(context.Background(), validStart(owner))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	job, st := h.finished(t, job.ID)
	if job.Status != StatusSucceeded || job.Error != nil {
		t.Fatalf("status %s, error %v", job.Status, job.Error)
	}
	if st.Written.Watched != st.Planned.Watched || st.Written.Plays != st.Planned.Plays ||
		st.Written.Playlists != 1 || st.Written.PlaylistItems != 2 || st.WriteProgress != 1 {
		t.Errorf("stats = %+v", st)
	}
	for _, e := range h.db.events {
		if e.ClientID != "import:plex" || e.ClientName != "Plex (imported)" || e.UserID != owner {
			t.Errorf("event = %+v", e)
		}
	}
	if len(h.months) != 1 || h.months[0] != "2025-03" {
		t.Errorf("partitions ensured for %v, want each month once", h.months)
	}

	// The same import again: nothing new.
	job, err = h.svc.Start(context.Background(), validStart(owner))
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	_, st = h.finished(t, job.ID)
	if st.Written != (Counts{}) {
		t.Errorf("re-run wrote %+v, want nothing", st.Written)
	}
}

func TestStart_OneAtATime(t *testing.T) {
	h := newServiceHarness(t)
	h.src.block = make(chan struct{})
	first, err := h.svc.Start(context.Background(), validStart(uuid.New()))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := h.svc.Start(context.Background(), validStart(uuid.New())); !errors.Is(err, ErrImportRunning) {
		t.Errorf("second Start while running: %v, want ErrImportRunning", err)
	}
	close(h.src.block)
	h.finished(t, first.ID)
}

func TestCancel(t *testing.T) {
	h := newServiceHarness(t)
	h.src.block = make(chan struct{})
	job, err := h.svc.Start(context.Background(), validStart(uuid.New()))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.svc.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	job, _ = h.finished(t, job.ID)
	if job.Status != StatusCancelled {
		t.Errorf("status = %s, want cancelled", job.Status)
	}
	if err := h.svc.Cancel(context.Background(), job.ID); !errors.Is(err, ErrJobNotRunning) {
		t.Errorf("cancel of a finished import: %v", err)
	}
	if err := h.svc.Cancel(context.Background(), uuid.New()); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("cancel of an unknown import: %v", err)
	}
}

func TestStart_SourceFailure(t *testing.T) {
	h := newServiceHarness(t)
	h.src.fetchErr = &FetchError{Msg: "boom"}
	job, err := h.svc.Start(context.Background(), validStart(uuid.New()))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	job, _ = h.finished(t, job.ID)
	if job.Status != StatusFailed || job.Error == nil || *job.Error != "boom" {
		t.Errorf("job = %s %v", job.Status, job.Error)
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"http://10.0.0.5:32400/":     "http://10.0.0.5:32400",
		" https://jf.example.com/  ": "https://jf.example.com",
		"http://host/jellyfin/":      "http://host/jellyfin",
	}
	for in, want := range cases {
		if got, err := NormalizeURL(in); err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "host:32400", "javascript:alert(1)", "http://", "http://u@host", "http://host/#x"} {
		if _, err := NormalizeURL(bad); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("NormalizeURL(%q) accepted", bad)
		}
	}
}
