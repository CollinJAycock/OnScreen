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

// ── Receiver encode: E-AC-3 / AC-3 instead of AAC ───────────────────────────

// A fleet whose workers know the receiver encode.
var dolbyEncodeFeatures = append([]string{transcode.WorkerFeatureAudioDolbyEncode}, allWorkerFeatures...)

// A TrueHD 7.1 film (1917) on a client that bitstreams Dolby to a receiver
// but asked for MPEG-TS segments (Roku): TrueHD can't be copied into TS, so
// the remux used to encode AAC and the receiver got no surround. It now gets
// 5.1 E-AC-3 (or AC-3) at 640 kb/s, still in TS.
func TestStart_ReceiverAudioEncode(t *testing.T) {
	const base = "videoDecoder=h264,audioDecoder=aac:ac3:eac3,protocols=ts,maxAudioChannels=2"
	cases := []struct {
		name      string
		video     string // "" = audio-only source
		audio     string
		channels  int
		caps      string
		features  []string
		container string
		videoCopy bool
		want      string // job AudioCodec
	}{
		{"truehd 7.1 remux into TS → eac3", "h264", "truehd", 8, base + ",audioPassthrough=ac3:eac3",
			dolbyEncodeFeatures, "ts", true, "eac3"},
		{"only ac3 passed through → ac3", "h264", "truehd", 8, base + ",audioPassthrough=ac3",
			dolbyEncodeFeatures, "ts", true, "ac3"},
		{"dts 5.1, client passes only eac3 → eac3", "h264", "dts", 6, base + ",audioPassthrough=eac3",
			dolbyEncodeFeatures, "", true, "eac3"},
		{"re-encode, truehd into TS → eac3", "vc1", "truehd", 8, base + ",audioPassthrough=ac3:eac3",
			dolbyEncodeFeatures, "ts", false, "eac3"},
		{"stereo source stays aac", "h264", "flac", 2, base + ",audioPassthrough=ac3:eac3",
			dolbyEncodeFeatures, "ts", true, "aac"},
		{"old fleet stays aac", "h264", "truehd", 8, base + ",audioPassthrough=ac3:eac3",
			allWorkerFeatures, "ts", true, "aac"},
		{"no audioPassthrough stays aac", "h264", "truehd", 8, base,
			dolbyEncodeFeatures, "ts", true, "aac"},
		{"truehd passthrough into fMP4 still copies", "h264", "truehd", 8,
			base + ",audioPassthrough=ac3:eac3:truehd", dolbyEncodeFeatures, "", true, "copy"},
		{"eac3 source still copies", "h264", "eac3", 6, base + ",audioPassthrough=ac3:eac3",
			dolbyEncodeFeatures, "ts", true, "copy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			itemID, user := uuid.New(), uuid.New()
			file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/1917.mkv",
				VideoCodec: strPtr(tc.video), AudioCodec: strPtr(tc.audio), Container: strPtr("matroska"),
				AudioStreams: []byte(`[{"index":1,"codec":"` + tc.audio + `","channels":` + strconv.Itoa(tc.channels) + `}]`),
				ResolutionW:  ip(1920), ResolutionH: ip(1080)}
			h, store := newStartHandler(t, file)
			registerWorker(t, store, tc.features)

			rec := startAs(t, h, itemID, user, "192.0.2.10:1", tc.caps,
				transcodeStartRequest{SegmentContainer: tc.container, VideoCopy: tc.videoCopy})
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			job := drainJob(t, store)
			if (job.Encoder == "copy") != tc.videoCopy {
				t.Fatalf("encoder %q, want videoCopy=%v", job.Encoder, tc.videoCopy)
			}
			if job.AudioCodec != tc.want {
				t.Fatalf("job audio %q, want %q", job.AudioCodec, tc.want)
			}
			s := onlySession(t, store)
			if !transcode.IsDolbyEncode(tc.want) {
				if tc.want == "aac" && (s.OutputAudioPassthrough || s.OutputAudioCodec != "aac") {
					t.Errorf("Now Playing %q passthrough=%v for an AAC encode", s.OutputAudioCodec, s.OutputAudioPassthrough)
				}
				return
			}
			if job.AudioChannels != 6 || job.AudioBitrateKbps != transcode.DolbyEncodeKbps {
				t.Errorf("job %d ch @%d kbps, want 6 @640", job.AudioChannels, job.AudioBitrateKbps)
			}
			if job.AudioPassthrough || job.ForcesFMP4() || s.FMP4Segments {
				t.Errorf("passthrough=%v fmp4=%v session fmp4=%v: the encode needs no fMP4",
					job.AudioPassthrough, job.ForcesFMP4(), s.FMP4Segments)
			}
			if tc.container == "ts" && (!job.TSSegments || s.VideoOutput().SegExt() != ".ts") {
				t.Errorf("TS asked for: job ts=%v ext=%s", job.TSSegments, s.VideoOutput().SegExt())
			}
			if s.OutputAudioCodec != tc.want || s.OutputAudioChannels != 6 || !s.OutputAudioPassthrough {
				t.Errorf("Now Playing %q/%d passthrough=%v, want %s/6/true",
					s.OutputAudioCodec, s.OutputAudioChannels, s.OutputAudioPassthrough, tc.want)
			}
			if !strings.Contains(strings.Join(s.TranscodeReasons, "|"), "for the receiver") {
				t.Errorf("reasons %v lack the receiver line", s.TranscodeReasons)
			}
			if out := sessionOutput(&s, &file, nil); out.AudioCodec != tc.want || !out.AudioPassthrough {
				t.Errorf("Now Playing output %+v", out)
			}
		})
	}
}

