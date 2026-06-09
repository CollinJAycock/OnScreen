// rtmp.go implements an embedded RTMP ingest server so a broadcaster (OBS,
// ffmpeg, etc.) can push a live stream into OnScreen, which then surfaces it
// as a Live TV channel. This is the "go live" path — a self-hosted
// replacement for a separate Owncast install.
//
// The design reuses the existing Live TV machinery wholesale: an RTMPDriver
// implements the same Driver interface as the HDHomeRun and M3U tuners, so
// the HLS proxy, channel listing, the web player, and the DVR all work
// unchanged. The only net-new concept is push-vs-pull — a tuner is polled on
// demand, whereas an RTMP broadcast exists only while a broadcaster is
// pushing.
//
// Data flow:
//
//	OBS ──rtmp://host:1935/live/<key>──▶ RTMPServer (authorize key)
//	                                        │  frame RTMP payloads → FLV, cache headers
//	   RTMPDriver.OpenStream ──Subscribe──▶ io.Pipe(FLV) ──▶ HLSProxy ffmpeg ──▶ HLS
//
// Codec handling: rather than decode/re-encode each frame (which would lock us
// to H.264), the server passes the raw RTMP tag payloads straight through into
// hand-framed FLV tags. This is codec-agnostic — legacy H.264/AAC and
// enhanced-RTMP HEVC/AV1/VP9 all flow through identically, and ffmpeg's FLV
// demuxer (6.1+) understands them all. The server only parses the first byte
// (and the enhanced FourCC) far enough to classify sequence headers and
// keyframes, which it caches so a viewer joining mid-broadcast can start
// decoding. The HLS proxy then transcodes whatever came in to browser-safe
// H.264.
//
// The server drains the publisher continuously even when nobody is watching
// (otherwise OBS's TCP send buffer backs up and it drops the connection).

package livetv

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
	"golang.org/x/net/netutil"
)

const (
	// rtmpMaxConns bounds concurrent ingest connections so unauthenticated
	// peers can't exhaust goroutines/buffers before the publish-key check.
	rtmpMaxConns = 64
	// rtmpHandshakeTimeout drops a connection that hasn't authenticated
	// (reached OnPublish) within this window.
	rtmpHandshakeTimeout = 15 * time.Second
)

const (
	// TunerTypeRTMP is the backend type for an RTMP live-broadcast device.
	// Stored in tuner_devices.type; one device == one ingest stream key ==
	// one Live TV channel.
	TunerTypeRTMP TunerType = "rtmp"

	// rtmpChannelNumber is the fixed channel number every RTMP device hosts.
	// A broadcast device backs exactly one channel, so the number is constant
	// and the secret stream key never has to appear in the channel listing.
	rtmpChannelNumber = "1"

	// rtmpSubQueueDepth bounds how many tags are buffered for the active
	// viewer pipeline before frames are dropped. The broadcaster produces at
	// ~realtime and the HLS ffmpeg consumes at ~realtime, so a few hundred
	// frames of slack absorbs transient jitter; a sustained overflow means
	// the transcoder has stalled, and dropping live frames is correct — we
	// must never block the broadcaster's receive loop.
	rtmpSubQueueDepth = 256

	// FLV tag types.
	flvTagAudio  = 8
	flvTagVideo  = 9
	flvTagScript = 18
)

var (
	errRTMPUnauthorized = errors.New("livetv: rtmp stream key not authorized")
	errRTMPAlreadyLive  = errors.New("livetv: rtmp stream key already broadcasting")
)

// RTMPConfig is the tuner_devices.config blob for type='rtmp' rows. StreamKey
// is the secret a broadcaster appends to the ingest URL
// (rtmp://host:1935/live/<StreamKey>); it authenticates the push and routes
// it to this device's channel.
type RTMPConfig struct {
	StreamKey string `json:"stream_key"`
}

// ── RTMP server ──────────────────────────────────────────────────────────────

// RTMPServer is the process-wide RTMP listener. One instance is shared across
// all RTMP devices; it routes an inbound push (keyed by the stream key in the
// publish URL) to a per-broadcast fan-out, and hands viewers an FLV byte
// stream via Subscribe.
type RTMPServer struct {
	addr   string
	logger *slog.Logger

	srv      *rtmp.Server
	listener net.Listener

	authMu    sync.RWMutex
	authorize func(streamKey string) bool

	mu        sync.Mutex
	publishes map[string]*rtmpPublish // live broadcasts, keyed by stream key
}

