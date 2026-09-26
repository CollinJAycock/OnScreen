package api

import "net/http"

// resizedArtworkWriter is the io.Writer handed to artwork.Manager.Resize. It
// holds back the success headers — image/jpeg plus the week-long immutable
// Cache-Control (and the Vary pair when shared-cacheable) — until Resize
// writes its first body byte.
//
// Setting them up front meant a resize that failed before writing anything
// (an undecodable .tbn, a source deleted mid-request) went out as a 200 with
// an empty body that the browser, and a CDN when the response was public,
// then pinned for a week as the poster.
type resizedArtworkWriter struct {
	w            http.ResponseWriter
	cacheControl string
	vary         bool
	wrote        bool
}

func (rw *resizedArtworkWriter) Write(p []byte) (int, error) {
	if !rw.wrote {
		rw.wrote = true
		h := rw.w.Header()
		h.Set("Content-Type", "image/jpeg")
		if rw.vary {
			// Both carriers, always: a cache keyed on only one of them will
			// happily serve a cookie-authenticated response to a
			// token-authenticated caller.
			h.Add("Vary", "Authorization")
			h.Add("Vary", "Cookie")
		}
		h.Set("Cache-Control", rw.cacheControl)
	}
	return rw.w.Write(p)
}

// fail answers a Resize error. Before any byte went out it sends the same 404
// a missing artwork file gets, marked no-store so nothing caches the failure.
// After that the status line is already on the wire and there is nothing left
// to change.
func (rw *resizedArtworkWriter) fail(req *http.Request) {
	if rw.wrote {
		return
	}
	rw.w.Header().Set("Cache-Control", "no-store")
	http.NotFound(rw.w, req)
}
