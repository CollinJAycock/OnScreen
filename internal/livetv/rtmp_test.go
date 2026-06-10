package livetv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ── raw FLV body builders (the bytes a broadcaster sends per video/audio tag) ──

func avcSeqBody() []byte {
	// 0x17 = keyframe(1)<<4 | AVC(7); 0x00 = AVCPacketType SequenceHeader.
	return []byte{0x17, 0x00, 0x00, 0x00, 0x00, 0x01, 0x42, 0x00, 0x1e, 0xff}
}
func avcFrameBody(keyframe bool) []byte {
	b0 := byte(0x27) // interframe(2)<<4 | AVC(7)
	if keyframe {
		b0 = 0x17
	}
	return []byte{b0, 0x01, 0x00, 0x00, 0x00, 0xde, 0xad, 0xbe, 0xef}
}
func aacSeqBody() []byte {
	// 0xaf = AAC(10)<<4 | 44k/16bit/stereo; 0x00 = AACPacketType SequenceHeader.
	return []byte{0xaf, 0x00, 0x12, 0x10}
}
func aacFrameBody() []byte {
	return []byte{0xaf, 0x01, 0x01, 0x02, 0x03}
}

// enhanced-RTMP HEVC bodies: byte0 = 0x80(ex) | frameType<<4 | packetType,
// followed by the "hvc1" FourCC.
func hevcSeqBody() []byte {
	return append([]byte{0x90, 'h', 'v', 'c', '1'}, 0x01, 0x02, 0x03) // ft=1,pt=0(SeqStart)
}
func hevcFrameBody(keyframe bool) []byte {
	b0 := byte(0xa1) // ft=2(inter), pt=1(CodedFrames)
	if keyframe {
		b0 = 0x91 // ft=1(key), pt=1(CodedFrames)
	}
	return append([]byte{b0, 'h', 'v', 'c', '1'}, 0x00, 0x00, 0x00, 0xca, 0xfe)
}

func mkVideo(ts uint32, body []byte) *mediaTag {
	sh, kf := classifyVideo(body)
	kind := tagVideoData
	switch {
	case sh:
		kind = tagVideoSeqHeader
	case kf:
		kind = tagVideoKeyframe
	}
	return &mediaTag{tagType: flvTagVideo, timestamp: ts, data: body, kind: kind}
}
func mkAudio(ts uint32, body []byte) *mediaTag {
	kind := tagAudioData
	if audioIsSequenceHeader(body) {
		kind = tagAudioSeqHeader
	}
	return &mediaTag{tagType: flvTagAudio, timestamp: ts, data: body, kind: kind}
}

// ── minimal FLV parser (validates what the fan-out produced) ──

type parsedTag struct {
	tagType byte
	ts      uint32
	body    []byte
}

func parseFLV(r io.Reader, n int) ([]parsedTag, error) {
	hdr := make([]byte, 9)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	if string(hdr[0:3]) != "FLV" {
		return nil, fmt.Errorf("bad FLV signature %q", hdr[0:3])
	}
	prev := make([]byte, 4)
	if _, err := io.ReadFull(r, prev); err != nil { // PreviousTagSize0
		return nil, err
	}
	var out []parsedTag
	for i := 0; i < n; i++ {
		th := make([]byte, 11)
		if _, err := io.ReadFull(r, th); err != nil {
			return out, err
		}
		dataSize := int(th[1])<<16 | int(th[2])<<8 | int(th[3])
		ts := uint32(th[4])<<16 | uint32(th[5])<<8 | uint32(th[6]) | uint32(th[7])<<24
		body := make([]byte, dataSize)
		if _, err := io.ReadFull(r, body); err != nil {
			return out, err
		}
		if _, err := io.ReadFull(r, prev); err != nil {
			return out, err
		}
		out = append(out, parsedTag{tagType: th[0], ts: ts, body: body})
	}
	return out, nil
}

// ── classification ──