// NewRTMPServer constructs a server bound to addr (e.g. ":1935"). Call
// SetAuthorizer before Start so publishes can be validated; Start begins
// accepting connections.
func NewRTMPServer(addr string, logger *slog.Logger) *RTMPServer {
	return &RTMPServer{
		addr:      addr,
		logger:    logger,
		publishes: make(map[string]*rtmpPublish),
	}
}

// SetAuthorizer installs the stream-key validator. Until it is set (or if it
// is nil), every publish is rejected — the server fails closed so an
// unconfigured deployment can't be pushed to by anyone.
func (s *RTMPServer) SetAuthorizer(fn func(streamKey string) bool) {
	s.authMu.Lock()
	s.authorize = fn
	s.authMu.Unlock()
}

func (s *RTMPServer) authorized(key string) bool {
	s.authMu.RLock()
	fn := s.authorize
	s.authMu.RUnlock()
	if fn == nil {
		return false
	}
	return fn(key)
}

// Start binds the listen socket and serves in a background goroutine. A bind
// failure is returned so the caller can decide whether it's fatal (it isn't —
// live TV without broadcast ingest is still useful).
func (s *RTMPServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("rtmp listen %s: %w", s.addr, err)
	}
	// Cap concurrent connections before the publish-key check (DoS guard).
	ln = netutil.LimitListener(ln, rtmpMaxConns)
	s.listener = ln
	s.srv = rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			// Drop a connection that doesn't authenticate (reach OnPublish)
			// within the handshake window so an idle/unauthenticated peer
			// can't pin a goroutine + buffers; cleared once a publish is
			// accepted (the session is then long-lived). ConnConfig defaults
			// (4KB buffers, 16MB max chunk, discarded logger) are appropriate.
			_ = conn.SetReadDeadline(time.Now().Add(rtmpHandshakeTimeout))
			return conn, &rtmp.ConnConfig{Handler: &rtmpHandler{server: s, rawConn: conn}}
		},
	})
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, rtmp.ErrClosed) {
			s.logger.Error("rtmp server stopped", "err", err)
		}
	}()
	s.logger.Info("rtmp ingest listening", "addr", s.addr)
	return nil
}

