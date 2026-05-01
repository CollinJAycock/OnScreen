package v1

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

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
	}, cfg, slog.Default())

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
	h := NewNativeTranscodeHandler(store, segToken, mediaSvc, cfg, slog.Default())
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

