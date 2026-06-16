package subtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// fakeOCREngine returns canned cues / errors and records the args it was called with.
type fakeOCREngine struct {
	cues       []ocr.Cue
	err        error
	gotInput   string
	gotStream  int
	gotLang    string
	gotWorkDir string
}

func (f *fakeOCREngine) Run(_ context.Context, input string, stream int, lang, workDir string) ([]ocr.Cue, error) {
	f.gotInput = input
	f.gotStream = stream
	f.gotLang = lang
	f.gotWorkDir = workDir
	return f.cues, f.err
}

// fakeProvider captures the last Search/Download call and serves canned data.
type fakeProvider struct {
	configured    bool
	searchResults []opensubtitles.SearchResult
	searchErr     error
	searchCalls   int
	downloadInfo  *opensubtitles.DownloadInfo
	downloadErr   error
	fetchBody     []byte
	fetchErr      error
}

func (f *fakeProvider) Configured() bool { return f.configured }
func (f *fakeProvider) Search(_ context.Context, _ opensubtitles.SearchOpts) ([]opensubtitles.SearchResult, error) {
	f.searchCalls++
	return f.searchResults, f.searchErr
}
func (f *fakeProvider) Download(_ context.Context, _ int) (*opensubtitles.DownloadInfo, error) {
	return f.downloadInfo, f.downloadErr
}
func (f *fakeProvider) FetchFile(_ context.Context, _ string) ([]byte, error) {
	return f.fetchBody, f.fetchErr
}

// fakeStore records inserts and serves them back via List/Get.
type fakeStore struct {
	rows map[uuid.UUID]gen.ExternalSubtitle
}

func (s *fakeStore) InsertExternalSubtitle(_ context.Context, arg gen.InsertExternalSubtitleParams) (gen.ExternalSubtitle, error) {
	if s.rows == nil {
		s.rows = map[uuid.UUID]gen.ExternalSubtitle{}
	}
	row := gen.ExternalSubtitle{
		ID:            uuid.New(),
		FileID:        arg.FileID,
		Language:      arg.Language,
		Title:         arg.Title,
		Forced:        arg.Forced,
		Sdh:           arg.Sdh,
		Source:        arg.Source,
		SourceID:      arg.SourceID,
		StoragePath:   arg.StoragePath,
		Rating:        arg.Rating,
		DownloadCount: arg.DownloadCount,
	}
	s.rows[row.ID] = row
	return row, nil
}
func (s *fakeStore) ListExternalSubtitlesForFile(_ context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error) {
	var out []gen.ExternalSubtitle
	for _, r := range s.rows {
		if r.FileID == fileID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *fakeStore) GetExternalSubtitle(_ context.Context, id uuid.UUID) (gen.ExternalSubtitle, error) {
	row, ok := s.rows[id]
	if !ok {
		return gen.ExternalSubtitle{}, errors.New("not found")
	}
	return row, nil
}
func (s *fakeStore) DeleteExternalSubtitle(_ context.Context, id uuid.UUID) error {
	delete(s.rows, id)
	return nil
}

func TestSRTtoVTTConvertsCueTiming(t *testing.T) {
	srt := "1\n00:00:01,500 --> 00:00:03,000\nHello world\n"
	got := string(normalizeToVTT([]byte(srt), "subs.srt"))
	if !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("expected WEBVTT header, got %q", got)
	}
	if !strings.Contains(got, "00:00:01.500 --> 00:00:03.000") {
		t.Fatalf("expected period-separated cue timing, got %q", got)
	}
	if strings.Contains(got, ",500") {
		t.Fatalf("SRT comma should be replaced in cue line, got %q", got)
	}
}

func TestNormalizeToVTTPassthroughWebVTT(t *testing.T) {
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHi\n"
	got := string(normalizeToVTT([]byte(vtt), "subs.vtt"))
	if got != vtt {
		t.Fatalf("WEBVTT input should pass through unchanged")
	}
}

func TestNormalizeToVTTStripsBOM(t *testing.T) {
	input := "\ufeffWEBVTT\n\n"
	got := string(normalizeToVTT([]byte(input), "subs.vtt"))
	if strings.HasPrefix(got, "\ufeff") {
		t.Fatalf("BOM should be stripped")
	}
}

