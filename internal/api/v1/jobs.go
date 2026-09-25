package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
)

// JobsScanLister returns the IDs of libraries currently being scanned.
// Implemented by the scanEnqueuer in cmd/server.
type JobsScanLister interface {
	InFlightScans() []uuid.UUID
}

// JobsLibraryNamer maps a library UUID to its name for display. Optional —
// when nil or when a name lookup fails, the response carries the UUID
// without a name and the frontend falls back to that.
type JobsLibraryNamer interface {
	NameOf(ctx context.Context, id uuid.UUID) (string, bool)
}

// JobsCounters surfaces snapshot counts for the operator-facing status
// signals on /api/v1/jobs. Both methods are pure SELECT count(*) reads
// against indexed predicates — cheap to call on every poll.
type JobsCounters interface {
	CountItemsMissingArt(ctx context.Context) (int, error)
	CountUnmatchedItems(ctx context.Context) (int, error)
}

// JobsHandler answers GET /api/v1/jobs. The endpoint is the data feed for
// the frontend status banner: "scanning Movies…", "12 shows still need
// artwork", "3 items need a manual match". Without it the user has to
// guess whether enrichment is in flight or has stalled — which is the
// QA experience that motivated this whole sequence of fixes.
type JobsHandler struct {
	scans    JobsScanLister
	counters JobsCounters
	libNamer JobsLibraryNamer
	access   LibraryAccessChecker
	logger   *slog.Logger
}

// NewJobsHandler constructs a JobsHandler. libNamer is optional.
func NewJobsHandler(scans JobsScanLister, counters JobsCounters, libNamer JobsLibraryNamer, logger *slog.Logger) *JobsHandler {
	return &JobsHandler{scans: scans, counters: counters, libNamer: libNamer, logger: logger}
}

// WithLibraryAccess scopes the in-flight scan list to libraries the caller can
// see. Without it every signed-in user — a managed child profile included — got
// the name of every library being scanned, private ones too.
func (h *JobsHandler) WithLibraryAccess(access LibraryAccessChecker) *JobsHandler {
	h.access = access
	return h
}

// scanInFlight is one entry in the response's `scans` array.
type scanInFlight struct {
	LibraryID   string `json:"library_id"`
	LibraryName string `json:"library_name,omitempty"`
}

// jobsResponse is the wire shape. Kept narrow on purpose — extra fields
// can land here without breaking clients, but every field that's here
// has a concrete UI use already in mind.
type jobsResponse struct {
	Scans      []scanInFlight `json:"scans"`
	MissingArt int            `json:"missing_art_count"`
	Unmatched  int            `json:"unmatched_count"`
}

// Get handles GET /api/v1/jobs. Snapshot semantics — a scan that
// completes between collection and response is fine, the next poll
// catches up. Counter queries that fail degrade to 0 with a logged
// error rather than failing the whole response, since a transient DB
// hiccup shouldn't black out the status banner.
func (h *JobsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	scans := []scanInFlight{}
	visible := h.visibleLibraries(r)
	if h.scans != nil {
		for _, id := range h.scans.InFlightScans() {
			if visible != nil && !visible(id) {
				continue
			}
			entry := scanInFlight{LibraryID: id.String()}
			if h.libNamer != nil {
				if name, ok := h.libNamer.NameOf(ctx, id); ok {
					entry.LibraryName = name
				}
			}
			scans = append(scans, entry)
		}
	}

	missingArt := 0
	unmatched := 0
	if h.counters != nil {
		if n, err := h.counters.CountItemsMissingArt(ctx); err != nil {
			h.logger.WarnContext(ctx, "jobs: count missing art failed", "err", err)
		} else {
			missingArt = n
		}
		if n, err := h.counters.CountUnmatchedItems(ctx); err != nil {
			h.logger.WarnContext(ctx, "jobs: count unmatched failed", "err", err)
		} else {
			unmatched = n
		}
	}

	respond.Success(w, r, jobsResponse{
		Scans:      scans,
		MissingArt: missingArt,
		Unmatched:  unmatched,
	})
}

// visibleLibraries returns a filter for the caller's libraries, or nil when no
// filtering applies (no access checker wired, or an admin caller). It fails
// closed: a missing caller or an ACL lookup error hides every scan.
func (h *JobsHandler) visibleLibraries(r *http.Request) func(uuid.UUID) bool {
	if h.access == nil {
		return nil
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return func(uuid.UUID) bool { return false }
	}
	if claims.IsAdmin {
		return nil
	}
	allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, false)
	if err != nil {
		h.logger.WarnContext(r.Context(), "jobs: library ACL lookup failed", "err", err)
		return func(uuid.UUID) bool { return false }
	}
	return func(id uuid.UUID) bool {
		_, ok := allowed[id]
		return ok
	}
}
