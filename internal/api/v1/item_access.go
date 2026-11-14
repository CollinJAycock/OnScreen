package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// itemGetter is the lookup every item-scoped gate starts from. Each
// handler's media service satisfies it.
type itemGetter interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
}

// loadVisibleItem fetches item id and applies the gates every item-scoped
// route shares, in one order with one set of answers:
//
//   - no signed-in caller: 401;
//   - no such item: 404 (a lookup failure other than not-found is logged
//     and answered 500, not passed off as a missing item);
//   - the item's library isn't visible to the caller: 404;
//   - the item is above the caller's content-rating ceiling, or carries a
//     label the caller's profile excludes: 404.
//
// "Exists but you may not see it" is 404, the same as a missing id, so a
// restricted profile can't probe which ids exist. On refusal the response is
// written and ok is false.
func loadVisibleItem(w http.ResponseWriter, r *http.Request, items itemGetter, access LibraryAccessChecker, logger *slog.Logger, id uuid.UUID) (*media.Item, bool) {
	return loadGatedItem(w, r, items, access, logger, id, respond.NotFound)
}

// loadVisibleItem403 is loadVisibleItem answering 403, not 404, when the
// caller's rating ceiling or label restriction hides the item (a library the
// caller can't see is still 404). Only for the routes whose first-party
// clients turn that 403 into their "outside your content rating limit"
// message — Android TV (PlaybackViewModel.playbackErrorMessage) and Android
// phone (PlayerViewModel.failureMessage) read any other 403 from the item
// fetch and transcode Start that way, and show a raw "HTTP 404" otherwise:
// GET /items/{id}, transcode Start and the playback decision. Every other
// item-scoped route uses loadVisibleItem.
func loadVisibleItem403(w http.ResponseWriter, r *http.Request, items itemGetter, access LibraryAccessChecker, logger *slog.Logger, id uuid.UUID) (*media.Item, bool) {
	return loadGatedItem(w, r, items, access, logger, id, respond.Forbidden)
}

func loadGatedItem(w http.ResponseWriter, r *http.Request, items itemGetter, access LibraryAccessChecker, logger *slog.Logger, id uuid.UUID, restricted func(http.ResponseWriter, *http.Request)) (*media.Item, bool) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return nil, false
	}
	item, err := items.GetItem(r.Context(), id)
	if err != nil {
		respond.NotFoundOrServerError(w, r, logger, err, "load item", media.ErrNotFound)
		return nil, false
	}
	if item == nil { // a (nil, nil) lookup is this codebase's "not found" too
		respond.NotFound(w, r)
		return nil, false
	}
	if !libraryVisible(w, r, claims, access, logger, item.LibraryID) {
		return nil, false
	}
	if !ratingPermits(r.Context(), claims, item) {
		restricted(w, r)
		return nil, false
	}
	return item, true
}

// ratingPermits is the content-rating ceiling + label restriction half.
func ratingPermits(ctx context.Context, claims *auth.Claims, item *media.Item) bool {
	if !viewerRestricted(claims) {
		return true
	}
	cr := ""
	if item.ContentRating != nil {
		cr = *item.ContentRating
	}
	return itemPermitted(ctx, claims, item.ID, cr)
}

// libraryVisible is the library-ACL half of loadVisibleItem. It fails closed: a
// handler built without its checker (see SecurityDeps) refuses every
// non-admin rather than skipping the check, and says so in the log.
func libraryVisible(w http.ResponseWriter, r *http.Request, claims *auth.Claims, access LibraryAccessChecker, logger *slog.Logger, libraryID uuid.UUID) bool {
	if access == nil {
		if claims.IsAdmin {
			return true
		}
		if logger == nil {
			logger = slog.Default()
		}
		logger.ErrorContext(r.Context(), "library access checker not wired — refusing non-admin", "library_id", libraryID)
		respond.NotFound(w, r)
		return false
	}
	ok, err := access.CanAccessLibrary(r.Context(), claims.UserID, libraryID, claims.IsAdmin)
	if err != nil {
		respond.ServerError(w, r, logger, err, "check library access", "library_id", libraryID)
		return false
	}
	if !ok {
		respond.NotFound(w, r)
		return false
	}
	return true
}
