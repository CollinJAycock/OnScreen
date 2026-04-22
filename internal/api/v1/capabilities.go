package v1

import (
	"net/http"

	"github.com/onscreen/onscreen/internal/api/respond"
)

// CapabilitiesResponse describes what this server can do. Public, intended
// for native clients that just discovered the server and need to know
// whether it's worth connecting to.
type CapabilitiesResponse struct {
	Server    CapabilitiesServer    `json:"server"`
	Features  CapabilitiesFeatures  `json:"features"`
	Limits    CapabilitiesLimits    `json:"limits"`
	Discovery CapabilitiesDiscovery `json:"discovery"`
}

// CapabilitiesServer carries server identity. Don't put anything sensitive
// here — the endpoint is anonymous.
type CapabilitiesServer struct {
	Name       string `json:"name"`
	MachineID  string `json:"machine_id"`
	Version    string `json:"version"`
	APIVersion string `json:"api_version"`
}

// CapabilitiesFeatures advertises the optional features clients can rely on.
// All bools so clients can do `if (caps.features.trickplay)` without having
// to interpret strings.
type CapabilitiesFeatures struct {
	Transcode         bool `json:"transcode"`
	Trickplay         bool `json:"trickplay"`
	SubtitlesExternal bool `json:"subtitles_external"`
	SubtitlesOCR      bool `json:"subtitles_ocr"`
	OIDC              bool `json:"oidc"`
	LDAP              bool `json:"ldap"`
	DevicePairing     bool `json:"device_pairing"`
	Plugins           bool `json:"plugins"`
	Backup            bool `json:"backup"`
	PeopleCredits     bool `json:"people_credits"`
	Photos            bool `json:"photos"`
	Music             bool `json:"music"`
	Webhooks          bool `json:"webhooks"`
	Notifications     bool `json:"notifications"`
	Requests          bool `json:"requests"`
}

// CapabilitiesLimits documents server-side caps so a client can pre-validate
// before asking the server to transcode at 8K and getting an error.
type CapabilitiesLimits struct {
	MaxUploadBytes          int64 `json:"max_upload_bytes"`
	MaxTranscodeBitrateKbps int   `json:"max_transcode_bitrate_kbps"`
	MaxTranscodeWidth       int   `json:"max_transcode_width"`
	MaxTranscodeHeight      int   `json:"max_transcode_height"`
}

// CapabilitiesDiscovery describes how to find this server again later
// without going through HTTP every time.
type CapabilitiesDiscovery struct {
	UDPPort int `json:"udp_port,omitempty"`
}

// CapabilitiesProvider is the small slice of config + state the handler
// needs. Injecting an interface (rather than the full Config) keeps the
// handler trivial to test.
type CapabilitiesProvider interface {
	Capabilities() CapabilitiesResponse
}

// CapabilitiesHandler serves GET /api/v1/system/capabilities.
type CapabilitiesHandler struct {
	provider CapabilitiesProvider
}

// NewCapabilitiesHandler constructs a handler.
func NewCapabilitiesHandler(p CapabilitiesProvider) *CapabilitiesHandler {
	return &CapabilitiesHandler{provider: p}
}

// Get handles the request.
func (h *CapabilitiesHandler) Get(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, h.provider.Capabilities())
}
