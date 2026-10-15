package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

func enforceMovie(codec string, w, h int, kbps int64) media.File {
	br := kbps * 1000
	return media.File{
		VideoCodec: strPtr(codec), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &br, ResolutionW: &w, ResolutionH: &h,
	}
}

// Start copies video only when the play decision would: a client asking to
// copy HEVC it declared it can't decode, or a picture above its ceiling,
// gets the re-encode.
func TestVideoCopyRefused(t *testing.T) {
	hevc4k := enforceMovie("hevc", 3840, 2160, 40_000)
	h264 := enforceMovie("h264", 1920, 1080, 8_000)
	server := transcode.ServerCaps{MaxHeight: 2160}
	h264Only := transcode.ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mp4:mpegts,maxWidth=3840,maxHeight=2160")
	hevcToo := transcode.ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:mpegts,maxWidth=3840,maxHeight=2160")

	if !videoCopyRefused(hevc4k, h264Only, true, QualityProfile{}, server) {
		t.Error("HEVC on an H.264-only client: want the copy refused")
	}
	if videoCopyRefused(hevc4k, hevcToo, true, QualityProfile{}, server) {
		t.Error("HEVC on an HEVC client: want the copy allowed")
	}
	if videoCopyRefused(h264, h264Only, true, QualityProfile{}, server) {
		t.Error("H.264 on an H.264 client: want the copy allowed")
	}
	// The viewer's ceiling reaches the decision through applyTo.
	if !videoCopyRefused(hevc4k, QualityProfile{MaxVideoHeight: 1080}.applyTo(hevcToo), true, QualityProfile{MaxVideoHeight: 1080}, server) {
		t.Error("4K source, 1080 ceiling: want the copy refused")
	}
	// Audio the client can't take is an audio-only conversion, not a reason
	// to re-encode the video.
	dts := h264
	dts.AudioCodec = strPtr("dts")
	dts.Container = strPtr("mkv")
	if videoCopyRefused(dts, h264Only, true, QualityProfile{}, server) {
		t.Error("H.264 + DTS: want the video copied (directStream)")
	}
	// No profile: only the viewer's resolution ceiling is held.
	if videoCopyRefused(hevc4k, transcode.ClientCapabilities{}, false, QualityProfile{}, server) {
		t.Error("no profile, no ceiling: want the copy allowed")
	}
	if !videoCopyRefused(hevc4k, transcode.ClientCapabilities{}, false, QualityProfile{MaxVideoHeight: 1080}, server) {
		t.Error("no profile, 1080 ceiling: want the copy refused")
	}
}

func TestQualityProfile_overResolution(t *testing.T) {
	scope := enforceMovie("h264", 1280, 536, 4000)
	full := enforceMovie("h264", 1920, 1080, 8000)
	q := QualityProfile{MaxVideoHeight: 720}
	if q.overResolution(&scope) {
		t.Error("1280x536 fits a 720p box")
	}
	if !q.overResolution(&full) {
		t.Error("1080p is over a 720 ceiling")
	}
	if (QualityProfile{}).overResolution(&full) {
		t.Error("no ceiling: never over")
	}
}

func gateRequest(remote string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/media/stream/x", nil)
	r.RemoteAddr = remote
	return r
}

func TestDirectPlayGate(t *testing.T) {
	user := uuid.New()
	movie := enforceMovie("h264", 1920, 1080, 20_000)
	const lan, wan = "192.168.1.50:5000", "203.0.113.9:5000"

	block := func(g *DirectPlayGate, file media.File, remote string) (bool, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		return g.Blocks(rec, gateRequest(remote), user, &file), rec
	}

	open := NewDirectPlayGate(staticCaps{}, dlPolicy{})
	if b, _ := block(open, movie, wan); b {
		t.Error("no limits: want served")
	}

	// The internet limit binds a remote client, not a LAN one.
	capped := NewDirectPlayGate(staticCaps{}, dlPolicy{p: RemoteStreamPolicy{MaxBitrateKbps: 8000}})
	if b, _ := block(capped, movie, lan); b {
		t.Error("internet limit, LAN client: want served")
	}
	b, rec := block(capped, movie, wan)
	if !b || rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), codeTranscodeRequired) {
		t.Errorf("internet limit, remote client: want 409 %s; got %d %s", codeTranscodeRequired, rec.Code, rec.Body.String())
	}

	// The viewer's own profile binds on any network: bitrate and resolution.
	if b, _ := block(NewDirectPlayGate(staticCaps{Quality: QualityProfile{MaxVideoBitrateKbps: 8000}}, nil), movie, lan); !b {
		t.Error("over the profile's video bitrate on the LAN: want refused")
	}
	if b, _ := block(NewDirectPlayGate(staticCaps{Quality: QualityProfile{MaxVideoHeight: 720}}, nil), movie, lan); !b {
		t.Error("over the profile's resolution on the LAN: want refused")
	}

	// Music and books stream as-is whatever the profile says.
	flacBR := int64(1_100_000)
	track := media.File{AudioCodec: strPtr("flac"), Container: strPtr("flac"), Bitrate: &flacBR}
	strict := NewDirectPlayGate(staticCaps{Quality: QualityProfile{MaxAudioBitrateKbps: 128, MaxVideoHeight: 480}},
		dlPolicy{p: RemoteStreamPolicy{MaxBitrateKbps: 100}})
	if b, _ := block(strict, track, wan); b {
		t.Error("audio-only file: want served")
	}

	// A damaged video is refused for everyone.
	damaged := movie
	damaged.IntegrityStatus = media.IntegrityDamaged
	b, rec = block(open, damaged, lan)
	if !b || rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "FILE_DAMAGED") {
		t.Errorf("damaged: want 422 FILE_DAMAGED; got %d %s", rec.Code, rec.Body.String())
	}

	// A nil gate (not wired) serves everything.
	var none *DirectPlayGate
	if none.Blocks(httptest.NewRecorder(), gateRequest(wan), user, &damaged) {
		t.Error("nil gate: want served")
	}
}

// countingCaps counts the caps reads the gate makes.
type countingCaps struct {
	n    int
	caps StreamCaps
}

func (c *countingCaps) GetStreamCaps(_ context.Context, _ uuid.UUID) (StreamCaps, error) {
	c.n++
	return c.caps, nil
}

// A range-heavy direct play reads the user's limits once per gateMemoTTL.
func TestDirectPlayGate_Memo(t *testing.T) {
	cc := &countingCaps{}
	g := NewDirectPlayGate(cc, nil)
	now := time.Unix(1_700_000_000, 0)
	g.nowFunc = func() time.Time { return now }
	user := uuid.New()
	movie := enforceMovie("h264", 1920, 1080, 20_000)
	for range 5 {
		g.Blocks(httptest.NewRecorder(), gateRequest("192.168.1.50:5000"), user, &movie)
	}
	if cc.n != 1 {
		t.Errorf("5 requests inside the TTL: want 1 caps read, got %d", cc.n)
	}
	now = now.Add(gateMemoTTL + time.Second)
	g.Blocks(httptest.NewRecorder(), gateRequest("192.168.1.50:5000"), user, &movie)
	if cc.n != 2 {
		t.Errorf("after the TTL: want a fresh read, got %d reads", cc.n)
	}
}
