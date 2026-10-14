package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/downloads"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// fakeOptimized serves fixed versions to the playback paths.
type fakeOptimized struct {
	versions []downloads.OptimizedVersion
	path     string // PlayableFile's MP4
}

func (f *fakeOptimized) PlayableForItem(context.Context, uuid.UUID) ([]downloads.OptimizedVersion, error) {
	return f.versions, nil
}

func (f *fakeOptimized) PlayableFile(_ context.Context, id uuid.UUID) (downloads.OptimizedVersion, string, error) {
	for _, v := range f.versions {
		if v.ID == id {
			return v, f.path, nil
		}
	}
	return downloads.OptimizedVersion{}, "", downloads.ErrOptimizedNotFound
}

func readyVersion(fileID uuid.UUID, profile string, w, h, kbps int) downloads.OptimizedVersion {
	return downloads.OptimizedVersion{
		ID: uuid.New(), FileID: fileID, Profile: profile, Status: downloads.StatusReady,
		Width: w, Height: h, BitrateKbps: kbps, DurationMS: 60_000,
	}
}

func TestPickOptimizedVersion(t *testing.T) {
	src := uuid.New()
	tv := readyVersion(src, downloads.OptimizeTV, 1920, 1080, 8200)
	mobile := readyVersion(src, downloads.OptimizeMobile, 1280, 720, 4200)
	other := readyVersion(uuid.New(), downloads.OptimizeMobile, 1280, 720, 4200)
	versions := []downloads.OptimizedVersion{tv, mobile} // highest bitrate first
	h264 := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mp4")
	server := transcode.ServerCaps{MaxHeight: 2160}

	if got := pickOptimizedVersion(versions, src, h264, server, 0); got == nil || got.ID != tv.ID {
		t.Fatalf("no cap: got %+v, want the TV version", got)
	}
	if got := pickOptimizedVersion(versions, src, h264, server, 8000); got == nil || got.ID != mobile.ID {
		t.Fatalf("8 Mbps cap: got %+v, want the mobile version", got)
	}
	if got := pickOptimizedVersion(versions, src, h264, server, 2000); got != nil {
		t.Fatalf("2 Mbps cap: got %+v, want none (transcode instead)", got)
	}
	small := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mp4,maxWidth=1280,maxHeight=720")
	if got := pickOptimizedVersion(versions, src, small, server, 0); got == nil || got.ID != mobile.ID {
		t.Fatalf("720p client: got %+v, want the mobile version", got)
	}
	noAAC := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=ac3,protocols=mp4")
	if got := pickOptimizedVersion(versions, src, noAAC, server, 0); got != nil {
		t.Fatalf("client without AAC: got %+v, want none", got)
	}
	if got := pickOptimizedVersion([]downloads.OptimizedVersion{other}, src, h264, server, 0); got != nil {
		t.Fatal("picked a version made from another file")
	}
}

