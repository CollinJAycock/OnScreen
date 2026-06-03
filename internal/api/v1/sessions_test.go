package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

type stubSessionItems struct{}

func (s *stubSessionItems) GetMediaItemsForSessions(_ context.Context, _ []uuid.UUID) ([]gen.SessionMediaItem, error) {
	return nil, nil
}
func (s *stubSessionItems) GetMediaItemByFilePath(_ context.Context, _ string) (*gen.SessionMediaItem, error) {
	return nil, nil
}

// mapSessionItems resolves items by id (for progress-heartbeat entries) and by
// path (for file-traffic entries).
type mapSessionItems struct {
	byID   map[uuid.UUID]gen.SessionMediaItem
	byPath map[string]gen.SessionMediaItem
}

func (s *mapSessionItems) GetMediaItemsForSessions(_ context.Context, ids []uuid.UUID) ([]gen.SessionMediaItem, error) {
	var out []gen.SessionMediaItem
	for _, id := range ids {
		if mi, ok := s.byID[id]; ok {
			out = append(out, mi)
		}
	}
	return out, nil
}
func (s *mapSessionItems) GetMediaItemByFilePath(_ context.Context, p string) (*gen.SessionMediaItem, error) {
	if mi, ok := s.byPath[p]; ok {
		return &mi, nil
	}
	return nil, nil
}

