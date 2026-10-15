package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/downloads"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type dlFakeService struct {
	jobs      map[uuid.UUID]downloads.Job
	active    int
	requested struct {
		user, item, file uuid.UUID
		quality          string
	}
	existing bool // Request returns an existing job (created=false)
	path     string
}

func (s *dlFakeService) Request(_ context.Context, userID, itemID, fileID uuid.UUID, quality string) (downloads.Job, bool, error) {
	s.requested.user, s.requested.item, s.requested.file, s.requested.quality = userID, itemID, fileID, quality
	j := downloads.Job{ID: uuid.New(), UserID: userID, ItemID: itemID, FileID: fileID, Quality: quality,
		Status: downloads.StatusQueued, ItemTitle: "Film", ItemType: "movie", CreatedAt: time.Now()}
	return j, !s.existing, nil
}
func (s *dlFakeService) ActiveCount(context.Context, uuid.UUID) (int, error) { return s.active, nil }
func (s *dlFakeService) Get(_ context.Context, id uuid.UUID) (downloads.Job, error) {
	j, ok := s.jobs[id]
	if !ok {
		return downloads.Job{}, downloads.ErrNotFound
	}
	return j, nil
}
func (s *dlFakeService) ListForUser(_ context.Context, userID uuid.UUID) ([]downloads.Job, error) {
	var out []downloads.Job
	for _, j := range s.jobs {
		if j.UserID == userID {
			out = append(out, j)
		}
	}
	return out, nil
}
func (s *dlFakeService) Delete(_ context.Context, userID, id uuid.UUID) error {
	j, ok := s.jobs[id]
	if !ok || j.UserID != userID {
		return downloads.ErrNotFound
	}
	delete(s.jobs, id)
	return nil
}
func (s *dlFakeService) ReadyFile(_ context.Context, j downloads.Job) (string, error) {
	if j.Status != downloads.StatusReady {
		return "", downloads.ErrNotReady
	}
	return s.path, nil
}

type dlFakeMedia struct {
	item  media.Item
	files []media.File
}

func (m *dlFakeMedia) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if id != m.item.ID {
		return nil, media.ErrNotFound
	}
	it := m.item
	return &it, nil
}
func (m *dlFakeMedia) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	for i := range m.files {
		if m.files[i].ID == id {
			f := m.files[i]
			return &f, nil
		}
	}
	return nil, media.ErrNotFound
}
func (m *dlFakeMedia) GetFiles(context.Context, uuid.UUID) ([]media.File, error) {
	return append([]media.File(nil), m.files...), nil
}

type dlCaps struct{ caps StreamCaps }

func (c dlCaps) GetStreamCaps(context.Context, uuid.UUID) (StreamCaps, error) { return c.caps, nil }

type dlPolicy struct{ p RemoteStreamPolicy }

func (d dlPolicy) RemoteStreamPolicy(context.Context) RemoteStreamPolicy { return d.p }

type dlAccess struct{ allow bool }

func (a dlAccess) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return a.allow, nil
}
func (a dlAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

type dlGateOff struct{}

func (dlGateOff) WebDownloadsEnabled(context.Context) bool { return false }

// ── helpers ──────────────────────────────────────────────────────────────────

type dlFixture struct {
	h     *DownloadsHandler
	svc   *dlFakeService
	media *dlFakeMedia
	user  uuid.UUID
	item  uuid.UUID
	file  uuid.UUID
}

func newDLFixture(capKbps int) dlFixture {
	str := func(s string) *string { return &s }
	item := media.Item{ID: uuid.New(), LibraryID: uuid.New(), Title: "Film", Type: "movie", ContentRating: str("R")}
	file := media.File{ID: uuid.New(), MediaItemID: item.ID, Status: "active", FilePath: "/m/film.mkv", VideoCodec: str("h264")}
	svc := &dlFakeService{jobs: map[uuid.UUID]downloads.Job{}}
	m := &dlFakeMedia{item: item, files: []media.File{file}}
	h := NewDownloadsHandler(svc, m, silentLogger()).
		WithLibraryAccess(dlAccess{allow: true}).
		WithStreamCaps(dlCaps{caps: StreamCaps{BitrateKbps: capKbps}}, nil)
	return dlFixture{h: h, svc: svc, media: m, user: uuid.New(), item: item.ID, file: file.ID}
}

func dlRequest(method, target string, body any, claims *auth.Claims, params map[string]string) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, target, &buf)
	r.RemoteAddr = "192.168.1.20:5000" // home network unless a test says otherwise
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	if claims != nil {
		ctx = middleware.WithClaims(ctx, claims)
	}
	return r.WithContext(ctx)
}

