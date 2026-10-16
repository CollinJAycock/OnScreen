package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// startResponse POSTs a transcode start (optionally with an
// X-Client-Capabilities header) and returns the decoded response.
func startResponse(t *testing.T, h *NativeTranscodeHandler, caps string, body transcodeStartRequest) transcodeStartResponse {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/items/"+uuid.New().String()+"/transcode", bytes.NewReader(raw))
	req = withChiParam(req, "id", uuid.New().String())
	req = withClaims(req)
	if caps != "" {
		req.Header.Set("X-Client-Capabilities", caps)
	}
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Start: got %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data transcodeStartResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	return resp.Data
}

// The start response names the segment container, because a Chromecast's
// Default Media Receiver has to be told it in the LOAD (hlsSegmentFormat /
// hlsVideoSegmentFormat) instead of reading it off the playlist. It has to
// be the container the session really writes: a remux keeps the source
// codec (HEVC/AV1 → fMP4, H.264 → MPEG-TS) and a re-encode follows the
// client, so it comes from the same VideoOutput the playlist handler reads.
func TestStart_ReportsSegmentFormat(t *testing.T) {
	// The Chromecast profiles the web sender and the phone send.
	const castHD = "videoDecoder=h264,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=1920,maxHeight=1080,maxAudioChannels=2,maxbitdepth=8,hdr=0"
	const cast4K = "videoDecoder=h264:h265,audioDecoder=aac:mp3,protocols=mp4:hls,maxWidth=3840,maxHeight=2160,maxAudioChannels=6,maxbitdepth=10,hdr=1"
	for _, tc := range []struct {
		name  string
		codec string
		caps  string
		body  transcodeStartRequest
		want  string
	}{
		{"h264 remux", "h264", castHD, transcodeStartRequest{VideoCopy: true}, "ts"},
		{"hevc remux", "hevc", cast4K, transcodeStartRequest{VideoCopy: true}, "fmp4"},
		{"av1 remux", "av1", "videoDecoder=h264:av1", transcodeStartRequest{VideoCopy: true}, "fmp4"},
		// The HD profile can't take HEVC: the copy is refused and the
		// re-encode is H.264 in MPEG-TS.
		{"hevc for an HD cast", "hevc", castHD, transcodeStartRequest{VideoCopy: true}, "ts"},
		{"hevc re-encode, no hevc client", "hevc", "", transcodeStartRequest{Height: 720}, "ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHandlerWithCodec(t, tc.codec)
			resp := startResponse(t, h, tc.caps, tc.body)
			if resp.SegmentFormat != tc.want {
				t.Errorf("segment_format = %q, want %q", resp.SegmentFormat, tc.want)
			}
		})
	}
}

// An ABR ladder's container is fixed by the ladder codec for its whole life
// (rung children keep it through an encoder fallback), so the start response
// can name it exactly.
func TestStartABR_ReportsSegmentFormat(t *testing.T) {
	for _, tc := range []struct {
		codec string
		want  string
	}{
		{transcode.LadderH264, "ts"},
		{transcode.LadderHEVC, "fmp4"},
		{transcode.LadderAV1, "fmp4"},
	} {
		t.Run(tc.codec, func(t *testing.T) {
			h, _ := newTestHandler(t)
			durationMS := int64(60 * 60 * 1000)
			file := &media.File{ID: uuid.New(), FilePath: "/media/test.mkv", DurationMS: &durationMS}
			ladder := []transcode.Rendition{{Label: "720p", Width: 1280, Height: 720, BitrateKbps: 3000}}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/items/x/transcode", nil)
			h.startABR(rec, req, transcode.NewSessionID(), "tok", "", uuid.New(), uuid.New(),
				file, ladder, 0, 2, false, tc.codec, 0, 5)
			if rec.Code != http.StatusOK {
				t.Fatalf("startABR status %d: %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Data transcodeStartResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Data.SegmentFormat != tc.want {
				t.Errorf("segment_format = %q, want %q", resp.Data.SegmentFormat, tc.want)
			}
		})
	}
}
