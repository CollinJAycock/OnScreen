//go:build dev

package api

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// newDevFrontendProxy returns a reverse proxy to the Vite dev server so the
// entire app — UI included — is reachable on the single API port during
// development. The embedded dist is a build artifact baked in at compile time
// and goes stale the moment you edit the SvelteKit source, so in dev we hand
// every non-API/non-asset request to Vite: the SPA shell, HMR websocket,
// /@vite, /@fs, /src, and node_modules pre-bundles all flow through here.
//
// Compiled only under `-tags dev`. Production builds get the no-op variant in
// frontend_prod.go and always serve the embedded dist directly.
func newDevFrontendProxy(rawURL string, logger *slog.Logger) http.Handler {
	if rawURL == "" {
		return nil
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		logger.Warn("dev frontend proxy disabled — invalid DEV_FRONTEND_URL", "url", rawURL, "err", err)
		return nil
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		base(req)
		// Vite validates the Host header against its allowed-hosts list;
		// forward the upstream host so requests relayed from the API port pass.
		req.Host = target.Host
	}
	logger.Info("dev frontend proxy enabled — UI served via Vite on the API port", "target", target.String())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The production CSP is nonce-based (script-src 'nonce-…', connect-src
		// 'self') which blocks Vite's inline bootstrap and HMR websocket. Drop
		// it for proxied dev responses only — this code path is absent from any
		// production binary.
		w.Header().Del("Content-Security-Policy")
		proxy.ServeHTTP(w, r)
	})
}