// Close stops the listener and disconnects in-flight broadcasts.
func (s *RTMPServer) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// Addr returns the resolved listen address (useful when the configured addr
// used an ephemeral ":0" port, and for surfacing the ingest port in the UI).
// Returns the configured addr if the listener isn't up yet.
func (s *RTMPServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

// register is invoked from a handler's OnPublish. It authorizes the key and,
// if no broadcaster already holds it, creates the per-broadcast fan-out. Only
// one broadcaster per key — a second concurrent publish is rejected.
func (s *RTMPServer) register(key string) (*rtmpPublish, error) {
	if key == "" || !s.authorized(key) {
		return nil, errRTMPUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.publishes[key]; exists {
		return nil, errRTMPAlreadyLive
	}
	p := newRTMPPublish(key)
	s.publishes[key] = p
	s.logger.Info("rtmp broadcast started", "stream_key", maskKey(key))
	return p, nil
}

func (s *RTMPServer) unregister(key string, p *rtmpPublish) {
	s.mu.Lock()
	if cur, ok := s.publishes[key]; ok && cur == p {
		delete(s.publishes, key)
	}
	s.mu.Unlock()
	p.close()
	s.logger.Info("rtmp broadcast ended", "stream_key", maskKey(key))
}

func (s *RTMPServer) lookup(key string) (*rtmpPublish, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.publishes[key]
	return p, ok
}

// Subscribe returns an FLV byte stream for the live broadcast on key, or
// ErrChannelNotFound when nobody is currently broadcasting. The caller (the
// HLS proxy, via RTMPDriver.OpenStream) reads FLV and closes the reader to
// detach. The returned reader hints "flv" to the HLS proxy's ffmpeg.
func (s *RTMPServer) Subscribe(key string) (io.ReadCloser, error) {
	p, ok := s.lookup(key)
	if !ok {
		return nil, ErrChannelNotFound
	}
	return p.subscribe()
}

// IsLive reports whether a broadcaster is currently pushing to key. Drives
// the broadcast status/admin UI.
func (s *RTMPServer) IsLive(key string) bool {
	_, ok := s.lookup(key)
	return ok
}

// ── per-connection handler ───────────────────────────────────────────────────

// rtmpHandler bridges one RTMP connection's callbacks into a single
// rtmpPublish. go-rtmp invokes these on the connection's read goroutine.
type rtmpHandler struct {
	rtmp.DefaultHandler
	server  *RTMPServer
	conn    *rtmp.Conn
	rawConn net.Conn // underlying TCP conn — used to clear the handshake deadline on publish
	key     string
	pub     *rtmpPublish

	videoLogged bool // logged the codec of the first video tag
}

func (h *rtmpHandler) OnServe(conn *rtmp.Conn) { h.conn = conn }

// OnPublish authorizes the stream key and claims the broadcast slot. Returning
// an error rejects the publish (OBS shows "could not access stream key").
func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	if h.pub != nil {
		return errors.New("livetv: connection already publishing")
	}
	pub, err := h.server.register(cmd.PublishingName)
	if err != nil {
		h.server.logger.Warn("rtmp publish rejected",
			"err", err, "stream_key", maskKey(cmd.PublishingName))
		return err
	}
	h.key = cmd.PublishingName
	h.pub = pub
	// Authenticated: clear the handshake read deadline so the long-lived
	// publish stream isn't torn down mid-broadcast.
	if h.rawConn != nil {
		_ = h.rawConn.SetReadDeadline(time.Time{})
	}
	return nil
}

func (h *rtmpHandler) OnSetDataFrame(timestamp uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.pub == nil || len(data.Payload) == 0 {
		return nil
	}
	// data.Payload is the FLV script body ("onMetaData" + AMF object) — pass
	// it through verbatim.
	h.pub.publish(&mediaTag{tagType: flvTagScript, timestamp: timestamp, data: append([]byte(nil), data.Payload...), kind: tagMeta})
	return nil
}

func (h *rtmpHandler) OnAudio(timestamp uint32, payload io.Reader) error {
	if h.pub == nil {
		return nil
	}
	raw, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	kind := tagAudioData
	if audioIsSequenceHeader(raw) {
		kind = tagAudioSeqHeader
	}
	h.pub.publish(&mediaTag{tagType: flvTagAudio, timestamp: timestamp, data: raw, kind: kind})
	return nil
}

func (h *rtmpHandler) OnVideo(timestamp uint32, payload io.Reader) error {
	if h.pub == nil {
		return nil
	}
	raw, err := io.ReadAll(payload)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	seqHeader, keyframe := classifyVideo(raw)
	if !h.videoLogged {
		h.videoLogged = true
		h.server.logger.Info("rtmp ingest: video stream",
			"stream_key", maskKey(h.key), "codec", videoCodecName(raw))
	}
	kind := tagVideoData
	switch {
	case seqHeader:
		kind = tagVideoSeqHeader
	case keyframe:
		kind = tagVideoKeyframe
	}
	h.pub.publish(&mediaTag{tagType: flvTagVideo, timestamp: timestamp, data: raw, kind: kind})
	return nil
}

func (h *rtmpHandler) OnClose() {
	if h.pub != nil {
		h.server.unregister(h.key, h.pub)
		h.pub = nil
	}
}

// ── codec classification ─────────────────────────────────────────────────────

// classifyVideo inspects an FLV video tag body far enough to tell whether it
// is a codec sequence header (decoder config) and/or a keyframe. Handles both
// legacy FLV (H.264) and enhanced-RTMP (HEVC/AV1/VP9) framing.
func classifyVideo(b []byte) (seqHeader, keyframe bool) {
	if len(b) < 1 {
		return false, false
	}
	if b[0]&0x80 != 0 {
		// Enhanced RTMP: bit7=IsExHeader, bits6-4=FrameType, bits3-0=PacketType.
		frameType := (b[0] >> 4) & 0x07
		packetType := b[0] & 0x0f
		seqHeader = packetType == 0 // PacketTypeSequenceStart
		keyframe = frameType == 1 && !seqHeader
		return seqHeader, keyframe
	}
	// Legacy: bits7-4=FrameType, bits3-0=CodecID; for AVC, b[1]=AVCPacketType.
	frameType := (b[0] >> 4) & 0x0f
	codecID := b[0] & 0x0f
	if codecID == 7 && len(b) >= 2 && b[1] == 0 {
		seqHeader = true // AVC sequence header
	}
	keyframe = frameType == 1 && !seqHeader
	return seqHeader, keyframe
}

// audioIsSequenceHeader reports whether an FLV audio tag body is a codec
// sequence header (AAC AudioSpecificConfig, or enhanced-audio SequenceStart).
func audioIsSequenceHeader(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	// Enhanced-RTMP audio is marked by sound-format nibble 9 — NOT the high
	// bit, because legacy AAC is sound-format 10 (0b1010), which itself sets
	// the high bit. In both layouts b[1] is the packet type; 0 = seq header.
	if (b[0] >> 4) == 9 {
		return b[1] == 0
	}
	soundFormat := (b[0] >> 4) & 0x0f
	return soundFormat == 10 && b[1] == 0 // legacy AAC AudioSpecificConfig
}

// videoCodecName returns a short codec label for logging.
func videoCodecName(b []byte) string {
	if len(b) < 1 {
		return "unknown"
	}
	if b[0]&0x80 != 0 {
		if len(b) >= 5 {
			return string(b[1:5]) // enhanced FourCC: hvc1 / av01 / vp09
		}
		return "enhanced"
	}
	switch b[0] & 0x0f {
	case 7:
		return "h264"
	case 12:
		return "hevc"
	default:
		return fmt.Sprintf("legacy-%d", b[0]&0x0f)
	}
}

// ── per-broadcast fan-out ────────────────────────────────────────────────────

// tagKind classifies a mediaTag for fan-out caching.
type tagKind uint8

const (
	tagVideoData tagKind = iota
	tagVideoSeqHeader
	tagVideoKeyframe
	tagAudioData
	tagAudioSeqHeader
	tagMeta
)

// mediaTag is one FLV tag's raw body plus the metadata the fan-out needs. The
// data slice is the verbatim RTMP payload — never decoded — so any codec
// passes through unchanged.
type mediaTag struct {
	tagType   byte
	timestamp uint32
	data      []byte
	kind      tagKind
}

// rtmpPublish receives FLV tags from one broadcaster and forwards them to the
// current viewer, caching the tags a late-joining viewer needs to begin
// decoding (script metadata, video/audio sequence headers, last keyframe).
//
// Only one viewer attaches at a time: the HLS proxy creates exactly one ffmpeg
// transcode per channel and shares its HLS output across all browser viewers,
// so a single subscriber here feeds an arbitrary number of watchers.
type rtmpPublish struct {
	key string

	mu           sync.Mutex
	closed       bool
	scriptData   *mediaTag
	videoSeqHead *mediaTag
	audioSeqHead *mediaTag
	lastKeyFrame *mediaTag
	sub          *rtmpSub
}

func newRTMPPublish(key string) *rtmpPublish {
	return &rtmpPublish{key: key}
}

// publish ingests one FLV tag: it updates the late-join cache and, if a viewer
// is attached, forwards the tag (initializing the viewer with the cached
// headers + keyframe on its first eligible tag).
func (p *rtmpPublish) publish(tag *mediaTag) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	switch tag.kind {
	case tagMeta:
		p.scriptData = tag
	case tagVideoSeqHeader:
		p.videoSeqHead = tag
	case tagAudioSeqHeader:
		p.audioSeqHead = tag
	case tagVideoKeyframe:
		p.lastKeyFrame = tag
	}

	if p.sub == nil {
		return
	}
	if !p.sub.initialized {
		// A viewer can't decode anything until it has the video sequence
		// header; hold off until it has been seen.
		if p.videoSeqHead == nil {
			return
		}
		p.sub.enqueue(p.scriptData)
		p.sub.enqueue(p.audioSeqHead)
		p.sub.enqueue(p.videoSeqHead)
		p.sub.enqueue(p.lastKeyFrame)
		p.sub.initialized = true
		// Don't double-send a tag that was just delivered as part of init.
		if tag == p.lastKeyFrame || tag == p.videoSeqHead ||
			tag == p.audioSeqHead || tag == p.scriptData {
			return
		}
	}
	p.sub.enqueue(tag)
}