func decodeDecision(t *testing.T, rec *httptest.ResponseRecorder) playbackDecisionResponse {
	t.Helper()
	var env struct {
		Data playbackDecisionResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return env.Data
}

// A client that can't decode the HEVC source and declared optimized=1 plays
// the optimized version; one that didn't keeps its old transcode verdict.
func TestDecision_OptimizedVersionReplacesTranscode(t *testing.T) {
	fileID := uuid.New()
	file := media.File{
		ID: fileID, VideoCodec: strPtr("hevc"), AudioCodec: strPtr("aac"), Container: strPtr("mkv"),
		AudioStreams: []byte(`[{"channels":2}]`),
	}
	mobile := readyVersion(fileID, downloads.OptimizeMobile, 1280, 720, 4200)
	h := newDecisionHandler(t, file).WithOptimizedVersions(&fakeOptimized{versions: []downloads.OptimizedVersion{mobile}})

	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4", true))
	d := decodeDecision(t, rec)
	if d.Decision != "transcode" || d.OptimizedVersion != nil || d.Reason != "" {
		t.Fatalf("without optimized=1: %+v, want the plain transcode", d)
	}

	rec = httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4,optimized=1", true))
	d = decodeDecision(t, rec)
	if d.Decision != "directPlay" || d.Reason != ReasonOptimizedVersion || d.OptimizedVersion == nil {
		t.Fatalf("with optimized=1: %+v", d)
	}
	if d.FileID != fileID.String() || d.SourceDecision != "transcode" {
		t.Fatalf("file_id/source_decision = %s/%s", d.FileID, d.SourceDecision)
	}
	ov := d.OptimizedVersion
	if ov.ID != mobile.ID.String() || ov.StreamURL != "/media/optimized/"+mobile.ID.String() ||
		ov.VideoCodec != "h264" || ov.Container != "mp4" || ov.Height != 720 {
		t.Fatalf("optimized_version = %+v", ov)
	}
}

// A direct-playable source stays a direct play: an optimized version only
// replaces transcodes.
func TestDecision_OptimizedVersionNotUsedForDirectPlay(t *testing.T) {
	fileID := uuid.New()
	file := media.File{
		ID: fileID, VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`),
	}
	h := newDecisionHandler(t, file).WithOptimizedVersions(&fakeOptimized{
		versions: []downloads.OptimizedVersion{readyVersion(fileID, downloads.OptimizeMobile, 1280, 720, 4200)},
	})
	rec := httptest.NewRecorder()
	h.Decision(rec, decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4,optimized=1", true))
	if d := decodeDecision(t, rec); d.Decision != "directPlay" || d.OptimizedVersion != nil {
		t.Fatalf("got %+v, want the source's own direct play", d)
	}
}

// Over the internet limit: the source would transcode; the version under the
// cap plays instead, the one over it doesn't.
func TestDecision_OptimizedVersionUnderInternetLimit(t *testing.T) {
	fileID := uuid.New()
	br := int64(25_000_000)
	file := media.File{
		ID: fileID, VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &br,
	}
	tv := readyVersion(fileID, downloads.OptimizeTV, 1920, 1080, 8200)
	mobile := readyVersion(fileID, downloads.OptimizeMobile, 1280, 720, 4200)
	h := newDecisionHandler(t, file).
		WithRemotePolicy(staticPolicy{MaxBitrateKbps: 8000}).
		WithOptimizedVersions(&fakeOptimized{versions: []downloads.OptimizedVersion{tv, mobile}})

	req := decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4,optimized=1", true)
	req.RemoteAddr = "203.0.113.20:5000"
	rec := httptest.NewRecorder()
	h.Decision(rec, req)
	d := decodeDecision(t, rec)
	if d.Decision != "directPlay" || d.OptimizedVersion == nil || d.OptimizedVersion.ID != mobile.ID.String() {
		t.Fatalf("got %+v, want the mobile version", d)
	}
	if d.SourceDecision != "transcode" || d.SourceReason != ReasonRemoteBitrateLimit || d.MaxBitrateKbps != 0 {
		t.Fatalf("source verdict = %s/%s cap %d", d.SourceDecision, d.SourceReason, d.MaxBitrateKbps)
	}
}

func optimizedStreamRequest(id uuid.UUID, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/media/optimized/"+id.String(), nil)
	req = withChiParam(req, "id", id.String())
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	return req
}

func TestStreamOptimized_ServesAndGates(t *testing.T) {
	dir := t.TempDir()
	mp4 := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(mp4, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	libID := uuid.New()
	itemID := uuid.New()
	pg13 := "PG-13"
	ms := &mockItemMedia{item: &media.Item{ID: itemID, LibraryID: libID, Type: "movie", ContentRating: &pg13}}
	v := readyVersion(uuid.New(), downloads.OptimizeMobile, 1280, 720, 4200)
	v.ItemID = itemID
	tracker := streaming.NewTracker()
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, tracker, slog.Default()).
		WithOptimizedVersions(&fakeOptimized{versions: []downloads.OptimizedVersion{v}, path: mp4})
	user := &auth.Claims{UserID: uuid.New(), Username: "u", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}

	// Served with Range support, and recorded for Now Playing.
	req := optimizedStreamRequest(v.ID, user)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	h.StreamOptimized(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("range: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type = %q", ct)
	}
	entries := tracker.List()
	if len(entries) != 1 || entries[0].OptimizedID != v.ID || entries[0].FileID != v.FileID || entries[0].MediaItemID != itemID {
		t.Fatalf("tracker = %+v", entries)
	}

	// Unknown id.
	rec = httptest.NewRecorder()
	h.StreamOptimized(rec, optimizedStreamRequest(uuid.New(), user))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown version: %d", rec.Code)
	}

	// No access to the library: indistinguishable from missing.
	h.WithLibraryAccess(&mockAccess{allow: map[uuid.UUID]struct{}{}})
	rec = httptest.NewRecorder()
	h.StreamOptimized(rec, optimizedStreamRequest(v.ID, user))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no library access: %d", rec.Code)
	}
	h.WithLibraryAccess(nil)

	// Rating ceiling below the item's rating.
	kid := *user
	kid.MaxContentRating = "PG"
	rec = httptest.NewRecorder()
	h.StreamOptimized(rec, optimizedStreamRequest(v.ID, &kid))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("over the rating ceiling: %d", rec.Code)
	}
}

func TestItemGet_OptimizedVersionsAreNotFiles(t *testing.T) {
	itemID := uuid.New()
	fileID := uuid.New()
	ms := &mockItemMedia{
		item:  &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie", Title: "M"},
		files: []media.File{{ID: fileID, MediaItemID: itemID, Status: "active", VideoCodec: strPtr("hevc")}},
	}
	v := readyVersion(fileID, downloads.OptimizeMobile, 1280, 720, 4200)
	h := newItemHandler(ms).WithOptimizedVersions(&fakeOptimized{versions: []downloads.OptimizedVersion{v}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/items/"+itemID.String(), nil)
	req = withClaims(withChiParam(req, "id", itemID.String()))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data ItemDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Files) != 1 || env.Data.Files[0].ID != fileID.String() {
		t.Fatalf("files = %+v, want only the source", env.Data.Files)
	}
	if len(env.Data.OptimizedVersions) != 1 || env.Data.OptimizedVersions[0].ID != v.ID.String() ||
		env.Data.OptimizedVersions[0].FileID != fileID.String() {
		t.Fatalf("optimized_versions = %+v", env.Data.OptimizedVersions)
	}
}

// fakeOptimizeStore records the admin calls.
type fakeOptimizeStore struct {
	sel       downloads.Selection
	targets   []uuid.UUID
	outcomes  map[uuid.UUID]downloads.OptimizeOutcome
	requested []uuid.UUID
	woken     bool
	deleted   []uuid.UUID
}

func (f *fakeOptimizeStore) Targets(_ context.Context, sel downloads.Selection) ([]uuid.UUID, bool, error) {
	f.sel = sel
	return f.targets, false, nil
}
func (f *fakeOptimizeStore) RequestItem(_ context.Context, _, itemID uuid.UUID, _ string) (downloads.OptimizeOutcome, string, error) {
	f.requested = append(f.requested, itemID)
	o := f.outcomes[itemID]
	if o == "" {
		o = downloads.OptimizeQueued
	}
	reason := ""
	if o == downloads.OptimizeSkipped {
		reason = "the file is damaged"
	}
	return o, reason, nil
}
func (f *fakeOptimizeStore) Wake() { f.woken = true }
func (f *fakeOptimizeStore) List(context.Context, downloads.ListFilter) ([]downloads.OptimizedVersion, map[string]int64, error) {
	return nil, map[string]int64{}, nil
}
func (f *fakeOptimizeStore) Get(context.Context, uuid.UUID) (downloads.OptimizedVersion, error) {
	return downloads.OptimizedVersion{}, downloads.ErrOptimizedNotFound
}
func (f *fakeOptimizeStore) Delete(_ context.Context, id uuid.UUID) (downloads.OptimizedVersion, error) {
	f.deleted = append(f.deleted, id)
	return downloads.OptimizedVersion{ID: id}, nil
}

func postOptimize(t *testing.T, h *OptimizeHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := withClaims(httptest.NewRequest(http.MethodPost, "/api/v1/optimize", bytes.NewBufferString(body)))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

func TestOptimizeCreate_Validation(t *testing.T) {
	h := NewOptimizeHandler(&fakeOptimizeStore{}, slog.Default())
	lib := uuid.New().String()
	for name, body := range map[string]string{
		"no selector":   `{"profile":"mobile_720p"}`,
		"two selectors": `{"profile":"mobile_720p","library_id":"` + lib + `","item_ids":["` + uuid.New().String() + `"]}`,
		"bad profile":   `{"profile":"4k_hdr","library_id":"` + lib + `"}`,
		"bad item id":   `{"profile":"mobile_720p","item_ids":["nope"]}`,
	} {
		if rec := postOptimize(t, h, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422 (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestOptimizeCreate_CountsOutcomes(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	store := &fakeOptimizeStore{
		targets:  []uuid.UUID{a, b, c},
		outcomes: map[uuid.UUID]downloads.OptimizeOutcome{b: downloads.OptimizeExisting, c: downloads.OptimizeSkipped},
	}
	h := NewOptimizeHandler(store, slog.Default())
	lib := uuid.New()
	rec := postOptimize(t, h, `{"profile":"tv_1080p","library_id":"`+lib.String()+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data OptimizeResultJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	r := env.Data
	if r.Items != 3 || r.Queued != 1 || r.Existing != 1 || r.Skipped != 1 ||
		len(r.SkippedItems) != 1 || r.SkippedItems[0].ItemID != c.String() {
		t.Fatalf("result = %+v", r)
	}
	if store.sel.LibraryID == nil || *store.sel.LibraryID != lib || !store.woken {
		t.Fatalf("selection %+v woken %v", store.sel, store.woken)
	}
}

type fakeSessionOptimized struct{ v downloads.OptimizedVersion }

func (f fakeSessionOptimized) Get(_ context.Context, id uuid.UUID) (downloads.OptimizedVersion, error) {
	if id != f.v.ID {
		return downloads.OptimizedVersion{}, downloads.ErrOptimizedNotFound
	}
	return f.v, nil
}

func TestSessions_ApplyOptimized(t *testing.T) {
	v := readyVersion(uuid.New(), downloads.OptimizeMobile, 1280, 720, 4200)
	h := (&NativeSessionsHandler{logger: slog.Default()}).WithOptimizedVersions(fakeSessionOptimized{v: v})
	var as activeSession
	h.applyOptimized(context.Background(), &as, v.ID)
	if as.OptimizedVersion == nil || as.OptimizedVersion.Profile != downloads.OptimizeMobile || as.OptimizedVersion.Label == "" {
		t.Fatalf("optimized = %+v", as.OptimizedVersion)
	}
	if as.Output == nil || as.Output.VideoCodec != "h264" || as.Output.Height != 720 || as.BitrateKbps == nil || *as.BitrateKbps != 4200 {
		t.Fatalf("output = %+v bitrate %v", as.Output, as.BitrateKbps)
	}
	// Deleted mid-play: still flagged, no details.
	var gone activeSession
	h.applyOptimized(context.Background(), &gone, uuid.New())
	if gone.OptimizedVersion == nil || gone.Output != nil {
		t.Fatalf("deleted version card = %+v", gone)
	}
}