// An audio-only multichannel source (music) keeps its own path.
func TestStart_ReceiverAudioEncodeSkipsAudioOnly(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/album.flac",
		AudioCodec: strPtr("flac"), Container: strPtr("flac"),
		AudioStreams: []byte(`[{"index":0,"codec":"flac","channels":6}]`)}
	h, store := newStartHandler(t, file)
	registerWorker(t, store, dolbyEncodeFeatures)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"audioDecoder=aac:ac3:eac3,protocols=ts,audioPassthrough=ac3:eac3", transcodeStartRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if job := drainJob(t, store); transcode.IsDolbyEncode(job.AudioCodec) {
		t.Errorf("audio-only source encoded to %q", job.AudioCodec)
	}
}

// Under the user's bitrate cap the remux keeps AAC, as the passthrough copy
// does.
func TestStart_ReceiverAudioEncodeBitrateLimited(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/1917.mkv",
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("truehd"), Container: strPtr("matroska"),
		AudioStreams: []byte(`[{"index":1,"codec":"truehd","channels":8}]`),
		ResolutionW:  ip(1920), ResolutionH: ip(1080)}
	h, store := newStartHandler(t, file)
	h.WithUserCaps(staticCaps{BitrateKbps: 40000})
	registerWorker(t, store, dolbyEncodeFeatures)
	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264,audioDecoder=aac,protocols=ts,audioPassthrough=ac3:eac3",
		transcodeStartRequest{SegmentContainer: "ts", VideoCopy: true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if job := drainJob(t, store); job.AudioCodec != "aac" {
		t.Errorf("bitrate-limited job audio %q, want aac", job.AudioCodec)
	}
}

// The decision on its own, bitrate limit included.
func TestReceiverAudioEncode(t *testing.T) {
	both := transcode.ParseCapabilities("audioPassthrough=ac3:eac3")
	ac3 := transcode.ParseCapabilities("audioPassthrough=ac3:truehd")
	none := transcode.ParseCapabilities("audioDecoder=aac:ac3:eac3")
	yes := func() bool { return true }
	no := func() bool { return false }
	cases := []struct {
		name      string
		caps      transcode.ClientCapabilities
		ch        int
		audioOnly bool
		limited   bool
		fleet     func() bool
		codec     string
		channels  int
	}{
		{"7.1 → eac3 5.1", both, 8, false, false, yes, "eac3", 6},
		{"5.1 → eac3 5.1", both, 6, false, false, yes, "eac3", 6},
		{"only ac3 listed", ac3, 8, false, false, yes, "ac3", 6},
		{"3.0 keeps its layout", both, 3, false, false, yes, "eac3", 3},
		{"stereo stays aac", both, 2, false, false, yes, "", 0},
		{"unknown layout stays aac", both, 0, false, false, yes, "", 0},
		{"bitrate limited stays aac", both, 8, false, true, yes, "", 0},
		{"audio-only stays", both, 6, true, false, yes, "", 0},
		{"old fleet stays aac", both, 8, false, false, no, "", 0},
		{"no audioPassthrough stays aac", none, 8, false, false, yes, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codec, ch := receiverAudioEncode(tc.caps, tc.ch, tc.audioOnly, tc.limited, tc.fleet)
			if codec != tc.codec || ch != tc.channels {
				t.Errorf("got %q/%d, want %q/%d", codec, ch, tc.codec, tc.channels)
			}
		})
	}
}