func TestNormalizeToVTTStripsActiveContent(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		absent []string // must be gone after sanitization
		want   []string // must survive
	}{
		{
			name:   "script block removed",
			in:     "WEBVTT\n\n00:00.000 --> 00:01.000\nHi<script>alert(1)</script>\n",
			absent: []string{"<script", "</script", "alert(1)"},
			want:   []string{"Hi"},
		},
		{
			name:   "event handler tag removed",
			in:     "WEBVTT\n\n00:00.000 --> 00:01.000\n<img src=x onerror=alert(1)>caption\n",
			absent: []string{"<img", "onerror"},
			want:   []string{"caption"},
		},
		{
			name:   "javascript uri tag removed",
			in:     "WEBVTT\n\n00:00.000 --> 00:01.000\n<a href=\"javascript:alert(1)\">x\n",
			absent: []string{"javascript:", "href"},
			want:   []string{"x"},
		},
		{
			name: "legit formatting preserved",
			in:   "WEBVTT\n\n00:00.000 --> 00:01.000\n<i>italic</i> <b>bold</b> <font color=\"red\">red</font>\n",
			want: []string{"<i>italic</i>", "<b>bold</b>", "<font color=\"red\">red</font>"},
		},
		{
			name: "bare angle-bracket text preserved",
			in:   "WEBVTT\n\n00:00.000 --> 00:01.000\n<MUSIC> 5 < 10 and 10 > 5\n",
			want: []string{"<MUSIC>", "5 < 10 and 10 > 5"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(normalizeToVTT([]byte(tc.in), "subs.vtt"))
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("expected %q to be stripped, got:\n%s", a, got)
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("expected %q to survive, got:\n%s", w, got)
				}
			}
		})
	}
}

func TestDownloadReturnsErrNoProviderWhenUnconfigured(t *testing.T) {
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := svc.Download(context.Background(), DownloadOpts{
		FileID:         uuid.New(),
		ProviderFileID: 42,
		Language:       "en",
	})
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got %v", err)
	}
}

func TestDownloadValidatesRequiredFields(t *testing.T) {
	p := &fakeProvider{configured: true}
	svc := New(p, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := svc.Download(context.Background(), DownloadOpts{ProviderFileID: 1, Language: "en"})
	if err == nil {
		t.Fatal("expected error for missing FileID")
	}
}

func TestDownloadWritesFileAndInsertsRow(t *testing.T) {
	tmp := t.TempDir()
	p := &fakeProvider{
		configured:   true,
		downloadInfo: &opensubtitles.DownloadInfo{Link: "http://x/sub.srt", FileName: "sub.srt", Remaining: 5},
		fetchBody:    []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"),
	}
	store := &fakeStore{}
	svc := New(p, store, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))

	fileID := uuid.New()
	row, err := svc.Download(context.Background(), DownloadOpts{
		FileID:         fileID,
		ProviderFileID: 42,
		Language:       "en",
		Title:          "Release Name",
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if row.FileID != fileID || row.Language != "en" {
		t.Fatalf("unexpected row: %+v", row)
	}
	path := filepath.Join(tmp, fileID.String(), "en_42.vtt")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if !strings.HasPrefix(string(body), "WEBVTT") {
		t.Fatalf("written subtitle should be WEBVTT, got %q", string(body))
	}
	if !strings.Contains(string(body), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("written subtitle should contain converted cue timing")
	}
}

func TestDownloadRollsBackFileOnInsertFailure(t *testing.T) {
	tmp := t.TempDir()
	p := &fakeProvider{
		configured:   true,
		downloadInfo: &opensubtitles.DownloadInfo{Link: "http://x/sub.srt", FileName: "sub.srt"},
		fetchBody:    []byte("WEBVTT\n\n"),
	}
	store := &failingInsertStore{}
	svc := New(p, store, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))

	fileID := uuid.New()
	_, err := svc.Download(context.Background(), DownloadOpts{
		FileID:         fileID,
		ProviderFileID: 7,
		Language:       "en",
	})
	if err == nil {
		t.Fatal("expected insert failure to bubble up")
	}
	// File should have been removed after the DB insert failed.
	if _, statErr := os.Stat(filepath.Join(tmp, fileID.String(), "en_7.vtt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected file to be removed after insert failure, stat err: %v", statErr)
	}
}

type failingInsertStore struct{ fakeStore }

func (f *failingInsertStore) InsertExternalSubtitle(_ context.Context, _ gen.InsertExternalSubtitleParams) (gen.ExternalSubtitle, error) {
	return gen.ExternalSubtitle{}, errors.New("boom")
}

// ── OCRStream ──────────────────────────────────────────────────────────────

func TestOCRStream_NoEngineReturnsErrNoOCR(t *testing.T) {
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:    uuid.New(),
		InputPath: "/x.mkv",
	})
	if !errors.Is(err, ErrNoOCR) {
		t.Fatalf("expected ErrNoOCR, got %v", err)
	}
}

