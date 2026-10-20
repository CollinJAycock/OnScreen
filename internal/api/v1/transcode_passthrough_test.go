package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── Audio passthrough on a re-encode (v2.7) ─────────────────────────────────

// A VC-1 Blu-ray rip (the video must be re-encoded) with a 7.1 track, on an
// Android TV that bitstreams to a receiver: the track is copied, TrueHD/DTS
// into fMP4 on a worker that has the feature. Each precondition missing
// leaves the AAC encode.
func TestStart_AudioPassthroughReencode(t *testing.T) {
	const base = "videoDecoder=h264,audioDecoder=aac,protocols=mp4:mkv:ts,maxAudioChannels=6"
	const passthrough = base + ",audioPassthrough=ac3:eac3:truehd:dts"
	cases := []struct {
		name      string
		audio     string
		caps      string
		features  []string
		container string // segment_container
		wantCopy  bool
		wantFMP4  bool
	}{
		{"truehd copied into fMP4", "truehd", passthrough, allWorkerFeatures, "", true, true},
		{"dts copied into fMP4", "dts", passthrough, allWorkerFeatures, "", true, true},
		{"eac3 copied into TS", "eac3", passthrough, nil, "", true, false},
		{"ac3 copied into TS segments a client asked for", "ac3", passthrough, nil, "ts", true, false},
		{"no audioPassthrough key: AAC", "truehd", base, allWorkerFeatures, "", false, false},
		{"codec not listed: AAC", "dts", base + ",audioPassthrough=ac3", allWorkerFeatures, "", false, false},
		{"old fleet: TrueHD is AAC", "truehd", passthrough, nil, "", false, false},
		{"client asked for TS: TrueHD is AAC", "truehd", passthrough, allWorkerFeatures, "ts", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			itemID, user := uuid.New(), uuid.New()
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
				VideoCodec: strPtr("vc1"), AudioCodec: strPtr(tc.audio), Container: strPtr("matroska"),
				AudioStreams: []byte(`[{"index":1,"codec":"` + tc.audio + `","channels":8}]`),
				ResolutionW:  ip(1920), ResolutionH: ip(1080)}
			h, store := newStartHandler(t, file)
			registerWorker(t, store, tc.features)

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", tc.caps,
				transcodeStartRequest{SegmentContainer: tc.container})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			job := drainJob(t, store)
			if job.Encoder == "copy" || job.Decision != "transcode" {
				t.Fatalf("VC-1 must be re-encoded: encoder %q decision %q", job.Encoder, job.Decision)
			}
			s := onlySession(t, store)
			if !tc.wantCopy {
				if job.AudioCodec != "aac" || job.AudioPassthrough || job.ForcesFMP4() || s.FMP4Segments {
					t.Errorf("job audio=%q passthrough=%v fmp4=%v session fmp4=%v, want the AAC encode",
						job.AudioCodec, job.AudioPassthrough, job.ForcesFMP4(), s.FMP4Segments)
				}
				if s.OutputAudioPassthrough {
					t.Error("Now Playing claims passthrough for an AAC encode")
				}
				return
			}
			if job.AudioCodec != "copy" || job.AudioPassthrough != tc.wantFMP4 || job.ForcesFMP4() != tc.wantFMP4 {
				t.Errorf("job audio=%q passthrough=%v fmp4=%v, want copy (fmp4=%v)",
					job.AudioCodec, job.AudioPassthrough, job.ForcesFMP4(), tc.wantFMP4)
			}
			if s.FMP4Segments != tc.wantFMP4 || (s.VideoOutput().SegExt() == ".m4s") != tc.wantFMP4 {
				t.Errorf("session FMP4Segments=%v ext=%s, want fmp4=%v", s.FMP4Segments, s.VideoOutput().SegExt(), tc.wantFMP4)
			}
			if s.OutputAudioCodec != "copy" || s.OutputAudioChannels != 8 || !s.OutputAudioPassthrough {
				t.Errorf("Now Playing audio %q/%d passthrough=%v, want copy/8/true",
					s.OutputAudioCodec, s.OutputAudioChannels, s.OutputAudioPassthrough)
			}
			if !strings.Contains(strings.Join(s.TranscodeReasons, "|"), "audio passed through to the receiver") {
				t.Errorf("reasons %v lack the passthrough line", s.TranscodeReasons)
			}
			out := sessionOutput(&s, &file, nil)
			if out.AudioCodec != tc.audio || !out.AudioPassthrough || out.VideoCodec != "h264" {
				t.Errorf("Now Playing output %+v", out)
			}
		})
	}
}