func TestClassifyVideo(t *testing.T) {
	cases := []struct {
		name            string
		body            []byte
		wantSeq, wantKf bool
		wantCodec       string
	}{
		{"avc seq header", avcSeqBody(), true, false, "h264"},
		{"avc keyframe", avcFrameBody(true), false, true, "h264"},
		{"avc interframe", avcFrameBody(false), false, false, "h264"},
		{"hevc seq header", hevcSeqBody(), true, false, "hvc1"},
		{"hevc keyframe", hevcFrameBody(true), false, true, "hvc1"},
		{"hevc interframe", hevcFrameBody(false), false, false, "hvc1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sh, kf := classifyVideo(c.body)
			if sh != c.wantSeq || kf != c.wantKf {
				t.Fatalf("classifyVideo: got seq=%v kf=%v, want seq=%v kf=%v", sh, kf, c.wantSeq, c.wantKf)
			}
			if got := videoCodecName(c.body); got != c.wantCodec {
				t.Fatalf("videoCodecName: got %q want %q", got, c.wantCodec)
			}
		})
	}
	if !audioIsSequenceHeader(aacSeqBody()) {
		t.Error("aac seq header not detected")
	}
	if audioIsSequenceHeader(aacFrameBody()) {
		t.Error("aac raw frame misdetected as seq header")
	}
}

// TestRTMPPublishLateJoin verifies a viewer attaching to an already-live
// broadcast first receives the cached sequence headers + last keyframe, then
// live tags, with timestamps rebased to start near zero — exercised against
// the raw-passthrough fan-out.
func TestRTMPPublishLateJoin(t *testing.T) {
	p := newRTMPPublish("key")

	// Broadcast already running, no viewer attached yet.
	p.publish(mkVideo(0, avcSeqBody()))
	p.publish(mkAudio(0, aacSeqBody()))
	p.publish(mkVideo(1000, avcFrameBody(true))) // keyframe at ts 1000

	rc, err := p.subscribe()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer rc.Close()

	// One live frame triggers the init burst (cached tags) then itself.
	p.publish(mkVideo(1040, avcFrameBody(false)))

	type result struct {
		tags []parsedTag
		err  error
	}
	done := make(chan result, 1)
	go func() {
		tags, err := parseFLV(rc, 4)
		done <- result{tags, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("parse FLV: %v", r.err)
		}
		tags := r.tags
		// 1. audio seq header (init order: script→audioSeq→videoSeq→keyframe).
		if tags[0].tagType != flvTagAudio {
			t.Fatalf("tag0: want audio, got type %d", tags[0].tagType)
		}
		// 2. video seq header.
		if sh, _ := classifyVideo(tags[1].body); tags[1].tagType != flvTagVideo || !sh {
			t.Fatalf("tag1: want video seq header, got type %d", tags[1].tagType)
		}
		// 3. last keyframe, rebased to ts 0.
		if _, kf := classifyVideo(tags[2].body); tags[2].tagType != flvTagVideo || !kf || tags[2].ts != 0 {
			t.Fatalf("tag2: want keyframe ts0, got type %d ts %d", tags[2].tagType, tags[2].ts)
		}
		// 4. live interframe, rebased to 40 (1040-1000).
		if tags[3].tagType != flvTagVideo || tags[3].ts != 40 {
			t.Fatalf("tag3: want interframe ts40, got type %d ts %d", tags[3].tagType, tags[3].ts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading FLV tags")
	}
}

func TestRTMPServerSubscribeOffline(t *testing.T) {
	s := NewRTMPServer(":0", testLogger())
	if _, err := s.Subscribe("not-live"); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("want ErrChannelNotFound for offline key, got %v", err)
	}
	if s.IsLive("not-live") {
		t.Fatal("IsLive should be false when nobody is broadcasting")
	}
}

func TestRTMPServerRegisterAuthorization(t *testing.T) {
	s := NewRTMPServer(":0", testLogger())

	if _, err := s.register("key"); !errors.Is(err, errRTMPUnauthorized) {
		t.Fatalf("want unauthorized with no authorizer, got %v", err)
	}
	s.SetAuthorizer(func(k string) bool { return k == "good" })
	if _, err := s.register("bad"); !errors.Is(err, errRTMPUnauthorized) {
		t.Fatalf("want unauthorized for bad key, got %v", err)
	}
	p, err := s.register("good")
	if err != nil {
		t.Fatalf("register good key: %v", err)
	}
	if !s.IsLive("good") {
		t.Fatal("IsLive should be true after register")
	}
	if _, err := s.register("good"); !errors.Is(err, errRTMPAlreadyLive) {
		t.Fatalf("want already-live for duplicate publish, got %v", err)
	}
	s.unregister("good", p)
	if s.IsLive("good") {
		t.Fatal("IsLive should be false after unregister")
	}
}

func TestRTMPDriverOpenStreamOffline(t *testing.T) {
	s := NewRTMPServer(":0", testLogger())
	d := NewRTMPDriver("My Stream", RTMPConfig{StreamKey: "abc"}, s)

	if d.Type() != TunerTypeRTMP {
		t.Fatalf("want type rtmp, got %v", d.Type())
	}
	chans, err := d.Discover(context.Background())
	if err != nil || len(chans) != 1 || chans[0].Number != rtmpChannelNumber || chans[0].Name != "My Stream" {
		t.Fatalf("discover: want one channel named 'My Stream', got %v err=%v", chans, err)
	}
	if _, err := d.OpenStream(context.Background(), rtmpChannelNumber); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("OpenStream offline: want ErrChannelNotFound, got %v", err)
	}
}

