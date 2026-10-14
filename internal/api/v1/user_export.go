package v1

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/userexport"
)

// UserExportHandler serves "Download my data": a ZIP of JSON files holding
// what the server keeps about one user (see internal/userexport for the
// contents and what is never included).
//
//	POST /api/v1/users/me/export    the caller's own data
//	POST /api/v1/users/{id}/export  admin: any user's (a data request)
//
// Both stream the archive as the response body. Personal API keys are
// refused (RefuseAPIKey at the router, and again here): this is account
// data, so it takes a signed-in session. Every export is audit-logged.
//
// Items in the archive are resolved under the CALLER's visibility (library
// access, rating ceiling, label restriction); one they can't see is written
// as its id only. An admin's export of another user therefore names every
// item; a restricted profile's own export doesn't name what is now hidden
// from it.
type UserExportHandler struct {
	db     userexport.Store
	access LibraryAccessChecker
	audit  *audit.Logger
	logger *slog.Logger
}

// NewUserExportHandler builds the handler.
func NewUserExportHandler(db userexport.Store, logger *slog.Logger) *UserExportHandler {
	return &UserExportHandler{db: db, logger: logger}
}

// WithLibraryAccess wires the per-user library filter used to decide which
// item references the archive names.
func (h *UserExportHandler) WithLibraryAccess(a LibraryAccessChecker) *UserExportHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired.
func (h *UserExportHandler) LibraryAccessWired() bool { return h.access != nil }

// WithAudit attaches the audit logger.
func (h *UserExportHandler) WithAudit(a *audit.Logger) *UserExportHandler {
	h.audit = a
	return h
}

// ExportMine handles POST /api/v1/users/me/export.
func (h *UserExportHandler) ExportMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	h.export(w, r, claims, claims.UserID)
}

// ExportUser handles POST /api/v1/users/{id}/export (admin only at the
// router).
func (h *UserExportHandler) ExportUser(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	target, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	h.export(w, r, claims, target)
}

func (h *UserExportHandler) export(w http.ResponseWriter, r *http.Request, claims *auth.Claims, target uuid.UUID) {
	// Belt and braces behind the router's RefuseAPIKey: a key (or a purpose
	// token minted from one) must never pull a whole account's data.
	if claims.IsAPIKey() || claims.Purpose != "" {
		respond.Error(w, r, http.StatusForbidden, "API_KEY_NOT_ALLOWED",
			"API keys can't be used here; sign in to do this")
		return
	}
	vis := userexport.Visibility{
		MaxRatingRank: maxRatingRankFromClaims(claims.MaxContentRating),
		BlockedLabels: claims.LabelFilter().BlockedArg(),
		AllowedLabels: claims.LabelFilter().AllowedArg(),
	}
	if h.access != nil {
		allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "user export: allowed libraries", "err", err)
			respond.InternalError(w, r)
			return
		}
		if allowed != nil {
			vis.LibraryIDs = make([]uuid.UUID, 0, len(allowed))
			for id := range allowed {
				vis.LibraryIDs = append(vis.LibraryIDs, id)
			}
		}
	}

	exp := userexport.New(h.db, vis)
	snap, err := exp.Collect(r.Context(), target)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "user export: collect", "user_id", target, "err", err)
		respond.InternalError(w, r)
		return
	}

	if h.audit != nil {
		detail := map[string]any{}
		if target != claims.UserID {
			detail["by_admin"] = true
		}
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserDataExport, target.String(), detail, audit.ClientIP(r))
	}

	name := downloadFilename("onscreen-data-"+snap.Username()+"-"+time.Now().UTC().Format("2006-01-02"), "export.zip")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+name+`"; filename*=UTF-8''`+url.PathEscape(name))
	w.Header().Set("Cache-Control", "private, no-store")
	// A large history takes a while to page out; give it a bounded window
	// rather than the JSON handlers' 60 s.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
	}
	if err := exp.Write(r.Context(), w, snap); err != nil {
		h.logger.ErrorContext(r.Context(), "user export: write", "user_id", target, "err", err)
		// The archive is already half sent. Drop the connection so the
		// client sees a failed download, not a truncated ZIP.
		panic(http.ErrAbortHandler)
	}
}
