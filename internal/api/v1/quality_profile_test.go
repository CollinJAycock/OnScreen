package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// The viewer's own quality profile turns a direct play into a transcode on
// the home network too — unlike the admin's caps, which only bind remotely.
func TestDecision_QualityProfile(t *testing.T) {
	br := int64(25_000_000)
	w, h := 1920, 1080
	movie := media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &br, ResolutionW: &w, ResolutionH: &h,
	}
	const videoCaps = "videoDecoder=h264,audioDecoder=aac,protocols=mp4"
	run := func(file media.File, caps string, q QualityProfile) *httptest.ResponseRecorder {
		hd := newDecisionHandler(t, file).WithUserCaps(staticCaps{Quality: q})
		req := decisionRequest(t, caps, true)
		req.RemoteAddr = "192.168.1.50:5000"
		rec := httptest.NewRecorder()
		hd.Decision(rec, req)
		return rec
	}

	if rec := run(movie, videoCaps, QualityProfile{}); !bodyHas(rec, `"decision":"directPlay"`) {
		t.Errorf("no profile: want directPlay; body: %s", rec.Body.String())
	}
	rec := run(movie, videoCaps, QualityProfile{MaxVideoBitrateKbps: 8000})
	if !bodyHas(rec, `"decision":"transcode"`) || !bodyHas(rec, `"reason":"quality_profile"`) ||
		!bodyHas(rec, `"max_bitrate_kbps":8000`) {
		t.Errorf("video bitrate: want transcode + quality_profile + cap; body: %s", rec.Body.String())
	}
	if rec := run(movie, videoCaps, QualityProfile{MaxVideoBitrateKbps: 40000}); !bodyHas(rec, `"decision":"directPlay"`) {
		t.Errorf("under the bitrate: want directPlay; body: %s", rec.Body.String())
	}
	if rec := run(movie, videoCaps, QualityProfile{MaxVideoHeight: 720}); !bodyHas(rec, `"decision":"transcode"`) {
		t.Errorf("1080p source, 720 ceiling: want transcode; body: %s", rec.Body.String())
	}
	// The audio limit doesn't force a movie to transcode…
	if rec := run(movie, videoCaps, QualityProfile{MaxAudioBitrateKbps: 128}); !bodyHas(rec, `"decision":"directPlay"`) {
		t.Errorf("audio limit on a movie: want directPlay; body: %s", rec.Body.String())
	}
	// …but a lossless track over it does.
	flacBR := int64(1_100_000)
	track := media.File{AudioCodec: strPtr("flac"), Container: strPtr("flac"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &flacBR}
	const audioCaps = "audioDecoder=flac:aac,protocols=flac:mp4"
	if rec := run(track, audioCaps, QualityProfile{}); bodyHas(rec, `"decision":"transcode"`) {
		t.Errorf("flac, no profile: want a direct verdict; body: %s", rec.Body.String())
	}
	rec = run(track, audioCaps, QualityProfile{MaxAudioBitrateKbps: 320})
	if !bodyHas(rec, `"decision":"transcode"`) || !bodyHas(rec, `"reason":"quality_profile"`) {
		t.Errorf("flac over 320: want transcode + quality_profile; body: %s", rec.Body.String())
	}
}

// The play decision refuses a stream over an admin's concurrent-stream
// limit, counting a direct play on another device.
func TestDecision_StreamLimit(t *testing.T) {
	br := int64(5_000_000)
	file := media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &br,
	}
	tr := streaming.NewTracker()
	req := decisionRequest(t, "videoDecoder=h264,audioDecoder=aac,protocols=mp4", true)
	req.RemoteAddr = "192.168.1.50:5000"
	user := middleware.ClaimsFromContext(req.Context()).UserID
	tr.TouchFile(user, "192.168.1.60", uuid.New(), uuid.New(), "/m/other.mkv", "TV")

	limit := NewStreamLimit(staticCaps{ConcurrentStreams: 1}, fixedSessions(nil), tr)
	hd := newDecisionHandler(t, file).WithStreamLimit(limit)
	rec := httptest.NewRecorder()
	hd.Decision(rec, req)
	if rec.Code != http.StatusTooManyRequests || !bodyHas(rec, "TOO_MANY_SESSIONS") {
		t.Errorf("at the limit: got %d %s", rec.Code, rec.Body.String())
	}

	limit2 := NewStreamLimit(staticCaps{ConcurrentStreams: 2}, fixedSessions(nil), tr)
	hd2 := newDecisionHandler(t, file).WithStreamLimit(limit2)
	rec = httptest.NewRecorder()
	hd2.Decision(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("room for one more: got %d %s", rec.Code, rec.Body.String())
	}
}

