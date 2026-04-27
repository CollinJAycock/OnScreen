package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// ── mocks ───────────────────────────────────────────────────────────────────

type mockSubtitleService struct {
	searchResults []opensubtitles.SearchResult
	searchErr     error
	searchOpts    subtitles.SearchOpts

	downloadRow  gen.ExternalSubtitle
	downloadErr  error
	downloadOpts subtitles.DownloadOpts

	rows      map[uuid.UUID]gen.ExternalSubtitle
	deleted   []uuid.UUID
	deleteErr error

	ocrRow  gen.ExternalSubtitle
	ocrErr  error
	ocrOpts subtitles.OCROpts
}

func (m *mockSubtitleService) Search(_ context.Context, opts subtitles.SearchOpts) ([]opensubtitles.SearchResult, error) {
	m.searchOpts = opts
	return m.searchResults, m.searchErr
}
func (m *mockSubtitleService) Download(_ context.Context, opts subtitles.DownloadOpts) (gen.ExternalSubtitle, error) {
	m.downloadOpts = opts
	return m.downloadRow, m.downloadErr
}
func (m *mockSubtitleService) List(_ context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error) {
	var out []gen.ExternalSubtitle
	for _, r := range m.rows {
		if r.FileID == fileID {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *mockSubtitleService) Get(_ context.Context, id uuid.UUID) (gen.ExternalSubtitle, error) {
	if row, ok := m.rows[id]; ok {
		return row, nil
	}
	return gen.ExternalSubtitle{}, errors.New("not found")
}
func (m *mockSubtitleService) Delete(_ context.Context, id uuid.UUID) error {
	m.deleted = append(m.deleted, id)
	return m.deleteErr
}
func (m *mockSubtitleService) OCRStream(_ context.Context, opts subtitles.OCROpts) (gen.ExternalSubtitle, error) {
	m.ocrOpts = opts
	return m.ocrRow, m.ocrErr
}

// mockSubsMedia implements ItemMediaService for the subtitle handler tests.
// Unlike the items_test mock, this one keys lookups by ID so we can model
// show→season→episode hierarchies and multi-item scenes.
type mockSubsMedia struct {
	items map[uuid.UUID]*media.Item
	files map[uuid.UUID][]media.File
}

func (m *mockSubsMedia) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if it, ok := m.items[id]; ok {
		return it, nil
	}
	return nil, media.ErrNotFound
}
func (m *mockSubsMedia) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	for _, files := range m.files {
		for i := range files {
			if files[i].ID == id {
				return &files[i], nil
			}
		}
	}
	return nil, media.ErrNotFound
}
func (m *mockSubsMedia) GetFiles(_ context.Context, itemID uuid.UUID) ([]media.File, error) {
	if fs, ok := m.files[itemID]; ok {
		return fs, nil
	}
	return nil, nil
}
func (m *mockSubsMedia) ListChildren(_ context.Context, _ uuid.UUID) ([]media.Item, error) {
	return nil, nil
}
func (m *mockSubsMedia) GetPhotoMetadata(_ context.Context, _ uuid.UUID) (*media.PhotoMetadata, error) {
	return nil, media.ErrNotFound
}

// mockAccess denies access to libraries not in allow.
type mockAccess struct {
	allow map[uuid.UUID]struct{}
}

func (a *mockAccess) CanAccessLibrary(_ context.Context, _, libraryID uuid.UUID, _ bool) (bool, error) {
	if a.allow == nil {
		return true, nil
	}
	_, ok := a.allow[libraryID]
	return ok, nil
}
func (a *mockAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return a.allow, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func subReq(method, url string, body []byte, userID uuid.UUID, params map[string]string) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID}))
	if len(params) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	return req
}

// ── Search ──────────────────────────────────────────────────────────────────

