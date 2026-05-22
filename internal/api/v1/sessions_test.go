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

	"github.com/onscreen/onscreen/internal/db/gen"
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
	h.List(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))

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