// The re-encode passthrough rules on their own: a bitrate limit or a
// server-side speed change keeps AAC whatever the client declares.
func TestTranscodeAudioPassthrough(t *testing.T) {
	caps := transcode.ParseCapabilities("audioPassthrough=ac3:eac3:truehd:dts")
	yes := func() bool { return true }
	no := func() bool { return false }
	cases := []struct {
		name           string
		alias          string
		audioOnly, ts  bool
		limited        bool
		rate           float64
		fleet          func() bool
		wantCopy, fmp4 bool
	}{
		{"ac3", "ac3", false, false, false, 0, no, true, false},
		{"eac3 in TS", "eac3", false, true, false, 0, no, true, false},
		{"truehd", "truehd", false, false, false, 0, yes, true, true},
		{"dts-hd alias", "dts-hd", false, false, false, 0, yes, true, true},
		{"truehd, no worker", "truehd", false, false, false, 0, no, false, false},
		{"truehd in TS", "truehd", false, true, false, 0, yes, false, false},
		{"bitrate limited", "eac3", false, false, true, 0, yes, false, false},
		{"audio-only source", "ac3", true, false, false, 0, yes, false, false},
		{"speed change", "ac3", false, false, false, 1.5, yes, false, false},
		{"normal speed is no change", "ac3", false, false, false, 1, yes, true, false},
		{"aac isn't passthrough", "aac", false, false, false, 0, yes, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f := transcodeAudioPassthrough(tc.alias, caps, tc.audioOnly, tc.ts, tc.limited, tc.rate, tc.fleet)
			if c != tc.wantCopy || f != tc.fmp4 {
				t.Errorf("got copy=%v fmp4=%v, want %v/%v", c, f, tc.wantCopy, tc.fmp4)
			}
		})
	}
}

// The ABR ladder: every rung child copies the same passed-through track
// (TrueHD makes the H.264 ladder fMP4), and the master playlist's BANDWIDTH
// covers it.
func TestStart_AudioPassthroughABRLadder(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("vc1"), AudioCodec: strPtr("truehd"), Container: strPtr("matroska"),
		AudioStreams: []byte(`[{"index":1,"codec":"truehd","channels":8}]`),
		ResolutionW:  ip(1920), ResolutionH: ip(1080), DurationMS: i64(2 * 3600 * 1000)}
	h, store := newStartHandler(t, file)
	h.cfg.TranscodeABR = true
	registerWorker(t, store, allWorkerFeatures)

	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264,audioDecoder=aac,protocols=mp4:mkv,audioPassthrough=truehd", transcodeStartRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"segment_format":"fmp4"`) {
		t.Errorf("a TrueHD ladder must announce fMP4: %s", rec.Body.String())
	}
	parent := onlySession(t, store)
	if !parent.ABR || parent.AudioPassthroughCodec != "truehd" || !parent.FMP4Segments {
		t.Fatalf("parent abr=%v passthrough=%q fmp4=%v", parent.ABR, parent.AudioPassthroughCodec, parent.FMP4Segments)
	}

	master := httptest.NewRecorder()
	h.serveABRMaster(master, httptest.NewRequest("GET", "/", nil), &parent, "tok")
	top := parent.ABRRenditions[0]
	if want := (top.BitrateKbps + transcode.PassthroughAudioKbps("truehd")) * 1000; !strings.Contains(master.Body.String(),
		"BANDWIDTH="+strconv.Itoa(want)) {
		t.Errorf("master BANDWIDTH should carry the TrueHD estimate (%d):\n%s", want, master.Body.String())
	}

	for _, rung := range parent.ABRRenditions[:2] {
		childID := abrChildID(parent.ID, rung.Label)
		h.ensureRungChild(context.Background(), &parent, rung, childID, 0, false)
		job := drainJob(t, store)
		if job.AudioCodec != "copy" || !job.AudioPassthrough || !job.ForcesFMP4() {
			t.Errorf("rung %s: audio=%q passthrough=%v fmp4=%v, want the TrueHD copy in fMP4",
				rung.Label, job.AudioCodec, job.AudioPassthrough, job.ForcesFMP4())
		}
	}
}

// ── Video profile / level (v2.7) ────────────────────────────────────────────

// An H.264 High@5.1 1080p60 file on a client whose decoder stops at 4.1: the
// requested copy is refused, the re-encode is sized to fit 4.1 at 60 fps and
// told the level, and Now Playing says why.
func TestStart_VideoLevelCeiling(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mp4",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		VideoProfile: strPtr("High"), VideoLevel: ip(51), FrameRate: f64(60),
		ResolutionW: ip(1920), ResolutionH: ip(1080)}
	h, store := newStartHandler(t, file)
	registerWorker(t, store, nil)

	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264,audioDecoder=aac,protocols=mp4,h264Profile=high,h264Level=4.1",
		transcodeStartRequest{VideoCopy: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	job := drainJob(t, store)
	if job.Encoder == "copy" {
		t.Fatal("a level 5.1 source must not be copied to a 4.1 decoder")
	}
	if job.OutputLimits == nil || job.OutputLimits.H264Level != 41 || job.OutputLimits.FrameRate != 60 {
		t.Fatalf("job output limits %+v, want H.264 4.1 at 60 fps", job.OutputLimits)
	}
	if job.Height >= 1080 || !transcode.FitsLevel("h264", 41, job.Width, job.Height, 60) {
		t.Errorf("output %dx%d doesn't fit 4.1 at 60 fps", job.Width, job.Height)
	}
	if job.BitrateKbps > transcode.LevelMaxKbps("h264", 41) {
		t.Errorf("bitrate %d over the level's", job.BitrateKbps)
	}
	s := onlySession(t, store)
	if !strings.Contains(strings.Join(s.TranscodeReasons, "|"), "H.264 level 5.1 → client max 4.1") {
		t.Errorf("reasons %v", s.TranscodeReasons)
	}
}

