package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// newTestHandlerWithCodec is newTestHandler with a caller-chosen source video
// codec, since the container a session ends up in is decided by exactly that.
func newTestHandlerWithCodec(t *testing.T, videoCodec string) (*NativeTranscodeHandler, *transcode.SessionStore) {
	t.Helper()
	v := testvalkey.New(t)
	store := transcode.NewSessionStore(v)
	segToken := transcode.NewSegmentTokenManager(v)

	h := NewNativeTranscodeHandler(store, segToken, &mockTranscodeMedia{
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
		files: []media.File{{
			ID:         uuid.New(),
			FilePath:   "/media/test.mkv",
			VideoCodec: strPtr(videoCodec),
			AudioCodec: strPtr("ac3"),
		}},
	}, &config.Config{TranscodeMaxHeight: 2160}, slog.Default()).
		WithVerifySource(fakeSourceOK)

	return h, store
}

// startSession POSTs a transcode start and returns the created session.
func startSession(t *testing.T, h *NativeTranscodeHandler, store *transcode.SessionStore,
	body transcodeStartRequest) *transcode.Session {
	t.Helper()

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(raw))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: got %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	// respond.Success wraps the payload in a {"data": …} envelope.
	var resp struct {
		Data transcodeStartResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	sess, err := store.Get(context.Background(), resp.Data.SessionID)
	if err != nil {
		t.Fatalf("load session %s: %v", resp.Data.SessionID, err)
	}
	return sess
}

// TestStart_RemuxSessionAdvertisesSourceContainer pins the segment container a
// freshly created session advertises, BEFORE any worker has stamped it.
//
// The worker overwrites HEVCOutput/AV1Output via SetWorkerInfo once it knows
// which encoder it actually got, but the client can request the playlist before
// that write lands — so the value the API writes at creation has to stand on
// its own. It previously came from preferHEVC/preferAV1, which both carry
// `!body.VideoCopy` because they gate RE-ENCODE decisions. That made an HEVC
// remux advertise H.264 output, so the playlist handler waited for seg00000.ts
// while ffmpeg wrote seg00000.m4s, and the playlist never became ready.
func TestStart_RemuxSessionAdvertisesSourceContainer(t *testing.T) {
	for _, tc := range []struct {
		codec   string
		wantExt string
	}{
		{"hevc", ".m4s"},
		{"h265", ".m4s"},
		{"av1", ".m4s"},
		{"h264", ".ts"},
	} {
		t.Run(tc.codec, func(t *testing.T) {
			h, store := newTestHandlerWithCodec(t, tc.codec)
			// VideoCopy = remux: the video stream passes through untouched, so
			// the OUTPUT codec is the SOURCE codec no matter what the client
			// said it prefers.
			sess := startSession(t, h, store, transcodeStartRequest{VideoCopy: true})

			if got := sess.VideoOutput().SegExt(); got != tc.wantExt {
				t.Errorf("%s remux session advertises %q segments, want %q — "+
					"the playlist handler will wait for a file ffmpeg never writes",
					tc.codec, got, tc.wantExt)
			}
		})
	}
}

// TestStart_TranscodeSessionUsesClientPreference is the other half: on a real
// re-encode the output codec is NOT the source codec, and the session must
// follow what the client can decode rather than what the file happens to be.
// Without this, "fix the remux case" could be implemented as "always use the
// source codec" and nothing would catch it.
func TestStart_TranscodeSessionUsesClientPreference(t *testing.T) {
	// An HEVC source re-encoded for a client that cannot decode HEVC comes out
	// as H.264 in MPEG-TS, despite the source being HEVC.
	h, store := newTestHandlerWithCodec(t, "hevc")
	sess := startSession(t, h, store, transcodeStartRequest{Height: 720})
	if got := sess.VideoOutput().SegExt(); got != ".ts" {
		t.Errorf("H.264 re-encode of an HEVC source advertises %q, want \".ts\" — "+
			"the source codec must not leak into a re-encode's container", got)
	}

	// Same source, same height, but a client that speaks HEVC: source
	// preservation kicks in and the output really is HEVC → fMP4.
	hevcYes := true
	h2, store2 := newTestHandlerWithCodec(t, "hevc")
	sess2 := startSession(t, h2, store2, transcodeStartRequest{Height: 720, SupportsHEVC: &hevcYes})
	if got := sess2.VideoOutput().SegExt(); got != ".m4s" {
		t.Errorf("HEVC re-encode advertises %q, want \".m4s\"", got)
	}
}