// subscribe attaches a fresh viewer pipe, replacing any existing subscriber
// (e.g. when the HLS proxy re-acquires the channel after its grace period).
func (p *rtmpPublish) subscribe() (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrChannelNotFound
	}
	if p.sub != nil {
		p.sub.shutdown()
		p.sub = nil
	}
	pr, pw := io.Pipe()
	s := &rtmpSub{
		pr:   pr,
		pw:   pw,
		ch:   make(chan *mediaTag, rtmpSubQueueDepth),
		done: make(chan struct{}),
	}
	p.sub = s
	// The FLV header write blocks until ffmpeg starts reading, so it must run
	// on the drain goroutine, never here under p.mu (that would deadlock).
	go s.run()
	return &subReader{pr: pr, detach: func() { p.detach(s) }}, nil
}

func (p *rtmpPublish) detach(s *rtmpSub) {
	p.mu.Lock()
	if p.sub == s {
		p.sub = nil
	}
	p.mu.Unlock()
	s.shutdown()
}

func (p *rtmpPublish) close() {
	p.mu.Lock()
	p.closed = true
	s := p.sub
	p.sub = nil
	p.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}

// ── viewer subscriber ────────────────────────────────────────────────────────

// rtmpSub is one attached viewer: a buffered queue feeding an FLV writer into
// an io.Pipe whose read end the HLS proxy's ffmpeg consumes. The queue + drain
// goroutine decouple the broadcaster's receive loop from ffmpeg's consumption
// pace so a transcoder stall drops frames instead of back-pressuring (and
// ultimately disconnecting) the broadcaster.
type rtmpSub struct {
	pr *io.PipeReader
	pw *io.PipeWriter

	ch   chan *mediaTag
	done chan struct{}

	initialized bool

	tsBase    uint32
	tsHasBase bool

	closeOnce sync.Once
}

