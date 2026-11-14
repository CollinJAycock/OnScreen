package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// DELETE /api/v1/transcode/sessions/{sid} through the real router: the
// session's own token alone stops it (the Roku channel through 1.1 sends no
// bearer on this call, and every stop 401'd, leaving ffmpeg to the idle
// sweep), while a bad credential is still a 401 and the transcode routes
// around it still need a user.
func TestRouter_StopTranscodeWithSessionToken(t *testing.T) {
	mr := miniredis.RunT(t)
	vc, err := valkey.New(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	store := transcode.NewSessionStore(vc)
	segToken := transcode.NewSegmentTokenManager(vc)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) {
		h.NativeTranscode = v1.NewNativeTranscodeHandler(store, segToken, nil, &config.Config{}, logger)
	})

	ctx := context.Background()
	newSession := func() (transcode.Session, string) {
		t.Helper()
		owner := uuid.New()
		sess := transcode.Session{ID: transcode.NewSessionID(), UserID: owner, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: time.Now().UTC()}
		tok, err := segToken.Issue(ctx, sess.ID, owner)
		if err != nil {
			t.Fatal(err)
		}
		sess.SegToken = tok
		if err := store.Create(ctx, sess); err != nil {
			t.Fatal(err)
		}
		return sess, tok
	}
	do := func(req *http.Request) int {
		req.RemoteAddr = "203.0.113.20:5000"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// The session token alone: stopped.
	sess, tok := newSession()
	if code := do(httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID+"?token="+tok, nil)); code != http.StatusNoContent {
		t.Fatalf("token-only stop: got %d, want 204", code)
	}
	if got, err := store.Get(ctx, sess.ID); err == nil && got != nil {
		t.Errorf("session survived the stop")
	}

	// A broken bearer next to a valid token: Required answers, not the token.
	sess, tok = newSession()
	req := httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID+"?token="+tok, nil)
	req.Header.Set("Authorization", "Bearer not-a-token")
	if code := do(req); code != http.StatusUnauthorized {
		t.Errorf("bad bearer + token: got %d, want 401", code)
	}
	if got, err := store.Get(ctx, sess.ID); err != nil || got == nil {
		t.Errorf("a refused stop must leave the session; err=%v", err)
	}

	// No credential and no token, or another session's token: 401.
	if code := do(httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID, nil)); code != http.StatusUnauthorized {
		t.Errorf("no credential: got %d, want 401", code)
	}
	other, otherTok := newSession()
	_ = other
	if code := do(httptest.NewRequest("DELETE", "/api/v1/transcode/sessions/"+sess.ID+"?token="+otherTok, nil)); code != http.StatusUnauthorized {
		t.Errorf("another session's token: got %d, want 401", code)
	}

	// The neighbouring transcode routes still need a user: a session token
	// is no credential there.
	for _, r := range []*http.Request{
		httptest.NewRequest("POST", "/api/v1/items/"+uuid.NewString()+"/transcode?token="+tok, nil),
		httptest.NewRequest("POST", "/api/v1/items/"+uuid.NewString()+"/playback-decision?token="+tok, nil),
	} {
		if code := do(r); code != http.StatusUnauthorized {
			t.Errorf("%s %s with only a session token: got %d, want 401", r.Method, r.URL.Path, code)
		}
	}
}
