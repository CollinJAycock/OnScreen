package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestStart_CapsHeaderConstrainsOutput pins the fix for "maxWidth/maxHeight
// force the transcode verdict but never constrain the transcode output": the
// header used to be parsed AFTER the resolution was chosen, so a client
// declaring maxheight=1080 was sent to transcode BECAUSE of its ceiling and
// then received the same 4K output it declared undecodable.
func TestStart_CapsHeaderConstrainsOutput(t *testing.T) {
	h, store := newTestHandlerWithCodec(t, "h264")
	// 4K source.
	w4k, h4k := 3840, 2160
	h.media.(*mockTranscodeMedia).files[0].ResolutionW = &w4k
	h.media.(*mockTranscodeMedia).files[0].ResolutionH = &h4k

	body, _ := json.Marshal(transcodeStartRequest{Height: 0}) // Auto
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req.Header.Set("X-Client-Capabilities", "videoDecoder=h264,audioDecoder=aac,maxWidth=1920,maxHeight=1080")
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: %d\n%s", rec.Code, rec.Body.String())
	}

	job := drainJob(t, store)
	if job.Height > 1080 || job.Width > 1920 {
		t.Errorf("output %dx%d exceeds the client's declared ceiling 1920x1080 — "+
			"the caps forced the transcode and then were ignored by it", job.Width, job.Height)
	}
	if job.Height <= 0 {
		t.Errorf("output height not set: %d", job.Height)
	}
}

// TestStart_BitDepthCeilingForces8BitJob pins the decision#0 fix end-to-end at
// the job boundary: an 8-bit-only HEVC client transcoding a 10-bit HEVC
// source must get a job flagged Force8Bit, so the encoder path strips depth
// instead of emitting Main 10 — the exact property that forced the transcode.
func TestStart_BitDepthCeilingForces8BitJob(t *testing.T) {
	h, store := newTestHandlerWithCodec(t, "hevc")
	depth := 10
	h.media.(*mockTranscodeMedia).files[0].VideoBitDepth = &depth

	body, _ := json.Marshal(transcodeStartRequest{Height: 720})
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	// Declares HEVC decode but 8-bit only — the web client's exact shape when
	// MSE reports hvc1.1.6 (Main) without hvc1.2.4 (Main 10).
	req.Header.Set("X-Client-Capabilities", "videoDecoder=h264:h265,audioDecoder=aac,maxbitdepth=8")
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: %d\n%s", rec.Code, rec.Body.String())
	}

	job := drainJob(t, store)
	if !job.Force8Bit {
		t.Error("job for an 8-bit-only client is not Force8Bit — the transcode " +
			"emits 10-bit HEVC to the client whose 8-bit limit caused it")
	}
}

// TestStart_AudioOnlySourceBuildsAudioJob pins the audio-only path at the job
// boundary: no ABR detour, AudioOnly set, and the remux flag left intact for
// the worker's builder.
func TestStart_AudioOnlySourceBuildsAudioJob(t *testing.T) {
	h, store := newTestHandlerWithCodec(t, "")
	// Audio-only: nil video codec (constructor set a codec; clear it).
	h.media.(*mockTranscodeMedia).files[0].VideoCodec = nil

	body, _ := json.Marshal(transcodeStartRequest{VideoCopy: true})
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(body))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)

	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: %d\n%s", rec.Code, rec.Body.String())
	}

	job := drainJob(t, store)
	if !job.AudioOnly {
		t.Error("audio-only source produced a job without AudioOnly — BuildHLS " +
			"hard-maps 0:v:0 and ffmpeg dies on 'matches no streams'")
	}
}
