package subtitles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// autoFakeDB is an in-memory AutoQuerier. Candidates are returned as given
// (the SQL's own filtering is covered by the integration test); attempts are
// recorded so a run's bookkeeping can be checked.
type autoFakeDB struct {
	overrides  []gen.LibrarySubtitleAutoDownload
	candidates []gen.ListSubtitleAutoCandidatesRow
	attempts   map[string]gen.SubtitleAutoAttempt
	external   map[uuid.UUID][]string
	candArgs   []gen.ListSubtitleAutoCandidatesParams
}

func (d *autoFakeDB) ListLibrarySubtitleAutoDownload(context.Context) ([]gen.LibrarySubtitleAutoDownload, error) {
	return d.overrides, nil
}

func (d *autoFakeDB) ListSubtitleAutoCandidates(_ context.Context, arg gen.ListSubtitleAutoCandidatesParams) ([]gen.ListSubtitleAutoCandidatesRow, error) {
	d.candArgs = append(d.candArgs, arg)
	var out []gen.ListSubtitleAutoCandidatesRow
	for _, c := range d.candidates {
		for _, lib := range arg.LibraryIds {
			if c.LibraryID == lib {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (d *autoFakeDB) ListSubtitleAutoAttempts(_ context.Context, ids []uuid.UUID) ([]gen.SubtitleAutoAttempt, error) {
	var out []gen.SubtitleAutoAttempt
	for _, a := range d.attempts {
		for _, id := range ids {
			if a.MediaID == id {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

func (d *autoFakeDB) UpsertSubtitleAutoAttempt(_ context.Context, arg gen.UpsertSubtitleAutoAttemptParams) error {
	if d.attempts == nil {
		d.attempts = map[string]gen.SubtitleAutoAttempt{}
	}
	d.attempts[arg.MediaID.String()+"|"+arg.Language] = gen.SubtitleAutoAttempt{
		MediaID: arg.MediaID, Language: arg.Language, Attempts: arg.Attempts, Status: arg.Status,
		NextAttemptAt: arg.NextAttemptAt, LastResult: arg.LastResult,
	}
	return nil
}

func (d *autoFakeDB) ListExternalSubtitleLanguagesForFile(_ context.Context, fileID uuid.UUID) ([]string, error) {
	return d.external[fileID], nil
}

func (d *autoFakeDB) attempt(id uuid.UUID, lang string) (gen.SubtitleAutoAttempt, bool) {
	a, ok := d.attempts[id.String()+"|"+lang]
	return a, ok
}

// autoFakeFinder scripts provider answers per language.
type autoFakeFinder struct {
	configured  bool
	results     map[string][]opensubtitles.SearchResult // by language
	searchErr   error
	downloadErr error
	searches    []SearchOpts
	downloads   []DownloadOpts
}

func (f *autoFakeFinder) Configured() bool { return f.configured }

func (f *autoFakeFinder) Search(_ context.Context, opts SearchOpts) ([]opensubtitles.SearchResult, error) {
	f.searches = append(f.searches, opts)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.results[opts.Languages], nil
}

func (f *autoFakeFinder) Download(_ context.Context, opts DownloadOpts) (gen.ExternalSubtitle, error) {
	f.downloads = append(f.downloads, opts)
	if f.downloadErr != nil {
		return gen.ExternalSubtitle{}, f.downloadErr
	}
	return gen.ExternalSubtitle{ID: uuid.New(), FileID: opts.FileID, Language: opts.Language}, nil
}

type autoHarness struct {
	db     *autoFakeDB
	finder *autoFakeFinder
	a      *AutoDownloader
	cfg    AutoConfig
	libs   []uuid.UUID
	now    time.Time
}

func newAutoHarness(t *testing.T) *autoHarness {
	t.Helper()
	h := &autoHarness{
		db:     &autoFakeDB{external: map[uuid.UUID][]string{}},
		finder: &autoFakeFinder{configured: true, results: map[string][]opensubtitles.SearchResult{}},
		cfg:    AutoConfig{Enabled: true, Languages: []string{"en"}, PreferHashMatch: true, HearingImpaired: HIAny},
		libs:   []uuid.UUID{uuid.New()},
		now:    time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
	}
	h.a = &AutoDownloader{
		q:           h.db,
		finder:      h.finder,
		config:      func(context.Context) AutoConfig { return h.cfg },
		libraries:   func(context.Context) ([]uuid.UUID, error) { return h.libs, nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:         func() time.Time { return h.now },
		hash:        func(string) (string, error) { return "0123456789abcdef", nil },
		sidecars:    func(string) []string { return nil },
		maxSearches: autoMaxSearches,
	}
	return h
}

func (h *autoHarness) movie(title string, streams string) gen.ListSubtitleAutoCandidatesRow {
	year := int32(2019)
	c := gen.ListSubtitleAutoCandidatesRow{
		ID: uuid.New(), LibraryID: h.libs[0], Type: "movie", Title: title, Year: &year,
		FileID: uuid.New(), FilePath: "/media/" + title + "/" + title + ".2019.1080p.BluRay.x264-GRP.mkv",
	}
	if streams != "" {
		c.SubtitleStreams = []byte(streams)
	}
	h.db.candidates = append(h.db.candidates, c)
	return c
}

func TestAuto_OffOrUnconfiguredDoesNothing(t *testing.T) {
	h := newAutoHarness(t)
	h.movie("Film", "")
	h.cfg.Enabled = false
	if out, err := h.a.Run(context.Background()); err != nil || !strings.Contains(out, "off") {
		t.Fatalf("disabled: %q %v", out, err)
	}
	h.cfg.Enabled = true
	h.finder.configured = false
	if out, err := h.a.Run(context.Background()); err != nil || !strings.Contains(out, "not set up") {
		t.Fatalf("unconfigured: %q %v", out, err)
	}
	if len(h.finder.searches) != 0 {
		t.Fatal("searched while off")
	}
}

func TestAuto_DownloadsBestAndRecords(t *testing.T) {
	h := newAutoHarness(t)
	c := h.movie("Film", "")
	h.finder.results["en"] = []opensubtitles.SearchResult{
		{FileID: 1, Language: "en", Release: "Film.2019.720p.WEB-DL", DownloadCount: 90000},
		{FileID: 2, Language: "en", Release: "Film.2019.1080p.BluRay.x264-GRP", DownloadCount: 10},
		{FileID: 3, Language: "fr", Release: "Film.2019.1080p.BluRay.x264-GRP", MovieHashMatch: true},
	}
	out, err := h.a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(h.finder.downloads) != 1 || h.finder.downloads[0].ProviderFileID != 2 || h.finder.downloads[0].FileID != c.FileID {
		t.Fatalf("downloaded %+v (summary %q)", h.finder.downloads, out)
	}
	s := h.finder.searches[0]
	if s.MovieHash != "0123456789abcdef" || s.Query != "Film" || s.Year != 2019 || s.Languages != "en" {
		t.Fatalf("search opts %+v", s)
	}
	if a, ok := h.db.attempt(c.ID, "en"); !ok || a.Status != "downloaded" || a.Attempts != 1 {
		t.Fatalf("attempt %+v", a)
	}
	if !strings.Contains(out, "1 downloaded") {
		t.Fatalf("summary %q", out)
	}
}

func TestAuto_SkipsLanguagesAlreadyPresent(t *testing.T) {
	h := newAutoHarness(t)
	h.cfg.Languages = []string{"en", "es", "fr", "de"}
	// Embedded English (ffprobe's 639-2), a Spanish sidecar, a French download.
	c := h.movie("Film", `[{"index":2,"language":"eng"}]`)
	h.a.sidecars = func(string) []string { return []string{"es"} }
	h.db.external[c.FileID] = []string{"fr"}
	h.finder.results["de"] = []opensubtitles.SearchResult{{FileID: 9, Language: "de", Release: "x"}}
	if _, err := h.a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.finder.searches) != 1 || h.finder.searches[0].Languages != "de" {
		t.Fatalf("searched %+v", h.finder.searches)
	}
	for _, l := range []string{"en", "es", "fr"} {
		a, ok := h.db.attempt(c.ID, l)
		if !ok || a.Status != "present" || !a.NextAttemptAt.Valid || !a.NextAttemptAt.Time.Equal(h.now.Add(autoPresentRecheck)) {
			t.Fatalf("%s attempt %+v", l, a)
		}
	}
}

// A miss is retried after 1, 7 and 30 days, then given up; nothing is
// searched before its retry is due.
func TestAuto_BackoffThenGiveUp(t *testing.T) {
	h := newAutoHarness(t)
	c := h.movie("Obscure", "")
	waits := []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
	for i, wait := range waits {
		if _, err := h.a.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		a, _ := h.db.attempt(c.ID, "en")
		if a.Status != "retry" || int(a.Attempts) != i+1 || !a.NextAttemptAt.Time.Equal(h.now.Add(wait)) {
			t.Fatalf("after miss %d: %+v", i+1, a)
		}
		// Not due yet: no search.
		before := len(h.finder.searches)
		h.now = h.now.Add(wait - time.Minute)
		if _, err := h.a.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(h.finder.searches) != before {
			t.Fatalf("searched before the retry was due (miss %d)", i+1)
		}
		h.now = h.now.Add(2 * time.Minute)
	}
	if _, err := h.a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _ := h.db.attempt(c.ID, "en")
	if a.Status != "gave_up" || a.Attempts != 4 || a.NextAttemptAt.Valid {
		t.Fatalf("after the 4th miss: %+v", a)
	}
	n := len(h.finder.searches)
	h.now = h.now.Add(365 * 24 * time.Hour)
	_, _ = h.a.Run(context.Background())
	if len(h.finder.searches) != n {
		t.Fatal("searched again after giving up")
	}
}

// A quota answer stops the run without costing the item an attempt, and the
// task searches nothing more for a day.
func TestAuto_QuotaStopsForTheDay(t *testing.T) {
	for name, set := range map[string]func(f *autoFakeFinder){
		"download quota": func(f *autoFakeFinder) { f.downloadErr = ErrQuotaExhausted },
		"search 429": func(f *autoFakeFinder) {
			f.searchErr = fmt.Errorf("opensubtitles search: %w", opensubtitles.ErrRateLimited)
		},
		"breaker open": func(f *autoFakeFinder) { f.searchErr = opensubtitles.ErrCircuitOpen },
		"download 406": func(f *autoFakeFinder) {
			f.downloadErr = fmt.Errorf("request download: %w", opensubtitles.ErrRateLimited)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAutoHarness(t)
			first := h.movie("A", "")
			h.movie("B", "")
			h.finder.results["en"] = []opensubtitles.SearchResult{{FileID: 1, Language: "en", Release: "A"}}
			set(h.finder)
			out, err := h.a.Run(context.Background())
			if err != nil || !strings.Contains(out, "daily limit") {
				t.Fatalf("run: %q %v", out, err)
			}
			if len(h.finder.searches) != 1 {
				t.Fatalf("kept searching after the quota answer: %d", len(h.finder.searches))
			}
			if _, ok := h.db.attempt(first.ID, "en"); ok {
				t.Fatal("quota answer cost the item an attempt")
			}
			h.finder.searchErr, h.finder.downloadErr = nil, nil
			h.now = h.now.Add(23 * time.Hour)
			if out, _ := h.a.Run(context.Background()); !strings.Contains(out, "paused") || len(h.finder.searches) != 1 {
				t.Fatalf("not paused: %q", out)
			}
			h.now = h.now.Add(2 * time.Hour)
			if _, err := h.a.Run(context.Background()); err != nil || len(h.finder.searches) == 1 {
				t.Fatalf("didn't resume after a day: %v", err)
			}
		})
	}
}

func TestAuto_ProviderErrorsEndRunWithoutAttempts(t *testing.T) {
	h := newAutoHarness(t)
	for i := 0; i < 5; i++ {
		h.movie(fmt.Sprintf("M%d", i), "")
	}
	h.finder.searchErr = errors.New("connection reset")
	out, err := h.a.Run(context.Background())
	if err != nil || len(h.finder.searches) != autoMaxFailures || !strings.Contains(out, "failing") {
		t.Fatalf("run: %q %v, %d searches", out, err, len(h.finder.searches))
	}
	if len(h.db.attempts) != 0 {
		t.Fatal("a provider outage was recorded against items")
	}
}

func TestAuto_SearchLimitPerRun(t *testing.T) {
	h := newAutoHarness(t)
	h.a.maxSearches = 3
	for i := 0; i < 10; i++ {
		h.movie(fmt.Sprintf("M%d", i), "")
	}
	out, _ := h.a.Run(context.Background())
	if len(h.finder.searches) != 3 || !strings.Contains(out, "limit") {
		t.Fatalf("%d searches, %q", len(h.finder.searches), out)
	}
}

func TestAuto_LibraryOverrides(t *testing.T) {
	h := newAutoHarness(t)
	off, spanish, follows := uuid.New(), uuid.New(), uuid.New()
	h.libs = []uuid.UUID{off, spanish, follows}
	f, es := false, "es, pt-BR"
	tr := true
	h.db.overrides = []gen.LibrarySubtitleAutoDownload{
		{LibraryID: off, Enabled: &f},
		{LibraryID: spanish, Enabled: &tr, Languages: &es},
	}
	groups, err := h.a.libraryGroups(context.Background(), h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || len(groups["en"]) != 1 || groups["en"][0] != follows ||
		len(groups["es,pt-br"]) != 1 || groups["es,pt-br"][0] != spanish {
		t.Fatalf("groups %v", groups)
	}
	// Server-wide off, but a library opted in.
	h.cfg.Enabled = false
	groups, _ = h.a.libraryGroups(context.Background(), h.cfg)
	if len(groups) != 1 || groups["es,pt-br"][0] != spanish {
		t.Fatalf("opt-in library: %v", groups)
	}
}

func TestAuto_EpisodeSearchUsesShow(t *testing.T) {
	h := newAutoHarness(t)
	s, e := int32(2), int32(5)
	imdb := "tt0903747"
	h.db.candidates = append(h.db.candidates, gen.ListSubtitleAutoCandidatesRow{
		ID: uuid.New(), LibraryID: h.libs[0], Type: "episode", Title: "Ep Title",
		SeasonNumber: &s, EpisodeNumber: &e, ShowTitle: "The Show", ShowImdbID: &imdb,
		FileID: uuid.New(), FilePath: "/tv/The Show/S02E05.mkv",
	})
	_, _ = h.a.Run(context.Background())
	got := h.finder.searches[0]
	if got.Query != "The Show" || got.Season != 2 || got.Episode != 5 || got.ParentIMDBID != imdb {
		t.Fatalf("episode search %+v", got)
	}
}

func TestPickAutoSubtitle(t *testing.T) {
	file := "Film.2019.1080p.BluRay.x264-GRP.mkv"
	base := AutoConfig{PreferHashMatch: true, HearingImpaired: HIAny}
	res := []opensubtitles.SearchResult{
		{FileID: 1, Language: "en", Release: "Film.2019.1080p.BluRay.x264-GRP", DownloadCount: 5},
		{FileID: 2, Language: "en", Release: "Film 2019 720p HDTV", MovieHashMatch: true},
		{FileID: 3, Language: "en", Release: "Film.2019.1080p.BluRay.x264-GRP", HearingImpaired: true, DownloadCount: 5},
		{FileID: 4, Language: "de", Release: "Film.2019.1080p.BluRay.x264-GRP", MovieHashMatch: true},
	}
	if r, ok := pickAutoSubtitle(res, "en", file, base); !ok || r.FileID != 2 {
		t.Fatalf("hash match not preferred: %+v", r)
	}
	noHash := base
	noHash.PreferHashMatch = false
	if r, _ := pickAutoSubtitle(res, "en", file, noHash); r.FileID != 1 && r.FileID != 3 {
		t.Fatalf("release name not used: %+v", r)
	}
	avoid := noHash
	avoid.HearingImpaired = HIAvoid
	if r, _ := pickAutoSubtitle(res, "en", file, avoid); r.FileID != 1 {
		t.Fatalf("HI not avoided: %+v", r)
	}
	prefer := noHash
	prefer.HearingImpaired = HIPrefer
	if r, _ := pickAutoSubtitle(res, "en", file, prefer); r.FileID != 3 {
		t.Fatalf("HI not preferred: %+v", r)
	}
	if _, ok := pickAutoSubtitle(res, "fr", file, base); ok {
		t.Fatal("picked a result in the wrong language")
	}
	if r, ok := pickAutoSubtitle([]opensubtitles.SearchResult{{FileID: 7, Language: "pt-BR"}}, "pt-br", file, base); !ok || r.FileID != 7 {
		t.Fatal("region tag not matched case-insensitively")
	}
}

func TestNormalizeAndParseLanguages(t *testing.T) {
	cases := map[string]string{"eng": "en", "EN": "en", "fre": "fr", "pt-BR": "pt-br", "pt_BR": "pt-br", "und": "", "": "", "chi": "zh"}
	for in, want := range cases {
		if got := NormalizeLang(in); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
	got := ParseLanguageList(" en, ES ,eng,../x, pt-BR,,")
	if strings.Join(got, ",") != "en,es,pt-br" {
		t.Fatalf("ParseLanguageList: %v", got)
	}
	if !langMatches("pt-br", "pt") || !langMatches("pt", "pt-br") || langMatches("pt-pt", "pt-br") || langMatches("en", "es") {
		t.Fatal("langMatches")
	}
}

func TestSidecarLanguages(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Movie (2019).mkv")
	for _, name := range []string{
		"Movie (2019).mkv", "Movie (2019).en.srt", "Movie (2019).eng.forced.ass",
		"Movie (2019).pt-BR.sdh.srt", "Movie (2019).srt", "Movie (2019) 2.es.srt", "Other.fr.srt", "Movie (2019).nfo",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := SidecarLanguages(video)
	want := map[string]bool{"en": true, "pt-br": true, "": true}
	if len(got) != 4 {
		t.Fatalf("sidecars %v", got)
	}
	for _, l := range got {
		if !want[l] {
			t.Fatalf("unexpected sidecar language %q in %v", l, got)
		}
	}
	if SidecarLanguages(filepath.Join(dir, "missing", "x.mkv")) != nil {
		t.Fatal("unreadable dir should report nothing")
	}
}

func TestMovieHash(t *testing.T) {
	// 128 KiB + 8 bytes of a known pattern: the hash is size + the sum of
	// the first and last 64 KiB as little-endian uint64s.
	size := 2*movieHashChunk + 8
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	got, err := movieHashReader(bytes.NewReader(data), int64(size))
	if err != nil {
		t.Fatal(err)
	}
	sum := uint64(size)
	for _, off := range []int{0, size - movieHashChunk} {
		for i := 0; i < movieHashChunk; i += 8 {
			var v uint64
			for b := 7; b >= 0; b-- {
				v = v<<8 | uint64(data[off+i+b])
			}
			sum += v
		}
	}
	if want := fmt.Sprintf("%016x", sum); got != want {
		t.Fatalf("hash %s, want %s", got, want)
	}
	if _, err := movieHashReader(bytes.NewReader(data[:100]), 100); err == nil {
		t.Fatal("tiny file hashed")
	}

	path := filepath.Join(t.TempDir(), "v.mkv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if h, err := MovieHash(path); err != nil || h != got {
		t.Fatalf("MovieHash(file) = %s, %v", h, err)
	}
}