func TestOCRStream_ValidatesRequiredFields(t *testing.T) {
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(&fakeOCREngine{})

	if _, err := svc.OCRStream(context.Background(), OCROpts{InputPath: "/x.mkv"}); err == nil {
		t.Error("expected error for nil FileID")
	}
	if _, err := svc.OCRStream(context.Background(), OCROpts{FileID: uuid.New()}); err == nil {
		t.Error("expected error for empty InputPath")
	}
}

func TestOCRStream_HappyPathWritesVTTAndInsertsRow(t *testing.T) {
	tmp := t.TempDir()
	store := &fakeStore{}
	engine := &fakeOCREngine{
		cues: []ocr.Cue{
			{StartMS: 0, EndMS: 1500, Text: "First"},
			{StartMS: 2000, EndMS: 3500, Text: "Second"},
		},
	}
	svc := New(nil, store, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(engine)

	fileID := uuid.New()
	row, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:         fileID,
		InputPath:      "/movies/x.mkv",
		AbsStreamIndex: 3,
		Language:       "fr",
		Title:          "Forced FR",
		Forced:         true,
	})
	if err != nil {
		t.Fatalf("OCRStream: %v", err)
	}

	// Engine got the right args (workdir is per-stream so its name encodes the index).
	if engine.gotInput != "/movies/x.mkv" || engine.gotStream != 3 || engine.gotLang != "fr" {
		t.Errorf("engine args wrong: input=%q stream=%d lang=%q", engine.gotInput, engine.gotStream, engine.gotLang)
	}
	if !strings.HasSuffix(engine.gotWorkDir, "ocr_work_stream3") {
		t.Errorf("workdir should encode stream index, got %q", engine.gotWorkDir)
	}

	// Row got source="ocr", source_id="stream_3", and the title was preserved.
	if row.Source != "ocr" || row.SourceID == nil || *row.SourceID != "stream_3" {
		t.Errorf("source metadata wrong: %+v", row)
	}
	if row.Title == nil || *row.Title != "Forced FR" {
		t.Errorf("expected title preserved, got %v", row.Title)
	}
	if !row.Forced {
		t.Errorf("expected forced=true to round-trip")
	}
	if row.Language != "fr" {
		t.Errorf("expected lang fr, got %q", row.Language)
	}

	// VTT was written with the expected name and contains the cues.
	wantPath := filepath.Join(tmp, fileID.String(), "ocr_stream3_fr.vtt")
	if row.StoragePath != wantPath {
		t.Errorf("storage path: got %q, want %q", row.StoragePath, wantPath)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read vtt: %v", err)
	}
	if !strings.HasPrefix(string(body), "WEBVTT") {
		t.Errorf("expected WEBVTT header, got %q", string(body)[:min(20, len(body))])
	}
	if !strings.Contains(string(body), "First") || !strings.Contains(string(body), "Second") {
		t.Errorf("expected cues serialized, got %q", string(body))
	}

	// Per-stream workdir is removed after the run (deferred RemoveAll).
	if _, err := os.Stat(engine.gotWorkDir); !os.IsNotExist(err) {
		t.Errorf("workdir should have been removed, stat err: %v", err)
	}
}

func TestOCRStream_LanguageDefaultsToEn(t *testing.T) {
	tmp := t.TempDir()
	store := &fakeStore{}
	engine := &fakeOCREngine{cues: []ocr.Cue{{StartMS: 0, EndMS: 1000, Text: "x"}}}
	svc := New(nil, store, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(engine)

	row, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:         uuid.New(),
		InputPath:      "/m.mkv",
		AbsStreamIndex: 0,
	})
	if err != nil {
		t.Fatalf("OCRStream: %v", err)
	}
	if row.Language != "en" {
		t.Errorf("expected language to default to en, got %q", row.Language)
	}
	if engine.gotLang != "en" {
		t.Errorf("engine should also receive en, got %q", engine.gotLang)
	}
}

func TestOCRStream_NoCuesReturnsError(t *testing.T) {
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(&fakeOCREngine{cues: nil})

	_, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:    uuid.New(),
		InputPath: "/m.mkv",
	})
	if err == nil || !strings.Contains(err.Error(), "no cues") {
		t.Fatalf("expected 'no cues' error, got %v", err)
	}
}

func TestOCRStream_EngineErrorPropagates(t *testing.T) {
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(&fakeOCREngine{err: errors.New("ffmpeg explode")})

	_, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:    uuid.New(),
		InputPath: "/m.mkv",
	})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg explode") {
		t.Fatalf("expected engine err to bubble up, got %v", err)
	}
}