func TestQualityProfileHelpers(t *testing.T) {
	caps := QualityProfile{MaxVideoHeight: 720}.applyTo(transcode.ClientCapabilities{MaxHeight: 2160})
	if caps.MaxHeight != 720 || caps.MaxWidth != 1280 {
		t.Errorf("applyTo: %dx%d, want a 1280x720 box", caps.MaxWidth, caps.MaxHeight)
	}
	caps = QualityProfile{MaxVideoHeight: 1080}.applyTo(transcode.ClientCapabilities{MaxHeight: 720, MaxWidth: 1280})
	if caps.MaxHeight != 720 || caps.MaxWidth != 1280 {
		t.Errorf("applyTo must never raise the client's own limits: %dx%d", caps.MaxWidth, caps.MaxHeight)
	}

	hevc := transcode.ClientCapabilities{SupportsHEVC: true}
	if h, a := (QualityProfile{PreferredVideoCodec: "h264"}).outputCodec(hevc, false, true, true); h || a {
		t.Error("h264 preference left HEVC/AV1 output on")
	}
	if h, _ := (QualityProfile{PreferredVideoCodec: "hevc"}).outputCodec(hevc, false, false, false); !h {
		t.Error("hevc preference didn't ask for HEVC on an HEVC client")
	}
	if h, _ := (QualityProfile{PreferredVideoCodec: "hevc"}).outputCodec(transcode.ClientCapabilities{}, false, false, false); h {
		t.Error("hevc preference asked for HEVC on a client that can't decode it")
	}
	if h, a := (QualityProfile{}).outputCodec(hevc, false, true, false); !h || a {
		t.Error("no preference changed the automatic choice")
	}

	for _, tc := range []struct{ cap, in, want int }{
		{0, 6, 6}, {96, 6, 2}, {192, 6, 2}, {256, 6, 6}, {96, 2, 2}, {320, 8, 8}, {256, 8, 2},
	} {
		if got := (QualityProfile{MaxAudioBitrateKbps: tc.cap}).audioChannels(tc.in); got != tc.want {
			t.Errorf("audioChannels(cap %d, %d ch) = %d, want %d", tc.cap, tc.in, got, tc.want)
		}
	}

	br := int64(20_000_000)
	movie := &media.File{VideoCodec: strPtr("h264"), Bitrate: &br}
	c := streamCap{Kbps: 12000, Remote: true, RemoteBinding: true}.withQuality(QualityProfile{MaxVideoBitrateKbps: 6000}, movie)
	if c.Kbps != 6000 || !c.QualityBinding || c.RemoteBinding {
		t.Errorf("withQuality: %+v", c)
	}
	c = streamCap{Kbps: 4000}.withQuality(QualityProfile{MaxVideoBitrateKbps: 6000}, movie)
	if c.Kbps != 4000 || c.QualityBinding {
		t.Errorf("withQuality raised a lower cap: %+v", c)
	}
}

// A limit too small to play anything is refused now that the profile is
// enforced; null and 0 mean no limit.
func TestSetQualityProfile_Validation(t *testing.T) {
	for body, want := range map[string]int{
		`{"max_video_height":720,"max_video_bitrate_kbps":4000,"max_audio_bitrate_kbps":192}`: http.StatusNoContent,
		`{"max_video_height":null,"max_video_bitrate_kbps":0}`:                                http.StatusNoContent,
		`{"max_video_height":50}`:         http.StatusBadRequest,
		`{"max_video_bitrate_kbps":-1}`:   http.StatusBadRequest,
		`{"max_audio_bitrate_kbps":8}`:    http.StatusBadRequest,
		`{"preferred_video_codec":"vp9"}`: http.StatusBadRequest,
	} {
		h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})
		req := httptest.NewRequest("PUT", "/api/v1/users/me/quality-profile", strings.NewReader(body))
		req = adminAuthedRequest(req, uuid.New())
		rec := httptest.NewRecorder()
		h.SetQualityProfile(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
}
