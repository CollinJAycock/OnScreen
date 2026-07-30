package v1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/staticabr"
)

// staticKey prefixes a static-ABR key with the configured root (empty for
// bucket-relative object storage).
func (h *NativeTranscodeHandler) staticKey(rel string) string {
	return path.Join(h.staticRoot, rel)
}

// staticAvailable reports whether a fresh pre-encoded ladder exists for the file
// (master present + source hash matches), so the ABR start path can serve it
// statically instead of spinning up a live session.
func (h *NativeTranscodeHandler) staticAvailable(ctx context.Context, fileID uuid.UUID, hash string) bool {
	if !h.staticEnabled || hash == "" {
		return false
	}
	ok, err := staticabr.StoreChecker{Store: h.mediaStore(), Prefix: h.staticRoot}.IsEncoded(ctx, fileID, hash)
	return err == nil && ok
}

// staticFileAccess resolves {fileID} and enforces the same per-library ACL as the
// live transcode path. Returns the file or writes the response and returns false.
func (h *NativeTranscodeHandler) staticFileAccess(w http.ResponseWriter, r *http.Request) (*media.File, bool) {
	fileID, err := parseUUID(r, "fileID")
	if err != nil {
		respond.BadRequest(w, r, "invalid file id")
		return nil, false
	}
	ctx := r.Context()
	file, err := h.media.GetFile(ctx, fileID)
	if err != nil {
		respond.NotFound(w, r)
		return nil, false
	}
	item, err := h.media.GetItem(ctx, file.MediaItemID)
	if err != nil {
		respond.NotFound(w, r)
		return nil, false
	}
	// Always require claims (these routes sit behind RequiredAllowQueryToken),
	// then enforce the per-library ACL and the content-rating ceiling — the
	// same gates the live StreamFile path applies. Previously this block was
	// skipped entirely when h.access was nil (fail-open) and never checked the
	// rating ceiling, so a restricted profile could stream a pre-encoded
	// ladder of content above its limit.
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return nil, false
	}
	if h.access != nil {
		ok, aerr := h.access.CanAccessLibrary(ctx, claims.UserID, item.LibraryID, claims.IsAdmin)
		if aerr != nil || !ok {
			respond.NotFound(w, r) // fail-closed, indistinguishable from missing
			return nil, false
		}
	}
	cr := ""
	if item.ContentRating != nil {
		cr = *item.ContentRating
	}
	if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
		respond.Forbidden(w, r)
		return nil, false
	}
	// Parental watch limit. ADR-035 introduced this path specifically to serve
	// playback "instead of a live session", so it must carry the same gate
	// Start does — otherwise any title with a pre-encoded ladder (by
	// construction, the most-played ones) was watchable outside allowed hours.
	//
	// It lives HERE, in the shared accessor, not in StaticMaster alone. Gating
	// only the master assumed playback begins by fetching it; nothing enforces
	// that. The rung playlist and segment URLs are derivable from the fileID
	// and are served by the same asset token, so a client that skips the master
	// — or replays URLs from an earlier session — streamed the whole ladder
	// ungated. StaticMaster, StaticRung and StaticSegment all route through
	// this function, so there is now no static entry point without the gate,
	// and a fourth one cannot be added without inheriting it.
	//
	// Rung playlists and segments are hot, which is what the master-only
	// placement was trying to protect. That cost is handled by the short-TTL
	// read memo the store is wrapped in (see newWatchLimitMemo), not by leaving
	// routes ungated.
	if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
		return nil, false
	}
	return file, true
}

// validRung guards the {rung} path param, which is interpolated into an
// object-store key — same posture as the segment-name check: non-empty, no
// path separators, no "..".
func validRung(s string) bool {
	return s != "" && !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}