func TestSubtitles_Search_ReturnsResults(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	year := 2020
	svc := &mockSubtitleService{
		searchResults: []opensubtitles.SearchResult{
			{FileID: 1, FileName: "a.srt", Language: "en", Release: "REL"},
			{FileID: 2, FileName: "b.srt", Language: "en"},
		},
	}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{
		itemID: {ID: itemID, LibraryID: libID, Title: "Movie Title", Type: "movie", Year: &year},
	}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/subtitles/search?lang=en", nil, uuid.New(), map[string]string{"id": itemID.String()})
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.searchOpts.Query != "Movie Title" {
		t.Errorf("expected Query to default to item title, got %q", svc.searchOpts.Query)
	}
	if svc.searchOpts.Year != 2020 {
		t.Errorf("expected year forwarded, got %d", svc.searchOpts.Year)
	}
	if svc.searchOpts.Languages != "en" {
		t.Errorf("expected lang=en forwarded, got %q", svc.searchOpts.Languages)
	}

	var resp struct {
		Data []SearchResultJSON `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Meta.Total != 2 || len(resp.Data) != 2 {
		t.Fatalf("expected 2 results, got total=%d len=%d", resp.Meta.Total, len(resp.Data))
	}
	if resp.Data[0].ProviderFileID != 1 || resp.Data[0].Release != "REL" {
		t.Errorf("unexpected first result: %+v", resp.Data[0])
	}
}

func TestSubtitles_Search_NoProviderReturns503(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{searchErr: subtitles.ErrNoProvider}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{
		itemID: {ID: itemID, LibraryID: libID, Title: "X", Type: "movie"},
	}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{"id": itemID.String()})
	h.Search(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestSubtitles_Search_OtherProviderErrReturns502(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{searchErr: errors.New("upstream 500")}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{
		itemID: {ID: itemID, LibraryID: libID, Title: "X", Type: "movie"},
	}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{"id": itemID.String()})
	h.Search(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestSubtitles_Search_UnknownItemReturns404(t *testing.T) {
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{"id": uuid.New().String()})
	h.Search(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSubtitles_Search_LibraryAccessDenied(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{
		itemID: {ID: itemID, LibraryID: libID, Title: "X", Type: "movie"},
	}}
	// Empty allow map = no libraries accessible.
	acc := &mockAccess{allow: map[uuid.UUID]struct{}{}}
	h := NewSubtitleHandler(svc, mm, slog.Default()).WithLibraryAccess(acc)

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{"id": itemID.String()})
	h.Search(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected access denial to return 404, got %d", rec.Code)
	}
}

func TestSubtitles_Search_EpisodeDerivesShowTitleAndNumbers(t *testing.T) {
	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()
	libID := uuid.New()
	seasonNum := 2
	episodeNum := 7

	svc := &mockSubtitleService{searchResults: []opensubtitles.SearchResult{}}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{
		showID:   {ID: showID, LibraryID: libID, Title: "Great Show", Type: "show"},
		seasonID: {ID: seasonID, LibraryID: libID, Title: "Season 2", Type: "season", ParentID: &showID, Index: &seasonNum},
		episodeID: {ID: episodeID, LibraryID: libID, Title: "S02E07 Episode", Type: "episode",
			ParentID: &seasonID, Index: &episodeNum},
	}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{"id": episodeID.String()})
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if svc.searchOpts.Query != "Great Show" {
		t.Errorf("expected show title forwarded, got %q", svc.searchOpts.Query)
	}
	if svc.searchOpts.Season != 2 || svc.searchOpts.Episode != 7 {
		t.Errorf("expected season=2 episode=7, got %d/%d", svc.searchOpts.Season, svc.searchOpts.Episode)
	}
}

// ── Download ────────────────────────────────────────────────────────────────

func TestSubtitles_Download_Success(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()
	subID := uuid.New()

	svc := &mockSubtitleService{
		downloadRow: gen.ExternalSubtitle{
			ID: subID, FileID: fileID, Language: "en", Source: "opensubtitles",
			StoragePath: "/cache/subs/whatever.vtt",
		},
	}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie", Title: "X"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{
		"file_id":          fileID.String(),
		"provider_file_id": 42,
		"language":         "en",
		"title":            "Cool.Movie.2020",
	})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.Download(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.downloadOpts.FileID != fileID || svc.downloadOpts.ProviderFileID != 42 || svc.downloadOpts.Language != "en" {
		t.Fatalf("download opts not forwarded: %+v", svc.downloadOpts)
	}
	var resp struct {
		Data ExternalSubtitleJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.URL != "/media/external-subtitles/"+subID.String() {
		t.Errorf("expected serve URL, got %q", resp.Data.URL)
	}
	if resp.Data.ID != subID.String() {
		t.Errorf("expected id=%s, got %s", subID, resp.Data.ID)
	}
}

func TestSubtitles_Download_RejectsFileFromDifferentItem(t *testing.T) {
	itemID := uuid.New()
	otherFileID := uuid.New() // not attached to itemID
	libID := uuid.New()

	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: uuid.New(), MediaItemID: itemID}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{
		"file_id":          otherFileID.String(),
		"provider_file_id": 42,
		"language":         "en",
	})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.Download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched file, got %d", rec.Code)
	}
	if svc.downloadOpts.ProviderFileID != 0 {
		t.Error("service Download should not have been called")
	}
}

func TestSubtitles_Download_MissingFieldsReturns400(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": uuid.New().String()}) // missing provider_file_id + language
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.Download(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSubtitles_Download_NoProviderReturns503(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()

	svc := &mockSubtitleService{downloadErr: subtitles.ErrNoProvider}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{
		"file_id":          fileID.String(),
		"provider_file_id": 1,
		"language":         "en",
	})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.Download(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

// ── OCR ─────────────────────────────────────────────────────────────────────
//
// v2.1 converted these endpoints from synchronous to job-queued. The
// POST returns 202 + a job descriptor immediately and tesseract runs
// in a server-lifetime goroutine; clients poll GET .../ocr/{jobId} for
// the terminal state. The conversion exists because the v2.0
// synchronous path 524'd behind reverse proxies with sub-multi-minute
// response timeouts (Cloudflare Tunnel free tier = 100 s).

// pollOCRJob waits up to 2 s for the job to reach a terminal state.
// The mock OCRStream returns synchronously, so the goroutine in the
// handler typically finishes within a few hundred microseconds —
// 2 s is a generous upper bound that absorbs CI scheduler jitter.
func pollOCRJob(t *testing.T, h *SubtitleHandler, itemID uuid.UUID, jobID string) ocrJobJSON {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{
			"id": itemID.String(), "jobId": jobID,
		})
		h.OCRStatus(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("poll: status %d body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data ocrJobJSON `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("poll decode: %v body=%s", err, rec.Body.String())
		}
		if resp.Data.Status != subtitles.OCRJobRunning {
			return resp.Data
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for OCR job %s to reach a terminal state", jobID)
	return ocrJobJSON{}
}

func TestSubtitles_OCR_AcceptsAndCompletes(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()
	subID := uuid.New()

	svc := &mockSubtitleService{
		ocrRow: gen.ExternalSubtitle{
			ID: subID, FileID: fileID, Language: "fr", Source: "ocr",
			SourceID: ptrStr("stream_2"), StoragePath: "/cache/subs/ocr.vtt",
		},
	}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie", Title: "X"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, FilePath: "/movies/x.mkv"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{
		"file_id":      fileID.String(),
		"stream_index": 2,
		"language":     "fr",
		"title":        "Forced FR",
		"forced":       true,
	})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	// POST returns 202 + a running-job descriptor; the actual OCR runs
	// in a goroutine and is observed via the poll endpoint.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var post struct {
		Data ocrJobJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &post); err != nil {
		t.Fatalf("decode POST: %v", err)
	}
	if post.Data.Status != subtitles.OCRJobRunning {
		t.Errorf("expected initial status=running, got %s", post.Data.Status)
	}
	if post.Data.JobID == "" {
		t.Fatalf("POST response missing job_id: %+v", post.Data)
	}

	// Poll until terminal — should land on completed with the row inline.
	final := pollOCRJob(t, h, itemID, post.Data.JobID)
	if final.Status != subtitles.OCRJobCompleted {
		t.Fatalf("expected completed, got status=%s err=%s", final.Status, final.Error)
	}
	if final.Subtitle == nil || final.Subtitle.Source != "ocr" || final.Subtitle.SourceID == nil || *final.Subtitle.SourceID != "stream_2" {
		t.Errorf("subtitle row not surfaced on completion: %+v", final.Subtitle)
	}

	// Opts forwarding sanity — same checks the synchronous test did.
	if svc.ocrOpts.FileID != fileID {
		t.Errorf("expected file_id forwarded, got %s", svc.ocrOpts.FileID)
	}
	if svc.ocrOpts.AbsStreamIndex != 2 {
		t.Errorf("expected stream_index=2 forwarded, got %d", svc.ocrOpts.AbsStreamIndex)
	}
	if svc.ocrOpts.InputPath != "/movies/x.mkv" {
		t.Errorf("expected input_path resolved from media file, got %q", svc.ocrOpts.InputPath)
	}
	if svc.ocrOpts.Language != "fr" || svc.ocrOpts.Title != "Forced FR" || !svc.ocrOpts.Forced {
		t.Errorf("opts not forwarded: %+v", svc.ocrOpts)
	}
}

