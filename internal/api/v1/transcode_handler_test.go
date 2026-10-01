package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// fakeSourceOK is the verifySource stub used by every Start-handler test —
// the mock file paths never resolve on disk, so the real scanner.VerifySource
// would short-circuit with SOURCE_MISSING and skip the code paths the tests
// are actually exercising (supersede, dispatch, quota, …).
func fakeSourceOK(context.Context, string) (scanner.SourceStatus, error) {
	return scanner.SourceOK, nil
}

// ── mocks ────────────────────────────────────────────────────────────────────

type mockTranscodeMedia struct {
	item  *media.Item
	files []media.File
}

func (m *mockTranscodeMedia) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if m.item != nil {
		return m.item, nil
	}
	return nil, media.ErrNotFound
}

func (m *mockTranscodeMedia) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	for i := range m.files {
		if m.files[i].ID == id {
			return &m.files[i], nil
		}
	}
	return nil, media.ErrNotFound
}

func (m *mockTranscodeMedia) GetFiles(_ context.Context, itemID uuid.UUID) ([]media.File, error) {
	return m.files, nil
}

type mockSessionKiller struct {
	killed []string
}

func (m *mockSessionKiller) KillSession(sessionID string) {
	m.killed = append(m.killed, sessionID)
}

func newTestHandler(t *testing.T) (*NativeTranscodeHandler, *transcode.SessionStore) {
	t.Helper()
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segToken := transcode.NewSegmentTokenManager(v)

	cfg := &config.Config{
		TranscodeMaxHeight: 2160,
	}

	h := NewNativeTranscodeHandler(store, segToken, &mockTranscodeMedia{
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
		files: []media.File{{
			ID:         uuid.New(),
			FilePath:   "/media/test.mkv",
			VideoCodec: strPtr("h264"),
			AudioCodec: strPtr("aac"),
		}},
	}, cfg, slog.Default()).WithVerifySource(fakeSourceOK)

	return h, store
}

func strPtr(s string) *string { return &s }

func withClaims(r *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "admin",
		IsAdmin:   true,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

// ── Start: pre-flight source verification ──────────────────────────────────

func TestStart_SourceMissingReturns422(t *testing.T) {
	// File is absent on disk → handler must bail with SOURCE_MISSING
	// (HTTP 422) instead of dispatching to a worker that will hang on
	// the missing source for the full 60 s playlist deadline.
	h, _ := newTestHandler(t)
	h.verifySource = func(context.Context, string) (scanner.SourceStatus, error) {
		return scanner.SourceMissing, nil
	}
	body, _ := json.Marshal(transcodeStartRequest{Height: 1080})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"SOURCE_MISSING"`)) {
		t.Errorf("response should carry code=SOURCE_MISSING; got %s", rec.Body.String())
	}
}

func TestStart_SourceUnreadableReturns422(t *testing.T) {
	// File exists but ffprobe can't demux it (corrupt container) →
	// handler must surface SOURCE_UNREADABLE so the player shows a
	// real error rather than the playlist-endpoint spinner.
	h, _ := newTestHandler(t)
	h.verifySource = func(context.Context, string) (scanner.SourceStatus, error) {
		return scanner.SourceUnreadable, nil
	}
	body, _ := json.Marshal(transcodeStartRequest{Height: 1080})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"SOURCE_UNREADABLE"`)) {
		t.Errorf("response should carry code=SOURCE_UNREADABLE; got %s", rec.Body.String())
	}
}

func TestStart_SourceUnverifiedStartsAnyway(t *testing.T) {
	// ffprobe ran out of time (a large MP4 with its index at the end, a disk
	// spinning up): no verdict, so the handler must not answer 422
	// SOURCE_UNREADABLE. It carries on to the supersede/dispatch steps (which
	// fail later in this harness for want of workers: not the point here).
	h, _ := newTestHandler(t)
	called := false
	h.verifySource = func(context.Context, string) (scanner.SourceStatus, error) {
		called = true
		return scanner.SourceUnverified, errors.New("ffprobe verify: no answer within 5s: signal: killed")
	}
	body, _ := json.Marshal(transcodeStartRequest{Height: 1080})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if !called {
		t.Fatal("verifySource was not consulted")
	}
	if rec.Code == http.StatusUnprocessableEntity || bytes.Contains(rec.Body.Bytes(), []byte(`"SOURCE_UNREADABLE"`)) {
		t.Fatalf("a timed-out pre-flight must not refuse the file; got %d %s", rec.Code, rec.Body.String())
	}
}