// An ABR parent reports the rung the player settled on (with that rung's
// bitrate), and its internal rung-child sessions are hidden from the listing.
func TestSessions_List_ABRRenditionAndChildFiltering(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	ctx := context.Background()
	now := time.Now().UTC()

	parent := transcode.Session{
		ID:             transcode.NewSessionID(),
		UserID:         uuid.New(),
		MediaItemID:    uuid.New(),
		FileID:         uuid.New(),
		Decision:       "transcode",
		FilePath:       "/media/movie.mkv",
		CreatedAt:      now,
		LastActivityAt: now,
		ABR:            true,
		ABRRenditions: []transcode.Rendition{
			{Label: "720p", Height: 720, Width: 1280, BitrateKbps: 2534},
			{Label: "480p", Height: 480, Width: 854, BitrateKbps: 1660},
		},
		SelectedRendition: "720p",
	}
	if err := store.Create(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	// Active rung child — must NOT appear as its own card.
	child := transcode.Session{
		ID:             parent.ID + "-r720p",
		MediaItemID:    parent.MediaItemID,
		Decision:       "transcode",
		FilePath:       "/media/movie.mkv",
		CreatedAt:      now,
		LastActivityAt: now,
		ParentID:       parent.ID,
		StartSeg:       0,
	}
	if err := store.Create(ctx, child); err != nil {
		t.Fatalf("create child: %v", err)
	}

	h := NewNativeSessionsHandler(store, nil, &stubSessionItems{}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var body struct {
		Data []struct {
			ID                string  `json:"id"`
			SelectedRendition *string `json:"selected_rendition"`
			BitrateKbps       *int    `json:"bitrate_kbps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}

	if len(body.Data) != 1 {
		t.Fatalf("want exactly 1 session (parent only), got %d: %s", len(body.Data), rec.Body.String())
	}
	got := body.Data[0]
	if got.ID != parent.ID {
		t.Errorf("listed session %s, want parent %s", got.ID, parent.ID)
	}
	if got.SelectedRendition == nil || *got.SelectedRendition != "720p" {
		t.Errorf("selected_rendition: got %v, want 720p", got.SelectedRendition)
	}
	// Parent runs no encode; bitrate must come from the selected rung.
	if got.BitrateKbps == nil || *got.BitrateKbps != 2534 {
		t.Errorf("bitrate_kbps: got %v, want 2534 (the 720p rung)", got.BitrateKbps)
	}
}

// A direct-play stream with no Valkey session (e.g. a browser streaming straight
// from /media/files) is kept in "Now Playing" by the player's progress heartbeat,
// which the tracker records by item id. Regression: such streams used to vanish
// ~45s in, once the client buffered ahead and stopped fetching bytes.
func TestSessions_List_DirectPlayHeartbeatVisible(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	tr := streaming.NewTracker()
	itemID := uuid.New()
	tr.Heartbeat("10.0.0.9", itemID, "Chrome")

	items := &mapSessionItems{byID: map[uuid.UUID]gen.SessionMediaItem{
		itemID: {ID: itemID, Title: "Vox Machina", Type: "show"},
	}}
	h := NewNativeSessionsHandler(store, tr, items, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			Title    string `json:"title"`
			Decision string `json:"decision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(body.Data) != 1 {
		t.Fatalf("want 1 direct-play card from the heartbeat, got %d: %s", len(body.Data), rec.Body.String())
	}
	if body.Data[0].Title != "Vox Machina" || body.Data[0].Decision != "directPlay" {
		t.Errorf("card: got %+v, want Vox Machina/directPlay", body.Data[0])
	}
}

// A transcode stream sends progress beacons too, so it also gets a tracker
// heartbeat — but it already shows via its Valkey session, so the heartbeat must
// be deduped out (one card, not two).
func TestSessions_List_HeartbeatDedupedAgainstSession(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	ctx := context.Background()
	now := time.Now().UTC()

	itemID := uuid.New()
	sess := transcode.Session{
		ID: transcode.NewSessionID(), UserID: uuid.New(), MediaItemID: itemID, FileID: uuid.New(),
		Decision: "transcode", FilePath: "/media/movie.mkv", CreatedAt: now, LastActivityAt: now,
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	tr := streaming.NewTracker()
	tr.Heartbeat("10.0.0.9", itemID, "Chrome") // same item, would double-show without dedup

	items := &mapSessionItems{byID: map[uuid.UUID]gen.SessionMediaItem{
		itemID: {ID: itemID, Title: "Movie", Type: "movie"},
	}}
	h := NewNativeSessionsHandler(store, tr, items, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			Decision string `json:"decision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(body.Data) != 1 {
		t.Fatalf("want 1 card (heartbeat deduped against the session), got %d: %s", len(body.Data), rec.Body.String())
	}
	if body.Data[0].Decision != "transcode" {
		t.Errorf("decision: got %q, want transcode (the Valkey session wins)", body.Data[0].Decision)
	}
}

// The endpoint is authenticated — no claims means 401, never a session list.
func TestSessions_List_RequiresAuth(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	h := NewNativeSessionsHandler(store, nil, &stubSessionItems{}, slog.Default())

	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

// A non-admin must see only their own sessions, never other users' activity.
func TestSessions_List_NonAdminSeesOnlyOwn(t *testing.T) {
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	ctx := context.Background()
	now := time.Now().UTC()

	me := uuid.New()
	other := uuid.New()
	mine := transcode.Session{
		ID: transcode.NewSessionID(), UserID: me, MediaItemID: uuid.New(), FileID: uuid.New(),
		Decision: "transcode", FilePath: "/media/mine.mkv", CreatedAt: now, LastActivityAt: now,
	}
	theirs := transcode.Session{
		ID: transcode.NewSessionID(), UserID: other, MediaItemID: uuid.New(), FileID: uuid.New(),
		Decision: "transcode", FilePath: "/media/theirs.mkv", CreatedAt: now, LastActivityAt: now,
	}
	if err := store.Create(ctx, mine); err != nil {
		t.Fatalf("create mine: %v", err)
	}
	if err := store.Create(ctx, theirs); err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	h := NewNativeSessionsHandler(store, nil, &stubSessionItems{}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: me, IsAdmin: false}))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(body.Data) != 1 {
		t.Fatalf("non-admin should see only their own session, got %d: %s", len(body.Data), rec.Body.String())
	}
	if body.Data[0].ID != mine.ID {
		t.Errorf("listed %s, want own session %s", body.Data[0].ID, mine.ID)
	}
}