func TestSubtitles_OCR_NotConfiguredJobFails(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()

	svc := &mockSubtitleService{ocrErr: subtitles.ErrNoOCR}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, FilePath: "/m.mkv"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": fileID.String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	// 202 even when OCR isn't configured — the job creation succeeds;
	// the failure surfaces on poll. Frontend can detect this by checking
	// the system capabilities endpoint before clicking the button.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var post struct {
		Data ocrJobJSON `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &post)

	final := pollOCRJob(t, h, itemID, post.Data.JobID)
	if final.Status != subtitles.OCRJobFailed {
		t.Fatalf("expected failed, got status=%s", final.Status)
	}
	if final.Error == "" {
		t.Errorf("expected error string surfaced when OCR not configured")
	}
}

func TestSubtitles_OCR_EngineErrorJobFails(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()

	svc := &mockSubtitleService{ocrErr: errors.New("tesseract crashed")}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, FilePath: "/m.mkv"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": fileID.String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
	var post struct {
		Data ocrJobJSON `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &post)

	final := pollOCRJob(t, h, itemID, post.Data.JobID)
	if final.Status != subtitles.OCRJobFailed {
		t.Fatalf("expected failed, got %s", final.Status)
	}
	if final.Error != "tesseract crashed" {
		t.Errorf("expected engine error surfaced, got %q", final.Error)
	}
}