// enqueue is called under the publish mutex. Non-blocking: a full buffer means
// the viewer's transcode has fallen behind, so the tag is dropped.
func (s *rtmpSub) enqueue(tag *mediaTag) {
	if tag == nil {
		return
	}
	select {
	case s.ch <- tag:
	case <-s.done:
	default: // buffer full — drop the frame
	}
}

func (s *rtmpSub) run() {
	if err := writeFLVHeader(s.pw); err != nil {
		_ = s.pw.CloseWithError(err)
		return
	}
	for {
		select {
		case <-s.done:
			return
		case tag := <-s.ch:
			if err := s.write(tag); err != nil {
				// The reader (ffmpeg/HLS) went away. Signal EOF and stop; the
				// publish detaches us via the reader's Close.
				_ = s.pw.CloseWithError(err)
				return
			}
		}
	}
}

func (s *rtmpSub) write(tag *mediaTag) error {
	// Rebase timestamps so the viewer's stream starts near zero regardless of
	// how long the broadcast has already been running.
	ts := tag.timestamp
	if !s.tsHasBase && ts != 0 {
		s.tsBase = ts
		s.tsHasBase = true
	}
	if ts >= s.tsBase {
		ts -= s.tsBase
	} else {
		ts = 0
	}
	return writeFLVTag(s.pw, tag.tagType, ts, tag.data)
}

func (s *rtmpSub) shutdown() {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.pw.Close()
	})
}

// subReader is the io.ReadCloser handed to the HLS proxy. Closing it detaches
// the subscriber from the broadcast (the broadcast itself stays live for the
// next viewer). It advertises FLV so the proxy forces ffmpeg's flv demuxer.
type subReader struct {
	pr     *io.PipeReader
	detach func()
	once   sync.Once
}

func (r *subReader) Read(b []byte) (int, error) { return r.pr.Read(b) }

func (r *subReader) Close() error {
	r.once.Do(r.detach)
	return r.pr.Close()
}

// FFmpegInputFormat satisfies the HLS proxy's optional input-format hint: the
// RTMP path produces FLV, which is more reliably handled with an explicit
// `-f flv` than left to autodetection on a non-seekable live pipe.
func (r *subReader) FFmpegInputFormat() string { return "flv" }

// ── FLV framing ──────────────────────────────────────────────────────────────

