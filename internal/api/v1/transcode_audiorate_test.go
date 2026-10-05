package v1

import (
	"bytes"
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

// audio_rate speeds an audio-only source up on the server (Samsung TVs ignore
// playbackRate, so an audiobook at 1.5x needs the stream itself sped up). The
// job carries it and the response echoes it; a video source ignores it.
func TestStart_AudioRate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		video    *string
		rate     float64
		wantRate float64
	}{
		{"audiobook 1.5x", nil, 1.5, 1.5},
		{"audiobook clamped to 3x", nil, 5, 3},
		{"audiobook 1x is no rate", nil, 1, 0},
		{"video ignores it", strPtr("h264"), 1.5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := testvalkey.New(t)
			store := transcode.NewSessionStore(v)
			h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{
				item:  &media.Item{ID: uuid.New(), Type: "audiobook", Title: "Book"},
				files: []media.File{{ID: uuid.New(), FilePath: "/media/book.mp3", VideoCodec: tc.video, AudioCodec: strPtr("mp3")}},
			}, &config.Config{TranscodeMaxHeight: 2160}, slog.Default()).WithVerifySource(fakeSourceOK)

			raw, _ := json.Marshal(map[string]any{"audio_rate": tc.rate, "position_ms": 60000})
			req := httptest.NewRequest("POST", "/api/v1/items/x/transcode", bytes.NewReader(raw))
			req = withClaims(withChiParam(req, "id", uuid.New().String()))
			rec := httptest.NewRecorder()
			h.Start(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("Start: %d %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Data transcodeStartResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Data.AudioRate != tc.wantRate {
				t.Errorf("response audio_rate = %v, want %v", resp.Data.AudioRate, tc.wantRate)
			}
			if job := drainJob(t, store); job.AudioRate != tc.wantRate {
				t.Errorf("job AudioRate = %v, want %v", job.AudioRate, tc.wantRate)
			}
		})
	}
}