// Back-compat: the same file with no profile/level keys is copied as before.
func TestStart_VideoLevelAbsentKeepsCopy(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mp4",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		VideoProfile: strPtr("High"), VideoLevel: ip(51), FrameRate: f64(60),
		ResolutionW: ip(1920), ResolutionH: ip(1080)}
	h, store := newStartHandler(t, file)
	registerWorker(t, store, nil)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1", "videoDecoder=h264,audioDecoder=aac,protocols=mp4",
		transcodeStartRequest{VideoCopy: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if job := drainJob(t, store); job.Encoder != "copy" || job.OutputLimits != nil {
		t.Errorf("encoder %q limits %+v, want an unlimited copy", job.Encoder, job.OutputLimits)
	}
}

// A 4K HEVC Main 10 file for a client that takes HEVC only up to level 4.1:
// the HEVC ladder tops out at 1080p, its CODECS say 4.1, and every rung is
// told the level. hevcProfile=main would additionally keep 10-bit off it.
func TestStart_VideoLevelABRLadder(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("hevc"), AudioCodec: strPtr("eac3"), Container: strPtr("matroska"),
		AudioStreams: []byte(`[{"index":1,"codec":"eac3","channels":6}]`),
		VideoProfile: strPtr("Main 10"), VideoLevel: ip(153), VideoBitDepth: ip(10), FrameRate: f64(23.976),
		ResolutionW: ip(3840), ResolutionH: ip(2160), DurationMS: i64(2 * 3600 * 1000)}
	h, store := newStartHandler(t, file)
	h.cfg.TranscodeABR = true
	if err := store.RegisterWorker(context.Background(), transcode.WorkerRegistration{
		ID: "w-hevc", Capabilities: []string{"libx264", "hevc_nvenc"}, MaxSessions: 8,
		Features: allWorkerFeatures, RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:mkv,maxWidth=3840,maxHeight=2160,maxbitdepth=10,"+
			"hevcProfile=main10,hevcLevel=4.1,audioPassthrough=eac3", transcodeStartRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	parent := onlySession(t, store)
	if !parent.ABR || !parent.HEVCOutput {
		t.Fatalf("want an HEVC ladder, got abr=%v hevc=%v", parent.ABR, parent.HEVCOutput)
	}
	if top := parent.ABRRenditions[0]; top.Height > 1080 {
		t.Errorf("top rung %s is over level 4.1", top.Label)
	}
	master := httptest.NewRecorder()
	h.serveABRMaster(master, httptest.NewRequest("GET", "/", nil), &parent, "tok")
	if !strings.Contains(master.Body.String(), `CODECS="hvc1.1.6.L123.B0,ec-3"`) {
		t.Errorf("master CODECS should be HEVC 4.1 + E-AC-3:\n%s", master.Body.String())
	}
	rung := parent.ABRRenditions[0]
	h.ensureRungChild(context.Background(), &parent, rung, abrChildID(parent.ID, rung.Label), 0, false)
	job := drainJob(t, store)
	if job.OutputLimits == nil || job.OutputLimits.HEVCLevel != 41 || job.AudioCodec != "copy" || job.AudioPassthrough {
		t.Errorf("rung job limits %+v audio %q passthrough %v", job.OutputLimits, job.AudioCodec, job.AudioPassthrough)
	}
}

func TestFitOutputLevel(t *testing.T) {
	// 4K scope source into H.264 4.1 at 24 fps: 1080-ish lines at its aspect.
	w, h, k := fitOutputLevel("h264", 41, 3840, 1606, 24, 3840, 1606, 40000)
	if !transcode.FitsLevel("h264", 41, w, h, 24) || h < 800 || k != transcode.LevelMaxKbps("h264", 41) {
		t.Errorf("got %dx%d @%d", w, h, k)
	}
	// Already within the level: unchanged.
	if w, h, k := fitOutputLevel("h264", 41, 1920, 1080, 24, 1280, 720, 4000); w != 1280 || h != 720 || k != 4000 {
		t.Errorf("a fitting output changed: %dx%d @%d", w, h, k)
	}
	if w, h, k := fitOutputLevel("h264", 0, 3840, 2160, 60, 3840, 2160, 40000); w != 3840 || h != 2160 || k != 40000 {
		t.Errorf("no level changed the output: %dx%d @%d", w, h, k)
	}
}

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
