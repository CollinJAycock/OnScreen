package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// The playlist wait was exactly as long as the server's WriteTimeout, so for a
// session that never became ready the 503 was written after the connection's
// write deadline and the client saw a connection reset ("socket hang up")
// instead of the status. Reproduced at small scale: a WriteTimeout shorter
// than the wait, and a session no worker ever claims.
func TestPlaylist_NotReady503SurvivesServerWriteTimeout(t *testing.T) {
	orig := playlistReadyWait
	playlistReadyWait = 500 * time.Millisecond
	t.Cleanup(func() { playlistReadyWait = orig })

	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segTok := transcode.NewSegmentTokenManager(v)
	h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{}, &config.Config{}, slog.Default())

	ctx := context.Background()
	userID := uuid.New()
	sid := uuid.NewString()
	// WorkerAddr stays empty: no worker ever claims the job.
	if err := store.Create(ctx, transcode.Session{ID: sid, UserID: userID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tok, err := segTok.Issue(ctx, sid, userID)
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/sessions/{sid}/playlist.m3u8", h.Playlist)
	srv := httptest.NewUnstartedServer(r)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/sessions/" + sid + "/playlist.m3u8?token=" + url.QueryEscape(tok))
	if err != nil {
		t.Fatalf("GET playlist: %v — the 503 was written past the write deadline", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