// StaticMaster serves the pre-encoded master playlist, rewriting each rung URI to
// this server's static rung endpoint (carrying the query token so HLS.js fetches
// authenticate). GET /transcode/static/{fileID}/master.m3u8
func (h *NativeTranscodeHandler) StaticMaster(w http.ResponseWriter, r *http.Request) {
	file, ok := h.staticFileAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	hash := ""
	if file.FileHash != nil {
		hash = *file.FileHash
	}
	if !h.staticAvailable(ctx, file.ID, hash) {
		respond.NotFound(w, r)
		return
	}
	master, err := h.readStaticPlaylist(ctx, staticabr.MasterKey(file.ID))
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	base := publicSeg(h.segBase(), fmt.Sprintf("/api/v1/transcode/static/%s", file.ID))
	out := rewritePlaylistURIs(master, func(uri string) string {
		// uri is "<rung>/index.m3u8" relative to the master.
		return base + "/" + uri + tokenQuery(token)
	})
	writeM3U8(w, out)
}

// StaticRung serves a rung's media playlist, rewriting each segment URI to a
// signed object-storage/CDN URL when the backend can offload, else to this
// server's static segment endpoint. GET /transcode/static/{fileID}/{rung}/index.m3u8
func (h *NativeTranscodeHandler) StaticRung(w http.ResponseWriter, r *http.Request) {
	file, ok := h.staticFileAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	rung := chi.URLParam(r, "rung")
	if !validRung(rung) {
		respond.BadRequest(w, r, "invalid rung")
		return
	}
	playlist, err := h.readStaticPlaylist(ctx, staticabr.RungPlaylistKey(file.ID, rung))
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	base := publicSeg(h.segBase(), fmt.Sprintf("/api/v1/transcode/static/%s/%s", file.ID, rung))
	out := rewritePlaylistURIs(playlist, func(seg string) string {
		key := h.staticKey(staticabr.SegmentKey(file.ID, rung, seg))
		if signed, serr := h.mediaStore().SignedURL(ctx, key, auth.StreamTokenTTL); serr == nil && signed != "" {
			return signed // CDN / presigned bucket — bytes never touch the app tier
		}
		return base + "/seg/" + seg + tokenQuery(token)
	})
	writeM3U8(w, out)
}

// StaticSegment serves one pre-encoded segment (the local / no-offload path —
// when SignedURL can offload, StaticRung points segments straight at it and this
// is never hit). GET /transcode/static/{fileID}/{rung}/seg/{name}
func (h *NativeTranscodeHandler) StaticSegment(w http.ResponseWriter, r *http.Request) {
	file, ok := h.staticFileAccess(w, r)
	if !ok {
		return
	}
	rung := chi.URLParam(r, "rung")
	if !validRung(rung) {
		respond.BadRequest(w, r, "invalid rung")
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		respond.BadRequest(w, r, "invalid segment name")
		return
	}
	key := h.staticKey(staticabr.SegmentKey(file.ID, rung, name))
	if err := mediastore.Serve(w, r, h.mediaStore(), key, name, auth.StreamTokenTTL); err != nil {
		if errors.Is(err, mediastore.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "static-abr: serve segment", "key", key, "err", err)
		respond.InternalError(w, r)
	}
}

// readStaticPlaylist reads a small playlist file from the static store.
func (h *NativeTranscodeHandler) readStaticPlaylist(ctx context.Context, rel string) (string, error) {
	f, err := h.mediaStore().Open(ctx, h.staticKey(rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20)) // playlists are tiny; cap defensively
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func tokenQuery(token string) string {
	if token == "" {
		return ""
	}
	return "?token=" + url.QueryEscape(token)
}

func writeM3U8(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/x-mpegURL")
	// Immutable for a given source hash, but `private`, not `public`: these
	// responses are authorization-dependent (staticFileAccess applies the
	// per-library ACL and the caller's content-rating ceiling) AND the rewritten
	// URIs inside carry the caller's own asset token. A shared cache would
	// store one user's authorized playlist — token and all — and hand it to the
	// next requester.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write([]byte(body))
}

// rewritePlaylistURIs rewrites every URI line (non-comment, non-blank) of an HLS
// playlist via fn, leaving #EXT tags and blank lines untouched.
func rewritePlaylistURIs(playlist string, fn func(uri string) string) string {
	lines := strings.Split(playlist, "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lines[i] = fn(t)
	}
	return strings.Join(lines, "\n")
}
