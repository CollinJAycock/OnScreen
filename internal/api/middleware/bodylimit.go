package middleware

import (
	"net/http"
	"strings"
)

// MaxBytesBody limits request body size, returning 413 if exceeded. Requests
// whose URL path ends with one of exemptSuffixes are NOT wrapped — used for
// large streamed uploads (e.g. the DB restore endpoint) that the handler
// spools to disk and bounds with its own MaxBytesReader.
func MaxBytesBody(maxBytes int64, exemptSuffixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && !hasAnySuffix(r.URL.Path, exemptSuffixes) {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