// An HEVC ladder whose rungs encode E-AC-3 for the receiver: every rung job
// says so, the master's CODECS names ec-3 and BANDWIDTH counts 640 kb/s.
func TestStart_ReceiverAudioEncodeABRLadder(t *testing.T) {
	itemID, user := uuid.New(), uuid.New()
	file := media.File{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/x.mkv",
		VideoCodec: strPtr("hevc"), AudioCodec: strPtr("truehd"), Container: strPtr("matroska"),
		AudioStreams: []byte(`[{"index":1,"codec":"truehd","channels":8}]`),
		ResolutionW:  ip(3840), ResolutionH: ip(2160), DurationMS: i64(2 * 3600 * 1000)}
	h, store := newStartHandler(t, file)
	h.cfg.TranscodeABR = true
	if err := store.RegisterWorker(context.Background(), transcode.WorkerRegistration{
		ID: "w-hevc", Capabilities: []string{"libx264", "hevc_nvenc"}, MaxSessions: 8,
		Features: dolbyEncodeFeatures, RegisteredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// hevcLevel=4.1 forces the re-encode (the source is 4K).
	rec := startAs(t, h, itemID, user, "192.0.2.10:1",
		"videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:mkv,maxWidth=3840,maxHeight=2160,"+
			"hevcLevel=4.1,audioPassthrough=ac3:eac3", transcodeStartRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	parent := onlySession(t, store)
	if !parent.ABR || !parent.HEVCOutput {
		t.Fatalf("want an HEVC ladder, got abr=%v hevc=%v", parent.ABR, parent.HEVCOutput)
	}
	if parent.AudioEncodeCodec != "eac3" || parent.AudioChannels != 6 || parent.AudioBitrateKbps != 640 ||
		parent.AudioPassthroughCodec != "" || parent.FMP4Segments != false {
		t.Fatalf("parent encode=%q ch=%d kbps=%d passthrough=%q fmp4=%v", parent.AudioEncodeCodec,
			parent.AudioChannels, parent.AudioBitrateKbps, parent.AudioPassthroughCodec, parent.FMP4Segments)
	}
	master := httptest.NewRecorder()
	h.serveABRMaster(master, httptest.NewRequest("GET", "/", nil), &parent, "tok")
	body := master.Body.String()
	if !strings.Contains(body, `CODECS="hvc1.1.6.L123.B0,ec-3"`) {
		t.Errorf("master CODECS should name E-AC-3:\n%s", body)
	}
	top := parent.ABRRenditions[0]
	if want := (top.BitrateKbps + transcode.DolbyEncodeKbps) * 1000; !strings.Contains(body, "BANDWIDTH="+strconv.Itoa(want)) {
		t.Errorf("master BANDWIDTH should count 640 kb/s of audio (%d):\n%s", want, body)
	}
	for _, rung := range parent.ABRRenditions[:2] {
		h.ensureRungChild(context.Background(), &parent, rung, abrChildID(parent.ID, rung.Label), 0, false)
		job := drainJob(t, store)
		if job.AudioCodec != "eac3" || job.AudioChannels != 6 || job.AudioBitrateKbps != 640 || job.AudioPassthrough {
			t.Errorf("rung %s: audio %q %dch @%d passthrough=%v", rung.Label, job.AudioCodec,
				job.AudioChannels, job.AudioBitrateKbps, job.AudioPassthrough)
		}
	}
}