func (f dlFixture) create(t *testing.T, quality string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	if claims == nil {
		claims = &auth.Claims{UserID: f.user}
	}
	rec := httptest.NewRecorder()
	f.h.Create(rec, dlRequest(http.MethodPost, "/", map[string]string{"quality": quality}, claims,
		map[string]string{"id": f.item.String()}))
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestDownloads_OptionsFollowBitrateCap(t *testing.T) {
	f := newDLFixture(4000)
	rec := httptest.NewRecorder()
	f.h.Options(rec, dlRequest(http.MethodGet, "/", nil, &auth.Claims{UserID: f.user},
		map[string]string{"id": f.item.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		Data DownloadOptionsJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.BitrateCapKbps != 4000 || len(resp.Data.Qualities) != 4 {
		t.Fatalf("got %+v", resp.Data)
	}
	for _, q := range resp.Data.Qualities {
		if want := q.ID != "1080p_8mbps"; q.Allowed != want {
			t.Errorf("%s allowed = %v, want %v", q.ID, q.Allowed, want)
		}
	}
}

func TestDownloads_CreateRefusesQualityAboveCap(t *testing.T) {
	f := newDLFixture(4000)
	rec := f.create(t, "1080p_8mbps", nil)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "QUALITY_ABOVE_LIMIT" {
		t.Fatalf("1080p over a 4 Mbps cap: %d %s", rec.Code, rec.Body)
	}
	if rec := f.create(t, "720p_4mbps", nil); rec.Code != http.StatusCreated {
		t.Fatalf("720p 4 Mbps under the cap: %d %s", rec.Code, rec.Body)
	}
	if f.svc.requested.quality != "720p_4mbps" || f.svc.requested.file != f.file || f.svc.requested.user != f.user {
		t.Errorf("service got %+v", f.svc.requested)
	}
}

// From outside the home network the internet limit binds too.
func TestDownloads_CreateAppliesInternetLimit(t *testing.T) {
	f := newDLFixture(0)
	f.h.WithStreamCaps(dlCaps{}, dlPolicy{p: RemoteStreamPolicy{MaxBitrateKbps: 2000}})
	claims := &auth.Claims{UserID: f.user}
	req := func() *http.Request {
		return dlRequest(http.MethodPost, "/", map[string]string{"quality": "720p_4mbps"}, claims,
			map[string]string{"id": f.item.String()})
	}
	lan := httptest.NewRecorder()
	f.h.Create(lan, req())
	if lan.Code != http.StatusCreated {
		t.Fatalf("LAN: %d %s", lan.Code, lan.Body)
	}
	remote := req()
	remote.RemoteAddr = "203.0.113.9:4000"
	rec := httptest.NewRecorder()
	f.h.Create(rec, remote)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote over the internet limit: %d %s", rec.Code, rec.Body)
	}
}

func TestDownloads_CreateExistingReturns200(t *testing.T) {
	f := newDLFixture(0)
	f.svc.existing = true
	if rec := f.create(t, "480p_1mbps", nil); rec.Code != http.StatusOK {
		t.Fatalf("existing job: %d", rec.Code)
	}
}

func TestDownloads_CreateValidation(t *testing.T) {
	f := newDLFixture(0)
	if rec := f.create(t, "original", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("original: %d", rec.Code)
	}
	if rec := f.create(t, "4k", nil); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Errorf("unknown quality: %d", rec.Code)
	}
	f.svc.active = maxActiveDownloadJobs
	if rec := f.create(t, "480p_1mbps", nil); rec.Code != http.StatusTooManyRequests {
		t.Errorf("over the active cap: %d", rec.Code)
	}
}

func TestDownloads_CreateGates(t *testing.T) {
	f := newDLFixture(0)
	f.h.WithDownloadGate(dlGateOff{})
	if rec := f.create(t, "480p_1mbps", nil); rec.Code != http.StatusForbidden || errCode(t, rec) != "DOWNLOADS_DISABLED" {
		t.Errorf("downloads disabled: %d %s", rec.Code, rec.Body)
	}

	f = newDLFixture(0)
	f.h.WithLibraryAccess(dlAccess{allow: false})
	if rec := f.create(t, "480p_1mbps", nil); rec.Code != http.StatusNotFound {
		t.Errorf("no library access: %d", rec.Code)
	}

	f = newDLFixture(0)
	if rec := f.create(t, "480p_1mbps", &auth.Claims{UserID: f.user, MaxContentRating: "PG-13"}); rec.Code != http.StatusForbidden {
		t.Errorf("over the rating ceiling: %d", rec.Code)
	}

	f = newDLFixture(0)
	f.media.files[0].IntegrityStatus = media.IntegrityDamaged
	if rec := f.create(t, "480p_1mbps", nil); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("damaged file: %d", rec.Code)
	}

	// A file_id from another item is not this item's file.
	f = newDLFixture(0)
	rec := httptest.NewRecorder()
	f.h.Create(rec, dlRequest(http.MethodPost, "/", map[string]string{"quality": "480p_1mbps", "file_id": uuid.NewString()},
		&auth.Claims{UserID: f.user}, map[string]string{"id": f.item.String()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("foreign file_id: %d", rec.Code)
	}
}

// Any part of a stacked movie converts the whole movie, keyed on part 1.
func TestDownloads_CreateStackedUsesFirstPart(t *testing.T) {
	f := newDLFixture(0)
	one, two := 1, 2
	p1 := f.media.files[0]
	p1.PartIndex = &one
	p1.FilePath = "/m/Film cd1.avi"
	p2 := p1
	p2.ID, p2.PartIndex, p2.FilePath = uuid.New(), &two, "/m/Film cd2.avi"
	f.media.files = []media.File{p1, p2}
	rec := httptest.NewRecorder()
	f.h.Create(rec, dlRequest(http.MethodPost, "/", map[string]string{"quality": "480p_1mbps", "file_id": p2.ID.String()},
		&auth.Claims{UserID: f.user}, map[string]string{"id": f.item.String()}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if f.svc.requested.file != p1.ID {
		t.Errorf("conversion keyed on %s, want part 1 %s", f.svc.requested.file, p1.ID)
	}
}

func TestDownloads_JobsAreOwnerOnly(t *testing.T) {
	f := newDLFixture(0)
	mine := downloads.Job{ID: uuid.New(), UserID: f.user, ItemID: f.item, Quality: "480p_1mbps", Status: downloads.StatusRunning, Progress: 0.4}
	f.svc.jobs[mine.ID] = mine
	other := &auth.Claims{UserID: uuid.New()}
	params := map[string]string{"job": mine.ID.String()}

	rec := httptest.NewRecorder()
	f.h.Get(rec, dlRequest(http.MethodGet, "/", nil, other, params))
	if rec.Code != http.StatusNotFound {
		t.Errorf("someone else's job: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	f.h.Delete(rec, dlRequest(http.MethodDelete, "/", nil, other, params))
	if rec.Code != http.StatusNotFound {
		t.Errorf("deleting someone else's job: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	f.h.Get(rec, dlRequest(http.MethodGet, "/", nil, &auth.Claims{UserID: f.user}, params))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"progress":0.4`) ||
		!strings.Contains(rec.Body.String(), `"quality_label":"480p · 1 Mbps"`) {
		t.Errorf("own job: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	f.h.ListMine(rec, dlRequest(http.MethodGet, "/", nil, other, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("other user's list: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	f.h.Delete(rec, dlRequest(http.MethodDelete, "/", nil, &auth.Claims{UserID: f.user}, params))
	if rec.Code != http.StatusNoContent {
		t.Errorf("own delete: %d", rec.Code)
	}
}

func TestDownloads_FileServesReadyMP4WithRange(t *testing.T) {
	f := newDLFixture(0)
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.svc.path = path
	ready := downloads.Job{ID: uuid.New(), UserID: f.user, ItemID: f.item, FileID: f.file, Quality: "720p_2mbps", Status: downloads.StatusReady}
	running := downloads.Job{ID: uuid.New(), UserID: f.user, ItemID: f.item, FileID: f.file, Quality: "720p_2mbps", Status: downloads.StatusRunning}
	f.svc.jobs[ready.ID], f.svc.jobs[running.ID] = ready, running
	claims := &auth.Claims{UserID: f.user}

	req := dlRequest(http.MethodGet, "/", nil, claims, map[string]string{"job": ready.ID.String()})
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	f.h.File(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("range: %d %q", rec.Code, rec.Body)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="Film (720p · 2 Mbps).mp4"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}

	rec = httptest.NewRecorder()
	f.h.File(rec, dlRequest(http.MethodGet, "/", nil, claims, map[string]string{"job": running.ID.String()}))
	if rec.Code != http.StatusConflict {
		t.Errorf("not ready: %d", rec.Code)
	}

	// The cap is re-checked at fetch time: a 2 Mbps file fetched by a user
	// now held to 1 Mbps is refused.
	f.h.WithStreamCaps(dlCaps{caps: StreamCaps{BitrateKbps: 1000}}, nil)
	rec = httptest.NewRecorder()
	f.h.File(rec, dlRequest(http.MethodGet, "/", nil, claims, map[string]string{"job": ready.ID.String()}))
	if rec.Code != http.StatusForbidden {
		t.Errorf("over the cap at fetch time: %d", rec.Code)
	}
}

func TestDownloads_JobJSONFileURLOnlyWhenReady(t *testing.T) {
	id := uuid.New()
	if got := toDownloadJobJSON(downloads.Job{ID: id, Status: downloads.StatusReady}).FileURL; got != "/api/v1/downloads/"+id.String()+"/file" {
		t.Errorf("ready file_url = %q", got)
	}
	if got := toDownloadJobJSON(downloads.Job{ID: id, Status: downloads.StatusQueued}).FileURL; got != "" {
		t.Errorf("queued file_url = %q", got)
	}
}
