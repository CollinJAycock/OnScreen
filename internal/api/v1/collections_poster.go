package v1

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// loadManualForAdmin loads the {id} manual collection for an admin write.
// ok=false means a response was already written.
func (h *CollectionHandler) loadManualForAdmin(w http.ResponseWriter, r *http.Request) (gen.Collection, bool) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return gen.Collection{}, false
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return gen.Collection{}, false
	}
	if !h.requireOwnerOrAdminMutate(w, r, col) || rejectManagedMutation(w, r, col) {
		return gen.Collection{}, false
	}
	if col.Type != collectionTypeManual || h.manual == nil {
		respond.BadRequest(w, r, "only manual collections have an uploaded cover")
		return gen.Collection{}, false
	}
	return col, true
}

// UploadPoster handles POST /api/v1/collections/{id}/poster: an admin's
// cover for a manual collection, as a multipart "poster" field or the raw
// image body. The API's 1 MB body limit applies (the web app scales an image
// down before sending it). The image is decoded through the artwork
// package's bounded path and re-encoded as a JPEG, which is all that's
// stored; anything that doesn't decode is a 400. The cover wins over a
// chosen member poster.
func (h *CollectionHandler) UploadPoster(w http.ResponseWriter, r *http.Request) {
	col, ok := h.loadManualForAdmin(w, r)
	if !ok {
		return
	}
	var src io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		f, _, err := r.FormFile("poster")
		if err != nil {
			if isBodyTooLarge(err) {
				respond.Error(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "the image is over 1 MB")
				return
			}
			respond.BadRequest(w, r, `send the image as the multipart field "poster"`)
			return
		}
		defer f.Close()
		src = f
	}
	jpg, err := artwork.NormalizeCover(src)
	if err != nil {
		if isBodyTooLarge(err) {
			respond.Error(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "the image is over 1 MB")
			return
		}
		respond.BadRequest(w, r, "the upload isn't an image OnScreen can read (JPEG, PNG, GIF, WebP)")
		return
	}
	if err := h.manual.UpsertCollectionPoster(r.Context(), gen.UpsertCollectionPosterParams{
		CollectionID: col.ID,
		ContentType:  "image/jpeg",
		Data:         jpg,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "collection poster upload", "id", col.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	resp := toCollectionResponse(col)
	if !h.manualDetail(w, r, col, &resp) {
		return
	}
	respond.Success(w, r, resp)
}

func isBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large")
}

// DeletePoster handles DELETE /api/v1/collections/{id}/poster: back to the
// chosen (or first) member's poster.
func (h *CollectionHandler) DeletePoster(w http.ResponseWriter, r *http.Request) {
	col, ok := h.loadManualForAdmin(w, r)
	if !ok {
		return
	}
	if err := h.manual.DeleteCollectionPoster(r.Context(), col.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "collection poster delete", "id", col.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Poster handles GET /api/v1/collections/{id}/poster: a manual collection's
// uploaded cover. Served on the asset-token route group (an <img> can't send
// an Authorization header), to admins and to anyone who can see one of the
// collection's members — the same rule as the collection itself; everyone
// else gets a 404.
func (h *CollectionHandler) Poster(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil || col.Type != collectionTypeManual || h.manual == nil {
		respond.NotFound(w, r)
		return
	}
	if !claims.IsAdmin {
		v, ok := scopeFor(w, r, h.access, "collections: allowed libraries", func(msg string, args ...any) {
			h.logger.ErrorContext(r.Context(), msg, args...)
		})
		if !ok {
			return
		}
		covers, err := h.visibleManualCovers(r.Context(), v)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "collection poster: visibility", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		if _, visible := covers[col.ID]; !visible {
			respond.NotFound(w, r)
			return
		}
	}
	p, err := h.manual.GetCollectionPoster(r.Context(), col.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "collection poster", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	etag := fmt.Sprintf(`"%d"`, p.UpdatedAt.Time.UnixMilli())
	w.Header().Set("ETag", etag)
	// Per-viewer authorization: never shared-cacheable.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Vary", "Authorization, Cookie")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(p.Data)
}