func TestSubtitles_OCRStatus_UnknownJobReturns404(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
	}
	h := NewSubtitleHandler(&mockSubtitleService{}, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.New(), map[string]string{
		"id": itemID.String(), "jobId": "no-such-job",
	})
	h.OCRStatus(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown job id, got %d", rec.Code)
	}
}

func TestSubtitles_OCRStatus_LibraryAccessDenied(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()
	otherLibID := uuid.New()

	svc := &mockSubtitleService{ocrRow: gen.ExternalSubtitle{ID: uuid.New(), FileID: fileID}}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, FilePath: "/m.mkv"}}},
	}
	// Allow access only to a *different* library — POST should be 403.
	h := NewSubtitleHandler(svc, mm, slog.Default()).
		WithLibraryAccess(&mockAccess{allow: map[uuid.UUID]struct{}{otherLibID: {}}})

	body, _ := json.Marshal(map[string]any{"file_id": fileID.String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)
	// 404 (not 403) is the deliberate response — same as the rest of the
	// item endpoints — to avoid leaking existence of items the caller
	// can't see.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-allowed library, got %d", rec.Code)
	}
}

func TestSubtitles_OCR_RejectsFileFromDifferentItem(t *testing.T) {
	itemID := uuid.New()
	otherFileID := uuid.New()
	libID := uuid.New()

	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: uuid.New(), MediaItemID: itemID, FilePath: "/m.mkv"}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": otherFileID.String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if svc.ocrOpts.AbsStreamIndex != 0 || svc.ocrOpts.FileID != uuid.Nil {
		t.Error("OCRStream should not have been called for mismatched file")
	}
}

func TestSubtitles_OCR_InvalidBodyReturns400(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", []byte("{not json"), uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", rec.Code)
	}
}