func TestRTMPFactoryRequiresStreamKey(t *testing.T) {
	f := RTMPFactory(NewRTMPServer(":0", testLogger()))
	if _, err := f("name", []byte(`{}`)); err == nil {
		t.Fatal("want error when stream_key missing")
	}
	d, err := f("name", []byte(`{"stream_key":"xyz"}`))
	if err != nil {
		t.Fatalf("factory with key: %v", err)
	}
	if d.(*RTMPDriver).StreamKey() != "xyz" {
		t.Fatalf("want stream key xyz, got %q", d.(*RTMPDriver).StreamKey())
	}
}

// TestRTMPEndToEndFFmpeg pushes a real clip with ffmpeg and verifies Subscribe
// yields a decodable FLV stream containing a video keyframe. Push codec is
// configurable via ONSCREEN_RTMP_E2E_PUSH (libx264 / hevc_nvenc / av1_nvenc /
// libsvtav1). Gated by ONSCREEN_RTMP_E2E.
func TestRTMPEndToEndFFmpeg(t *testing.T) {
	if os.Getenv("ONSCREEN_RTMP_E2E") == "" {
		t.Skip("set ONSCREEN_RTMP_E2E=1 to run the ffmpeg RTMP end-to-end test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}

	const key = "e2ekey"
	s := NewRTMPServer("127.0.0.1:0", testLogger())
	s.SetAuthorizer(func(k string) bool { return k == key })
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	push := exec.CommandContext(ctx, "ffmpeg", pushArgs(pushCodec(), "rtmp://"+s.Addr()+"/live/"+key)...)
	if err := push.Start(); err != nil {
		t.Fatalf("ffmpeg start: %v", err)
	}
	defer func() { _ = push.Process.Kill() }()

	deadline := time.Now().Add(10 * time.Second)
	for !s.IsLive(key) {
		if time.Now().After(deadline) {
			t.Fatal("broadcast never went live")
		}
		time.Sleep(50 * time.Millisecond)
	}

	rc, err := s.Subscribe(key)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer rc.Close()

	tags, err := parseFLV(rc, 40)
	if err != nil && len(tags) == 0 {
		t.Fatalf("parse FLV: %v", err)
	}
	sawKeyframe := false
	for _, tg := range tags {
		if tg.tagType == flvTagVideo {
			if _, kf := classifyVideo(tg.body); kf {
				sawKeyframe = true
				break
			}
		}
	}
	if !sawKeyframe {
		t.Fatalf("no video keyframe in %d parsed FLV tags", len(tags))
	}
}

// TestRTMPThroughFFmpegToHLS reproduces the full HLS proxy path: push with
// ffmpeg, Subscribe, then consume the FLV with `ffmpeg -f flv -i pipe:0` →
// HLS, asserting a playlist + segment appear. By default it runs both H.264
// (classic FLV) AND HEVC (enhanced-RTMP, software libx265) so the codec-
// agnostic ingest of enhanced-RTMP stays covered against regression. Set
// ONSCREEN_RTMP_E2E_PUSH to pin one push codec (e.g. hevc_nvenc / av1_nvenc /
// libsvtav1); the transcode target is ONSCREEN_RTMP_E2E_VENC (default
// libx264). Gated by ONSCREEN_RTMP_E2E.
func TestRTMPThroughFFmpegToHLS(t *testing.T) {
	if os.Getenv("ONSCREEN_RTMP_E2E") == "" {
		t.Skip("set ONSCREEN_RTMP_E2E=1 to run the ffmpeg→HLS end-to-end test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	codecs := []string{"libx264", "libx265"} // classic-FLV H.264 + enhanced-RTMP HEVC
	if c := os.Getenv("ONSCREEN_RTMP_E2E_PUSH"); c != "" {
		codecs = []string{c}
	}
	for _, push := range codecs {
		t.Run(push, func(t *testing.T) { rtmpThroughFFmpegToHLS(t, push) })
	}
}

// rtmpThroughFFmpegToHLS pushes a clip encoded with pushVenc into a fresh RTMP
// server, then transcodes the subscribed FLV to HLS, failing if no segment is
// produced.
func rtmpThroughFFmpegToHLS(t *testing.T, pushVenc string) {
	const key = "hlskey"
	s := NewRTMPServer("127.0.0.1:0", testLogger())
	s.SetAuthorizer(func(k string) bool { return k == key })
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	pushCtx, cancelPush := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancelPush()
	push := exec.CommandContext(pushCtx, "ffmpeg", pushArgs(pushVenc, "rtmp://"+s.Addr()+"/live/"+key)...)
	if err := push.Start(); err != nil {
		t.Fatalf("ffmpeg push start: %v", err)
	}
	defer func() { _ = push.Process.Kill() }()

	deadline := time.Now().Add(10 * time.Second)
	for !s.IsLive(key) {
		if time.Now().After(deadline) {
			t.Fatal("broadcast never went live")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Join mid-broadcast (the real-world case): headers come from the cache.
	time.Sleep(6 * time.Second)

	rc, err := s.Subscribe(key)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer rc.Close()

	dir := t.TempDir()
	playlist := dir + "/playlist.m3u8"
	consCtx, cancelCons := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCons()

	venc := os.Getenv("ONSCREEN_RTMP_E2E_VENC")
	if venc == "" {
		venc = "libx264"
	}
	// Mirror hls.go: force keyframes every 60 frames so the HLS muxer can cut.
	vargs := []string{"-c:v", venc, "-profile:v", "high", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0"}
	switch venc {
	case "libx264":
		vargs = append(vargs, "-preset", "veryfast", "-tune", "zerolatency")
	case "h264_nvenc":
		vargs = append(vargs, "-preset", "p4", "-tune", "ll", "-rc", "vbr",
			"-b:v", "6M", "-maxrate", "8M", "-bufsize", "8M", "-forced-idr", "1")
	}
	consArgs := []string{"-fflags", "+genpts+discardcorrupt", "-f", "flv", "-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0?"}
	consArgs = append(consArgs, vargs...)
	consArgs = append(consArgs, "-c:a", "aac",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "5",
		"-hls_flags", "delete_segments+omit_endlist", playlist)
	cons := exec.CommandContext(consCtx, "ffmpeg", consArgs...)
	cons.Stdin = rc
	if err := cons.Start(); err != nil {
		t.Fatalf("ffmpeg consume start: %v", err)
	}
	defer func() { _ = cons.Process.Kill() }()

	pd := time.Now().Add(22 * time.Second)
	for time.Now().Before(pd) {
		if data, err := os.ReadFile(playlist); err == nil && containsAny(string(data), ".ts") {
			return // success
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("HLS playlist with a segment was never produced (push=%s venc=%s)", pushVenc, venc)
}

func pushCodec() string {
	if c := os.Getenv("ONSCREEN_RTMP_E2E_PUSH"); c != "" {
		return c
	}
	return "libx264"
}

// pushArgs builds an ffmpeg command line that generates a 30s test pattern and
// pushes it to dst via RTMP with the given video codec (H.264, HEVC, or AV1 —
// HEVC/AV1 go out as enhanced-RTMP, which the server passes through).
func pushArgs(venc, dst string) []string {
	base := []string{
		"-re",
		"-f", "lavfi", "-i", "testsrc=duration=30:size=1280x720:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=30",
		"-pix_fmt", "yuv420p", "-g", "60",
	}
	switch venc {
	case "hevc_nvenc":
		base = append(base, "-c:v", "hevc_nvenc", "-preset", "p4")
	case "av1_nvenc":
		base = append(base, "-c:v", "av1_nvenc", "-preset", "p4")
	case "libsvtav1":
		base = append(base, "-c:v", "libsvtav1", "-preset", "8")
	case "libx265":
		// No -tag:v: the FLV/enhanced-RTMP muxer assigns the HEVC FourCC
		// itself and rejects an explicit hvc1 container tag.
		base = append(base, "-c:v", "libx265", "-preset", "ultrafast")
	default:
		base = append(base, "-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-bf", "2")
	}
	return append(base, "-c:a", "aac", "-f", "flv", dst)
}

// TestRTMPServerStartCloseAddr exercises the listener lifecycle (Start/Addr/
// Close) without a broadcaster — covering the network-bind path that the
// ffmpeg e2e tests otherwise gate behind ONSCREEN_RTMP_E2E.
func TestRTMPServerStartCloseAddr(t *testing.T) {
	s := NewRTMPServer("127.0.0.1:0", testLogger())
	s.SetAuthorizer(func(string) bool { return true })
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	if _, _, err := net.SplitHostPort(s.Addr()); err != nil {
		t.Fatalf("Addr() = %q, not host:port: %v", s.Addr(), err)
	}
	if s.IsLive("anything") {
		t.Fatal("nothing should be live on a fresh server")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestRTMPHandlerPublishFlow drives the go-rtmp callback handler directly
// (OnPublish/OnSetDataFrame/OnAudio/OnVideo/OnClose) without a real network
// connection, so the handler glue is covered in CI rather than only by the
// gated ffmpeg e2e. Asserts auth, registration, media fan-out, and teardown.
func TestRTMPHandlerPublishFlow(t *testing.T) {
	s := NewRTMPServer(":0", testLogger())
	s.SetAuthorizer(func(k string) bool { return k == "good" })

	// Unauthorized key is rejected.
	hbad := &rtmpHandler{server: s}
	if err := hbad.OnPublish(nil, 0, &rtmpmsg.NetStreamPublish{PublishingName: "bad"}); !errors.Is(err, errRTMPUnauthorized) {
		t.Fatalf("OnPublish(bad): want unauthorized, got %v", err)
	}

	h := &rtmpHandler{server: s}
	h.OnServe(nil)
	if err := h.OnPublish(nil, 0, &rtmpmsg.NetStreamPublish{PublishingName: "good"}); err != nil {
		t.Fatalf("OnPublish(good): %v", err)
	}
	if !s.IsLive("good") {
		t.Fatal("IsLive should be true after OnPublish")
	}
	// A second publish on the same connection is rejected.
	if err := h.OnPublish(nil, 0, &rtmpmsg.NetStreamPublish{PublishingName: "good"}); err == nil {
		t.Fatal("second OnPublish on same conn should error")
	}

	// Establish the broadcast (metadata + seq headers + keyframe).
	if err := h.OnSetDataFrame(0, &rtmpmsg.NetStreamSetDataFrame{Payload: []byte("onMetaData")}); err != nil {
		t.Fatalf("OnSetDataFrame: %v", err)
	}
	if err := h.OnVideo(0, bytes.NewReader(avcSeqBody())); err != nil {
		t.Fatalf("OnVideo seq: %v", err)
	}
	if err := h.OnAudio(0, bytes.NewReader(aacSeqBody())); err != nil {
		t.Fatalf("OnAudio seq: %v", err)
	}
	if err := h.OnVideo(1000, bytes.NewReader(avcFrameBody(true))); err != nil {
		t.Fatalf("OnVideo keyframe: %v", err)
	}

	rc, err := s.Subscribe("good")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer rc.Close()
	if sr, ok := rc.(*subReader); !ok || sr.FFmpegInputFormat() != "flv" {
		t.Fatalf("Subscribe reader should hint flv input format")
	}
	if err := h.OnVideo(1040, bytes.NewReader(avcFrameBody(false))); err != nil {
		t.Fatalf("OnVideo live: %v", err)
	}

	done := make(chan []parsedTag, 1)
	go func() { tags, _ := parseFLV(rc, 4); done <- tags }()
	select {
	case tags := <-done:
		sawVideo := false
		for _, tg := range tags {
			if tg.tagType == flvTagVideo {
				sawVideo = true
			}
		}
		if !sawVideo {
			t.Fatalf("no video tags flowed through the handler; got %d", len(tags))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading FLV from handler-fed publish")
	}

	h.OnClose()
	if s.IsLive("good") {
		t.Fatal("IsLive should be false after OnClose")
	}
}

func TestRTMPDriverGetters(t *testing.T) {
	d := NewRTMPDriver("S", RTMPConfig{StreamKey: "k"}, NewRTMPServer(":0", testLogger()))
	if d.TuneCount() != 1 {
		t.Fatalf("TuneCount=%d want 1", d.TuneCount())
	}
	if err := d.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d.StreamKey() != "k" {
		t.Fatalf("StreamKey=%q want k", d.StreamKey())
	}
}

func TestConstantTimeKeyEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"secret", "secret", true},
		{"secret", "Secret", false},
		{"secret", "secre", false},
		{"", "", true},
		{"a", "", false},
	}
	for _, c := range cases {
		if got := constantTimeKeyEqual(c.a, c.b); got != c.want {
			t.Fatalf("constantTimeKeyEqual(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
