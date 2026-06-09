package v1

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/livetv"
)

// RTMPStatusProvider is the slice of the embedded RTMP ingest server the
// broadcast endpoints need: whether a stream key is currently being pushed,
// and the bound listen address (for the ingest URL's port).
type RTMPStatusProvider interface {
	IsLive(streamKey string) bool
	Addr() string
}

// WithRTMP attaches the embedded RTMP server so the broadcast endpoints can
// report live status and the ingest URL. publicHost is the hostname shown in
// the ingest URL (rtmp://<host>:<port>/live/<key>); empty falls back to the
// request host. listenAddr is the server's configured RTMP listen address,
// used to derive the port.
func (h *LiveTVHandler) WithRTMP(rtmp RTMPStatusProvider, publicHost, listenAddr string) *LiveTVHandler {
	h.rtmp = rtmp
	h.rtmpPublicHost = publicHost
	h.rtmpListenAddr = listenAddr
	return h
}

// BroadcastResponse is one live-broadcast device in the admin UI. A broadcast
// is a tuner device of type 'rtmp'; this view adds the derived ingest URL and
// the real-time live/offline status. StreamKey is included because the admin
// needs it to configure OBS — these endpoints are admin-gated.
type BroadcastResponse struct {
	TunerID   uuid.UUID  `json:"tuner_id"`
	ChannelID *uuid.UUID `json:"channel_id,omitempty"`
	Name      string     `json:"name"`
	StreamKey string     `json:"stream_key"`
	IngestURL string     `json:"ingest_url"`
	Enabled   bool       `json:"enabled"`
	IsLive    bool       `json:"is_live"`
}

// ListBroadcasts handles GET /api/v1/tv/broadcasts (admin). Returns every RTMP
// broadcast device with its ingest URL and current live status.
func (h *LiveTVHandler) ListBroadcasts(w http.ResponseWriter, r *http.Request) {
	if !h.broadcastAvailable(w, r) {
		return
	}
	tuners, err := h.svc.ListTuners(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list broadcasts: tuners", "err", err)
		respond.InternalError(w, r)
		return
	}
	channels, err := h.svc.ListChannels(r.Context(), false)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list broadcasts: channels", "err", err)
		respond.InternalError(w, r)
		return
	}
	chanByTuner := make(map[uuid.UUID]uuid.UUID, len(channels))
	for _, c := range channels {
		if c.TunerType == livetv.TunerTypeRTMP {
			chanByTuner[c.TunerID] = c.ID
		}
	}

	out := make([]BroadcastResponse, 0)
	for _, t := range tuners {
		if t.Type != livetv.TunerTypeRTMP {
			continue
		}
		br := h.broadcastFromTuner(r, t)
		if cid, ok := chanByTuner[t.ID]; ok {
			c := cid
			br.ChannelID = &c
		}
		out = append(out, br)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// createBroadcastRequest is the POST body for /api/v1/tv/broadcasts.
type createBroadcastRequest struct {
	Name string `json:"name"`
}

// CreateBroadcast handles POST /api/v1/tv/broadcasts (admin). Generates a
// random stream key and persists an RTMP tuner device, which immediately
// surfaces as a Live TV channel. Returns the broadcast with its ingest URL so
// the operator can paste it (and the key) into OBS.
func (h *LiveTVHandler) CreateBroadcast(w http.ResponseWriter, r *http.Request) {
	if !h.broadcastAvailable(w, r) {
		return
	}
	var body createBroadcastRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		respond.BadRequest(w, r, "name is required")
		return
	}
	key, err := generateStreamKey()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "generate stream key", "err", err)
		respond.InternalError(w, r)
		return
	}
	cfg, _ := json.Marshal(livetv.RTMPConfig{StreamKey: key})
	row, err := h.svc.CreateTuner(r.Context(), livetv.CreateTunerDeviceParams{
		Type:   livetv.TunerTypeRTMP,
		Name:   body.Name,
		Config: cfg,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create broadcast", "err", err)
		respond.InternalError(w, r)
		return
	}

	br := h.broadcastFromTuner(r, row)
	// Resolve the channel CreateTuner's discover just created so the client
	// can link straight to the watch page.
	if channels, cerr := h.svc.ListChannels(r.Context(), false); cerr == nil {
		for _, c := range channels {
			if c.TunerID == row.ID {
				cid := c.ID
				br.ChannelID = &cid
				break
			}
		}
	}
	respond.Created(w, r, br)
}

// broadcastFromTuner builds a BroadcastResponse from an RTMP tuner row.
func (h *LiveTVHandler) broadcastFromTuner(r *http.Request, t livetv.TunerDevice) BroadcastResponse {
	var cfg livetv.RTMPConfig
	_ = json.Unmarshal(t.Config, &cfg)
	isLive := false
	if h.rtmp != nil && cfg.StreamKey != "" {
		isLive = h.rtmp.IsLive(cfg.StreamKey)
	}
	return BroadcastResponse{
		TunerID:   t.ID,
		Name:      t.Name,
		StreamKey: cfg.StreamKey,
		IngestURL: h.ingestURL(r, cfg.StreamKey),
		Enabled:   t.Enabled,
		IsLive:    isLive,
	}
}

// broadcastAvailable gates the broadcast endpoints on both the live-TV service
// and the embedded RTMP server being configured.
func (h *LiveTVHandler) broadcastAvailable(w http.ResponseWriter, r *http.Request) bool {
	if !h.available(w, r) {
		return false
	}
	if h.rtmp == nil {
		respond.Error(w, r, http.StatusServiceUnavailable,
			"RTMP_NOT_ENABLED", "RTMP broadcast ingest is not enabled on this server")
		return false
	}
	return true
}

// ingestURL builds the rtmp:// URL a broadcaster pushes to. Host preference:
// the configured RTMP public host, else the API request's host (port stripped,
// since the RTMP port differs from the HTTP port).
func (h *LiveTVHandler) ingestURL(r *http.Request, streamKey string) string {
	host := h.rtmpPublicHost
	if host == "" {
		host = r.Host
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
	}
	return fmt.Sprintf("rtmp://%s:%s/live/%s", host, rtmpPort(h.rtmpListenAddr), streamKey)
}

// rtmpPort extracts the port from a listen address (":1935", "0.0.0.0:1935"),
// defaulting to 1935.
func rtmpPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	return "1935"
}

// generateStreamKey returns a 128-bit random hex stream key.
func generateStreamKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