func TestSubtitles_OCR_InvalidFileIDReturns400(t *testing.T) {
	itemID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": "not-a-uuid", "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad file_id, got %d", rec.Code)
	}
}

func TestSubtitles_OCR_UnknownItemReturns404(t *testing.T) {
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{items: map[uuid.UUID]*media.Item{}}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	body, _ := json.Marshal(map[string]any{"file_id": uuid.New().String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": uuid.New().String()})
	h.OCR(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSubtitles_OCR_LibraryAccessDeniedReturns404(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()
	svc := &mockSubtitleService{}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, FilePath: "/m.mkv"}}},
	}
	acc := &mockAccess{allow: map[uuid.UUID]struct{}{}}
	h := NewSubtitleHandler(svc, mm, slog.Default()).WithLibraryAccess(acc)

	body, _ := json.Marshal(map[string]any{"file_id": fileID.String(), "stream_index": 2})
	rec := httptest.NewRecorder()
	req := subReq(http.MethodPost, "/x", body, uuid.New(), map[string]string{"id": itemID.String()})
	h.OCR(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for denied library, got %d", rec.Code)
	}
}

func ptrStr(s string) *string { return &s }

// ── Delete ──────────────────────────────────────────────────────────────────

func TestSubtitles_Delete_Success(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	libID := uuid.New()
	subID := uuid.New()

	svc := &mockSubtitleService{
		rows: map[uuid.UUID]gen.ExternalSubtitle{
			subID: {ID: subID, FileID: fileID},
		},
	}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodDelete, "/x", nil, uuid.New(), map[string]string{
		"id":    itemID.String(),
		"subId": subID.String(),
	})
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(svc.deleted) != 1 || svc.deleted[0] != subID {
		t.Errorf("expected subId to be deleted, got %v", svc.deleted)
	}
}

func TestSubtitles_Delete_RejectsSubFromDifferentItem(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New() // attached to itemID
	otherFileID := uuid.New() // subtitle points at a different file
	libID := uuid.New()
	subID := uuid.New()

	svc := &mockSubtitleService{
		rows: map[uuid.UUID]gen.ExternalSubtitle{subID: {ID: subID, FileID: otherFileID}},
	}
	mm := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libID, Type: "movie"}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID}}},
	}
	h := NewSubtitleHandler(svc, mm, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodDelete, "/x", nil, uuid.New(), map[string]string{
		"id":    itemID.String(),
		"subId": subID.String(),
	})
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
	if len(svc.deleted) != 0 {
		t.Error("delete should not have been called for mismatched file")
	}
}

// ── Serve ───────────────────────────────────────────────────────────────────

func TestSubtitles_Serve_ReturnsVTTWithHeaders(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "subs.vtt")
	body := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	subID := uuid.New()
	fileID := uuid.New()
	itemID := uuid.New()
	libraryID := uuid.New()
	svc := &mockSubtitleService{
		rows: map[uuid.UUID]gen.ExternalSubtitle{subID: {ID: subID, FileID: fileID, StoragePath: path}},
	}
	mediaSvc := &mockSubsMedia{
		items: map[uuid.UUID]*media.Item{itemID: {ID: itemID, LibraryID: libraryID}},
		files: map[uuid.UUID][]media.File{itemID: {{ID: fileID, MediaItemID: itemID, Status: "active"}}},
	}
	h := NewSubtitleHandler(svc, mediaSvc, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.Nil, map[string]string{"subId": subID.String()})
	h.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/vtt; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
	if rec.Body.String() != body {
		t.Errorf("body mismatch: got %q", rec.Body.String())
	}
}

func TestSubtitles_Serve_UnknownSubReturns404(t *testing.T) {
	svc := &mockSubtitleService{rows: map[uuid.UUID]gen.ExternalSubtitle{}}
	h := NewSubtitleHandler(svc, &mockSubsMedia{}, slog.Default())

	rec := httptest.NewRecorder()
	req := subReq(http.MethodGet, "/x", nil, uuid.Nil, map[string]string{"subId": uuid.New().String()})
	h.Serve(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
