package transcode

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// SegmentProxyToken derives the bearer token that authenticates segment /
// seghead requests between the API server and a worker's segment HTTP server.
// Both sides compute it from the shared SECRET_KEY (every fleet node already
// holds it), so closing the otherwise-open segment endpoint needs no extra
// configuration. Returns "" for an empty key — which leaves the segment server
// open, but only happens when SECRET_KEY is unset (the server config layer
// rejects that in production).
func SegmentProxyToken(secretKey string) string {
	if secretKey == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(secretKey))
	_, _ = m.Write([]byte("onscreen-segment-proxy-v1"))
	return hex.EncodeToString(m.Sum(nil))
}