// writeFLVHeader writes the 9-byte FLV file header (audio+video flags) plus
// the initial zero PreviousTagSize.
func writeFLVHeader(w io.Writer) error {
	_, err := w.Write([]byte{
		'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, // header (flags 0x05 = audio+video)
		0x00, 0x00, 0x00, 0x00, // PreviousTagSize0
	})
	return err
}

// writeFLVTag frames a raw FLV tag body into a complete FLV tag (11-byte
// header + body + trailing PreviousTagSize) and writes it.
func writeFLVTag(w io.Writer, tagType byte, timestamp uint32, body []byte) error {
	dataSize := len(body)
	buf := make([]byte, 11+dataSize+4)
	buf[0] = tagType
	buf[1] = byte(dataSize >> 16)
	buf[2] = byte(dataSize >> 8)
	buf[3] = byte(dataSize)
	buf[4] = byte(timestamp >> 16)
	buf[5] = byte(timestamp >> 8)
	buf[6] = byte(timestamp)
	buf[7] = byte(timestamp >> 24) // extended timestamp byte
	// buf[8:11] StreamID = 0
	copy(buf[11:], body)
	binary.BigEndian.PutUint32(buf[11+dataSize:], uint32(11+dataSize))
	_, err := w.Write(buf)
	return err
}

// maskKey redacts all but the last 4 characters of a stream key for logs.
func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// ── RTMP driver ──────────────────────────────────────────────────────────────

// RTMPDriver exposes a single live-broadcast channel backed by an RTMP push.
// It implements Driver so broadcasts flow through the same HLS proxy, channel
// listing, and DVR as tuner channels. Unlike pull tuners, the stream exists
// only while a broadcaster is pushing this device's stream key; OpenStream
// returns ErrChannelNotFound (surfaced to the client as a 404 / "offline")
// when nobody is live.
type RTMPDriver struct {
	name   string
	cfg    RTMPConfig
	server *RTMPServer
}

// NewRTMPDriver builds a driver bound to the shared RTMP server.
func NewRTMPDriver(name string, cfg RTMPConfig, server *RTMPServer) *RTMPDriver {
	return &RTMPDriver{name: name, cfg: cfg, server: server}
}

func (d *RTMPDriver) Type() TunerType { return TunerTypeRTMP }

// Discover returns the single channel this broadcast device hosts. The stream
// key is intentionally NOT used as the channel number — it stays out of the
// listing.
func (d *RTMPDriver) Discover(_ context.Context) ([]DiscoveredChannel, error) {
	return []DiscoveredChannel{{Number: rtmpChannelNumber, Name: d.name}}, nil
}

// TuneCount is 1 — a broadcast is a single live source.
func (d *RTMPDriver) TuneCount() int { return 1 }

// OpenStream returns the live FLV stream for this device's broadcast, or
// ErrChannelNotFound when nobody is broadcasting. channelNumber is ignored —
// the device hosts exactly one channel.
func (d *RTMPDriver) OpenStream(_ context.Context, _ string) (Stream, error) {
	if d.server == nil {
		return nil, ErrChannelNotFound
	}
	return d.server.Subscribe(d.cfg.StreamKey)
}

// Probe always succeeds: the broadcast device is our own embedded server, so
// it's reachable by definition. Whether someone is actually broadcasting is a
// separate "live" concept surfaced via the broadcast status endpoint.
func (d *RTMPDriver) Probe(_ context.Context) error { return nil }

// StreamKey returns the configured ingest key — used by the broadcast admin /
// status endpoints, never placed in the public channel listing.
func (d *RTMPDriver) StreamKey() string { return d.cfg.StreamKey }

// RTMPFactory returns a Registry factory bound to the shared RTMP server.
func RTMPFactory(server *RTMPServer) DriverFactory {
	return func(name string, configJSON []byte) (Driver, error) {
		var cfg RTMPConfig
		if len(configJSON) > 0 {
			if err := json.Unmarshal(configJSON, &cfg); err != nil {
				return nil, fmt.Errorf("rtmp config parse: %w", err)
			}
		}
		if cfg.StreamKey == "" {
			return nil, errors.New("rtmp: stream_key required")
		}
		return NewRTMPDriver(name, cfg, server), nil
	}
}

// constantTimeKeyEqual compares two stream keys without leaking length-
// independent timing. Used by the publish authorizer.
func constantTimeKeyEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