func TestOCRStream_RollsBackFileOnInsertFailure(t *testing.T) {
	tmp := t.TempDir()
	engine := &fakeOCREngine{cues: []ocr.Cue{{StartMS: 0, EndMS: 1000, Text: "x"}}}
	store := &failingInsertStore{}
	svc := New(nil, store, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(engine)

	fileID := uuid.New()
	_, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:         fileID,
		InputPath:      "/m.mkv",
		AbsStreamIndex: 7,
		Language:       "en",
	})
	if err == nil {
		t.Fatal("expected insert failure to bubble up")
	}
	path := filepath.Join(tmp, fileID.String(), "ocr_stream7_en.vtt")
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("expected vtt to be removed after insert failure, stat err: %v", statErr)
	}
}

func TestSearchCachesIdenticalQueries(t *testing.T) {
	p := &fakeProvider{
		configured:    true,
		searchResults: []opensubtitles.SearchResult{{FileID: 1, Language: "en"}},
	}
	svc := New(p, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	opts := SearchOpts{Query: "The Matrix", Year: 1999, Languages: "en"}
	for i := 0; i < 3; i++ {
		got, err := svc.Search(context.Background(), opts)
		if err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
		if len(got) != 1 {
			t.Fatalf("search %d: got %d results, want 1", i, len(got))
		}
	}
	if p.searchCalls != 1 {
		t.Fatalf("provider hit %d times, want 1 (identical queries memoized)", p.searchCalls)
	}

	// A different query is a distinct cache key → a fresh provider call.
	if _, err := svc.Search(context.Background(), SearchOpts{Query: "Inception", Languages: "en"}); err != nil {
		t.Fatalf("distinct search: %v", err)
	}
	if p.searchCalls != 2 {
		t.Fatalf("distinct query should miss cache: provider hit %d, want 2", p.searchCalls)
	}
}

func TestSearchNegativeResultIsCached(t *testing.T) {
	p := &fakeProvider{configured: true, searchResults: nil} // no matches
	svc := New(p, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	opts := SearchOpts{Query: "Obscure Film Nobody Has", Languages: "en"}
	for i := 0; i < 3; i++ {
		if _, err := svc.Search(context.Background(), opts); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	if p.searchCalls != 1 {
		t.Fatalf("empty result not cached: provider hit %d, want 1", p.searchCalls)
	}
}

func TestSearchErrorIsNotCached(t *testing.T) {
	p := &fakeProvider{configured: true, searchErr: errors.New("circuit open")}
	svc := New(p, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	opts := SearchOpts{Query: "x", Languages: "en"}
	for i := 0; i < 2; i++ {
		if _, err := svc.Search(context.Background(), opts); err == nil {
			t.Fatal("expected error")
		}
	}
	if p.searchCalls != 2 {
		t.Fatalf("errors must not be cached: provider hit %d, want 2", p.searchCalls)
	}
}

func TestDownloadGatesOnExhaustedQuota(t *testing.T) {
	tmp := t.TempDir()
	p := &fakeProvider{
		configured:   true,
		downloadInfo: &opensubtitles.DownloadInfo{Link: "http://x/s.srt", FileName: "s.srt", Remaining: 0},
		fetchBody:    []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"),
	}
	svc := New(p, &fakeStore{}, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := DownloadOpts{FileID: uuid.New(), ProviderFileID: 1, Language: "en"}

	// First download succeeds but reports 0 remaining → records exhaustion.
	if _, err := svc.Download(context.Background(), base); err != nil {
		t.Fatalf("first download: %v", err)
	}
	// Second download is refused pre-flight (no provider call) with the sentinel.
	if _, err := svc.Download(context.Background(), base); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("second download: got %v, want ErrQuotaExhausted", err)
	}
}

func TestDownloadAllowedWhenQuotaRemains(t *testing.T) {
	tmp := t.TempDir()
	p := &fakeProvider{
		configured:   true,
		downloadInfo: &opensubtitles.DownloadInfo{Link: "http://x/s.srt", FileName: "s.srt", Remaining: 3},
		fetchBody:    []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"),
	}
	svc := New(p, &fakeStore{}, tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := DownloadOpts{FileID: uuid.New(), ProviderFileID: 1, Language: "en"}
	for i := 0; i < 2; i++ {
		if _, err := svc.Download(context.Background(), base); err != nil {
			t.Fatalf("download %d with remaining quota: %v", i, err)
		}
	}
}

func TestOCRStreamRejectsTraversalLanguage(t *testing.T) {
	eng := &fakeOCREngine{cues: []ocr.Cue{{StartMS: 0, EndMS: 1000, Text: "hi"}}}
	svc := New(nil, &fakeStore{}, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetOCR(eng)
	_, err := svc.OCRStream(context.Background(), OCROpts{
		FileID:    uuid.New(),
		InputPath: "/some/movie.mkv",
		Language:  "../../../../tmp/evil",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid language") {
		t.Fatalf("expected invalid-language rejection, got %v", err)
	}
}