func TestStart_DamagedFileReturns422(t *testing.T) {
	// The integrity probe marked this file damaged → Start must refuse with
	// FILE_DAMAGED, mirroring the DecisionDamaged verdict the
	// playback-decision endpoint returns, so decision-following clients and
	// decision-skipping clients end at the same clear message.
	h, _ := newTestHandler(t)
	h.media = &mockTranscodeMedia{
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Fake Release"},
		files: []media.File{{
			ID:              uuid.New(),
			FilePath:        "/media/fake.mkv",
			VideoCodec:      strPtr("hevc"),
			AudioCodec:      strPtr("eac3"),
			IntegrityStatus: media.IntegrityDamaged,
		}},
	}
	// The gate is a free DB-field read and sits BEFORE the ffprobe
	// pre-flight; a damaged file must not cost a probe.
	h.verifySource = func(context.Context, string) (scanner.SourceStatus, error) {
		t.Error("verifySource must not run for a damaged file")
		return scanner.SourceOK, nil
	}
	body, _ := json.Marshal(transcodeStartRequest{Height: 1080})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"FILE_DAMAGED"`)) {
		t.Errorf("response should carry code=FILE_DAMAGED; got %s", rec.Body.String())
	}
}

// ── Start: audio track selection ─────────────────────────────────────────────

// A remux decides audio passthrough on the track ffmpeg actually maps
// (`-map 0:a:N`). It used to read the file's top-level audio_codec — the
// FIRST track — so picking the DTS track of an AC3-first file copied DTS
// straight through to a client that can't decode it: no sound. An index past
// the last track is refused with 400 before anything runs, instead of
// starting an ffmpeg that dies on the map and never writes a playlist.
func TestStart_RemuxAudioCopyFollowsSelectedTrack(t *testing.T) {
	// [0] AC3 5.1, [1] DTS-HD MA 5.1, [2] E-AC3 5.1; the client decodes
	// AAC/AC3/E-AC3 but not DTS.
	streams := []byte(`[{"index":1,"codec":"ac3","channels":6},` +
		`{"index":2,"codec":"dts","channels":6},` +
		`{"index":3,"codec":"eac3","channels":6}]`)
	const caps = "videoDecoder=h264,audioDecoder=aac:ac3:eac3"
	cases := []struct {
		name     string
		streams  []byte // the file's audio_streams; nil = row scanned before the column
		idx      *int   // audio_stream_index; nil = default track
		status   int
		jobCodec string // the worker job's AudioCodec: "copy" or "aac"
		jobIdx   int
		shown    string // Now Playing's output audio codec
	}{
		{"default track (AC3) copies", streams, nil, http.StatusOK, "copy", -1, "ac3"},
		{"selected copyable track (E-AC3) copies", streams, ip(2), http.StatusOK, "copy", 2, "eac3"},
		{"selected DTS track behind AC3 re-encodes", streams, ip(1), http.StatusOK, "aac", 1, "aac"},
		{"out of range refused", streams, ip(3), http.StatusBadRequest, "", 0, ""},
		{"unknown track on a row with no stream list re-encodes", nil, ip(1), http.StatusOK, "aac", 1, "aac"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			itemID, user := uuid.New(), uuid.New()
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
				VideoCodec: strPtr("h264"), AudioCodec: strPtr("ac3"), AudioStreams: tc.streams,
				ResolutionW: ip(1920), ResolutionH: ip(1080)}
			h, store := newStartHandler(t, file)
			// The session the viewer is watching: a refused switch must
			// leave it alone (a successful one supersedes it).
			prior := transcode.Session{ID: transcode.NewSessionID(), UserID: user,
				MediaItemID: itemID, FileID: file.ID, Decision: "remux", CreatedAt: time.Now().UTC()}
			if err := store.Create(ctx, prior); err != nil {
				t.Fatalf("seed prior session: %v", err)
			}

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", caps,
				transcodeStartRequest{VideoCopy: true, AudioStreamIndex: tc.idx})
			if rec.Code != tc.status {
				t.Fatalf("status: got %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}

			if tc.status != http.StatusOK {
				if code, _ := errorCode(t, rec); code != "BAD_REQUEST" {
					t.Errorf("code: got %q, want BAD_REQUEST", code)
				}
				if _, err := store.Get(ctx, prior.ID); err != nil {
					t.Errorf("a refused Start must not supersede the playing session: %v", err)
				}
				if job, _ := store.DequeueJob(ctx, "", 100*time.Millisecond); job != nil {
					t.Errorf("a refused Start dispatched a job: %+v", job)
				}
				return
			}

			job := drainJob(t, store)
			if job.AudioCodec != tc.jobCodec || job.AudioStreamIndex != tc.jobIdx {
				t.Errorf("job audio: got codec=%q idx=%d, want codec=%q idx=%d",
					job.AudioCodec, job.AudioStreamIndex, tc.jobCodec, tc.jobIdx)
			}
			s := onlySession(t, store)
			if got := sessionOutput(&s, &file, nil).AudioCodec; got != tc.shown {
				t.Errorf("Now Playing audio codec: got %q, want %q", got, tc.shown)
			}
		})
	}
}

// ── Start: height validation ─────────────────────────────────────────────────

func TestStart_NegativeHeight(t *testing.T) {
	h, _ := newTestHandler(t)
	body, _ := json.Marshal(transcodeStartRequest{Height: -1})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestStart_HeightExceedsMax(t *testing.T) {
	h, _ := newTestHandler(t)
	body, _ := json.Marshal(transcodeStartRequest{Height: 9999})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestStart_ZeroHeight_Accepted(t *testing.T) {
	h, _ := newTestHandler(t)
	body, _ := json.Marshal(transcodeStartRequest{Height: 0})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	// Should NOT be 400 — height 0 means "use source".
	if rec.Code == http.StatusBadRequest {
		t.Errorf("height=0 should be accepted, got 400")
	}
}

func TestStart_Unauthorized(t *testing.T) {
	h, _ := newTestHandler(t)
	body, _ := json.Marshal(transcodeStartRequest{Height: 720})

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	// No claims attached.

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestStart_InvalidBody(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode",
		bytes.NewReader([]byte("not json")))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Stop: SessionKiller integration ──────────────────────────────────────────

func TestStop_KillsFFmpegViaSessionKiller(t *testing.T) {
	h, store := newTestHandler(t)
	killer := &mockSessionKiller{}
	h.SetSessionKiller(killer)

	// Create a session owned by the user.
	userID := uuid.New()
	sess := transcode.Session{
		ID:          transcode.NewSessionID(),
		UserID:      userID,
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		Decision:    "transcode",
		SegToken:    "seg-tok",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	claims := &auth.Claims{
		UserID:    userID,
		Username:  "admin",
		IsAdmin:   true,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sid", sess.ID)
	req = req.WithContext(context.WithValue(
		middleware.WithClaims(req.Context(), claims),
		chi.RouteCtxKey, rctx,
	))

	rec := httptest.NewRecorder()
	h.Stop(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if len(killer.killed) != 1 || killer.killed[0] != sess.ID {
		t.Errorf("expected KillSession(%q), got %v", sess.ID, killer.killed)
	}
}

func TestStop_ForbiddenForOtherUser(t *testing.T) {
	h, store := newTestHandler(t)

	sess := transcode.Session{
		ID:          transcode.NewSessionID(),
		UserID:      uuid.New(), // owned by a different user
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	claims := &auth.Claims{
		UserID:    uuid.New(), // different user
		Username:  "other",
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sid", sess.ID)
	req = req.WithContext(context.WithValue(
		middleware.WithClaims(req.Context(), claims),
		chi.RouteCtxKey, rctx,
	))

	rec := httptest.NewRecorder()
	h.Stop(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// An ADMIN may stop any user's session — the moderation affordance every
// competitor ships in core or paid. The owner-only 403 above stays the
// contract for regular users; this pins the admin bypass actually tearing
// the session down (gone from the store), not just returning 204.
func TestStop_AdminTerminatesOtherUsersSession(t *testing.T) {
	h, store := newTestHandler(t)

	sess := transcode.Session{
		ID:          transcode.NewSessionID(),
		UserID:      uuid.New(), // owned by a different user
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "admin",
		IsAdmin:   true,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sid", sess.ID)
	req = req.WithContext(context.WithValue(
		middleware.WithClaims(req.Context(), claims),
		chi.RouteCtxKey, rctx,
	))

	rec := httptest.NewRecorder()
	h.Stop(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got, err := store.Get(context.Background(), sess.ID); err == nil && got != nil {
		t.Error("session still exists after admin stop — tearDown did not run")
	}
}

// ── Start: last-writer-wins supersede ────────────────────────────────────────

func TestStart_SupersedesPriorSessionForSameUserAndItem(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segToken := transcode.NewSegmentTokenManager(v)
	cfg := &config.Config{TranscodeMaxHeight: 2160}

	itemID := uuid.New()
	fileID := uuid.New()
	mediaSvc := &mockTranscodeMedia{
		item: &media.Item{ID: itemID, Type: "movie", Title: "Test"},
		files: []media.File{{
			ID: fileID, MediaItemID: itemID,
			FilePath:   "/media/test.mkv",
			VideoCodec: strPtr("h264"),
			AudioCodec: strPtr("aac"),
		}},
	}
	h := NewNativeTranscodeHandler(store, segToken, mediaSvc, cfg, slog.Default()).WithVerifySource(fakeSourceOK)
	killer := &mockSessionKiller{}
	h.SetSessionKiller(killer)

	userID := uuid.New()

	// Pre-existing session this user has for itemID — and a noise session
	// for the same item but a different user, to confirm we don't kill it.
	priorSelf := transcode.Session{
		ID: transcode.NewSessionID(), UserID: userID, MediaItemID: itemID,
		FileID: fileID, Decision: "transcode", SegToken: "self-tok",
		CreatedAt: time.Now().UTC(),
	}
	priorOther := transcode.Session{
		ID: transcode.NewSessionID(), UserID: uuid.New(), MediaItemID: itemID,
		FileID: fileID, Decision: "transcode", SegToken: "other-tok",
		CreatedAt: time.Now().UTC(),
	}
	for _, s := range []transcode.Session{priorSelf, priorOther} {
		if err := store.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %s: %v", s.ID, err)
		}
	}

	body, _ := json.Marshal(transcodeStartRequest{Height: 720})
	req := httptest.NewRequest("POST", "/api/v1/items/"+itemID.String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", itemID.String())
	claims := &auth.Claims{
		UserID: userID, Username: "user", IsAdmin: false,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))

	rec := httptest.NewRecorder()
	h.Start(rec, req)

	// DispatchJob has no workers registered so Start fails after supersede
	// runs. We don't care about the response code — we care that the prior
	// self-session was killed and the other user's session was left alone.
	if len(killer.killed) != 1 || killer.killed[0] != priorSelf.ID {
		t.Fatalf("expected KillSession(%q), got %v", priorSelf.ID, killer.killed)
	}
	if _, err := store.Get(context.Background(), priorSelf.ID); err == nil {
		t.Errorf("prior self-session should have been deleted")
	}
	if got, err := store.Get(context.Background(), priorOther.ID); err != nil || got == nil {
		t.Errorf("other user's session must NOT be superseded; got err=%v sess=%v", err, got)
	}
}

func TestStop_Unauthorized(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/some-id", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sid", "some-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// No claims.

	rec := httptest.NewRecorder()
	h.Stop(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
