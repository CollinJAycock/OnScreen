package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/downloads"
)

// ── Converted downloads ──────────────────────────────────────────────────────
//
// The original file downloads from /media/download/{file_id} (unchanged). The
// endpoints here add lower-quality copies: the user picks a rung of the
// transcode ladder, the server encodes the file once into a progressive MP4
// (internal/downloads), and the user fetches it when it's ready. The same
// file at the same quality is encoded once and shared by everyone who asks.
//
// Every gate the original download has applies here too: the server-wide
// "web downloads" toggle, the library ACL, the content-rating ceiling and
// label restriction, the parental watch limit and an admin's playback stop —
// checked when the job is created AND again when the file is fetched, since a
// ready file can be fetched days later. The user's bitrate cap (and, from
// outside the home network, the internet limit) bounds the quality: a user
// held to 4 Mbps can't take the 1080p 8 Mbps copy, at either step.

// DownloadJobService is the job store + cache behind the endpoints.
// Satisfied by *downloads.Service.
type DownloadJobService interface {
	Request(ctx context.Context, userID, itemID, fileID uuid.UUID, quality string) (downloads.Job, bool, error)
	ActiveCount(ctx context.Context, userID uuid.UUID) (int, error)
	Get(ctx context.Context, jobID uuid.UUID) (downloads.Job, error)
	ListForUser(ctx context.Context, userID uuid.UUID) ([]downloads.Job, error)
	Delete(ctx context.Context, userID, jobID uuid.UUID) error
	ReadyFile(ctx context.Context, j downloads.Job) (string, error)
}

// DownloadMediaLookup is the media surface the handler reads.
type DownloadMediaLookup interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// maxActiveDownloadJobs caps a user's jobs still waiting on an encode, so one
// account can't fill the (deliberately narrow) conversion queue.
const maxActiveDownloadJobs = 3

// DownloadsHandler serves /items/{id}/downloads, /downloads/{job}… and
// /users/me/downloads.
type DownloadsHandler struct {
	svc        DownloadJobService
	media      DownloadMediaLookup
	access     LibraryAccessChecker
	dlGate     DownloadGate
	userCaps   UserStreamCapsReader
	remote     RemoteStreamPolicyReader
	watchLimit ItemWatchLimit
	stops      *PlaybackStopGuard
	logger     *slog.Logger
}

// NewDownloadsHandler builds the handler.
func NewDownloadsHandler(svc DownloadJobService, m DownloadMediaLookup, logger *slog.Logger) *DownloadsHandler {
	return &DownloadsHandler{svc: svc, media: m, logger: logger}
}

// WithLibraryAccess attaches the per-library ACL.
func (h *DownloadsHandler) WithLibraryAccess(a LibraryAccessChecker) *DownloadsHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the ACL is attached (startup self-check).
func (h *DownloadsHandler) LibraryAccessWired() bool { return h.access != nil }

// WithDownloadGate attaches the server-wide "web downloads" toggle.
func (h *DownloadsHandler) WithDownloadGate(g DownloadGate) *DownloadsHandler {
	h.dlGate = g
	return h
}

// WithStreamCaps attaches the per-user bitrate caps and the internet limit.
func (h *DownloadsHandler) WithStreamCaps(c UserStreamCapsReader, p RemoteStreamPolicyReader) *DownloadsHandler {
	h.userCaps = c
	h.remote = p
	return h
}

// WithWatchLimit attaches the parental watch limit.
func (h *DownloadsHandler) WithWatchLimit(wl ItemWatchLimit) *DownloadsHandler {
	h.watchLimit = wl
	return h
}

// WithPlaybackStops attaches the admin playback-stop guard.
func (h *DownloadsHandler) WithPlaybackStops(g *PlaybackStopGuard) *DownloadsHandler {
	h.stops = g
	return h
}

// DownloadQualityJSON is one entry of the download menu.
type DownloadQualityJSON struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	BitrateKbps int    `json:"bitrate_kbps"`
	MaxHeight   int    `json:"max_height"`
	// Allowed is false when the user's bitrate cap (or, from outside the
	// home network, the internet limit) is below this quality.
	Allowed bool `json:"allowed"`
}

// DownloadOptionsJSON is GET /items/{id}/downloads.
type DownloadOptionsJSON struct {
	FileID    string                `json:"file_id"`
	Qualities []DownloadQualityJSON `json:"qualities"`
	// BitrateCapKbps is the cap that applies to this request; 0 = none.
	BitrateCapKbps int `json:"bitrate_cap_kbps,omitempty"`
}

// DownloadJobJSON is one converted download.
type DownloadJobJSON struct {
	ID           string `json:"id"`
	ItemID       string `json:"item_id"`
	FileID       string `json:"file_id"`
	Title        string `json:"title"`
	ItemType     string `json:"item_type"`
	Quality      string `json:"quality"`
	QualityLabel string `json:"quality_label"`
	// Status: queued | running | ready | failed | expired.
	Status string `json:"status"`
	// Progress of the encode, 0..1.
	Progress    float64    `json:"progress"`
	Error       string     `json:"error,omitempty"`
	SizeBytes   *int64     `json:"size_bytes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// ExpiresAt is when a ready file may leave the cache (fetching it or
	// asking for it again moves this out).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// FileURL fetches the MP4 once Status is ready (cookie, Bearer or an
	// asset ?token=).
	FileURL string `json:"file_url,omitempty"`
}

func toDownloadJobJSON(j downloads.Job) DownloadJobJSON {
	out := DownloadJobJSON{
		ID:          j.ID.String(),
		ItemID:      j.ItemID.String(),
		FileID:      j.FileID.String(),
		Title:       j.ItemTitle,
		ItemType:    j.ItemType,
		Quality:     j.Quality,
		Status:      j.Status,
		Progress:    j.Progress,
		Error:       j.Error,
		SizeBytes:   j.SizeBytes,
		CreatedAt:   j.CreatedAt,
		CompletedAt: j.CompletedAt,
		ExpiresAt:   j.ExpiresAt,
	}
	if q, ok := downloads.QualityByID(j.Quality); ok {
		out.QualityLabel = q.Label
	}
	if j.Status == downloads.StatusReady {
		out.FileURL = "/api/v1/downloads/" + j.ID.String() + "/file"
	}
	return out
}

// downloadsEnabled applies the server-wide toggle.
func (h *DownloadsHandler) downloadsEnabled(w http.ResponseWriter, r *http.Request) bool {
	if h.dlGate != nil && !h.dlGate.WebDownloadsEnabled(r.Context()) {
		respond.Error(w, r, http.StatusForbidden, "DOWNLOADS_DISABLED",
			"file downloads are disabled on this server")
		return false
	}
	return true
}

// gateItem applies the item-level gates a download shares with
// /media/download: library ACL, rating ceiling + label restriction, watch
// limit, admin stop. Writes the response and returns nil when refused.
func (h *DownloadsHandler) gateItem(w http.ResponseWriter, r *http.Request, claims *auth.Claims, itemID uuid.UUID) *media.Item {
	ctx := r.Context()
	item, err := h.media.GetItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return nil
		}
		h.logger.ErrorContext(ctx, "downloads: get item", "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return nil
	}
	if h.access != nil {
		ok, err := h.access.CanAccessLibrary(ctx, claims.UserID, item.LibraryID, claims.IsAdmin)
		if err != nil {
			h.logger.ErrorContext(ctx, "downloads: library access", "library_id", item.LibraryID, "err", err)
			respond.InternalError(w, r)
			return nil
		}
		if !ok {
			respond.NotFound(w, r)
			return nil
		}
	}
	if viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(ctx, claims, item.ID, cr) {
			respond.Forbidden(w, r)
			return nil
		}
	}
	if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
		return nil
	}
	if playbackStopBlocks(w, r, h.stops, claims.UserID, item.ID) {
		return nil
	}
	return item
}

// bitrateCap is the cap this request is held to: the user's cap, plus the
// user's and the server's internet limits when the client is remote.
// Fail-open on a cap-read error, as transcode start does.
func (h *DownloadsHandler) bitrateCap(r *http.Request, userID uuid.UUID, file *media.File) int {
	var caps StreamCaps
	if h.userCaps != nil {
		c, err := h.userCaps.GetStreamCaps(r.Context(), userID)
		if err != nil {
			h.logger.WarnContext(r.Context(), "downloads: read stream caps", "user_id", userID, "err", err)
		} else {
			caps = c
		}
	}
	return resolveStreamCap(r.Context(), caps, h.remote, audit.ClientIP(r), file).Kbps
}

// pickFile resolves the file a conversion encodes: the requested one (which
// must belong to the item) or the item's best, and for a stacked movie its
// first part, so cd1 and cd2 requests share one joined encode.
func (h *DownloadsHandler) pickFile(w http.ResponseWriter, r *http.Request, itemID uuid.UUID, fileID string) *media.File {
	ctx := r.Context()
	files, err := h.media.GetFiles(ctx, itemID)
	if err != nil {
		h.logger.ErrorContext(ctx, "downloads: list files", "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return nil
	}
	var file *media.File
	if fileID != "" {
		fid, err := uuid.Parse(fileID)
		if err != nil {
			respond.BadRequest(w, r, "invalid file_id")
			return nil
		}
		for i := range files {
			if files[i].ID == fid {
				file = &files[i]
				break
			}
		}
	} else if len(files) > 0 {
		file = &files[0] // sorted best quality first
	}
	if file == nil {
		respond.NotFound(w, r)
		return nil
	}
	if parts := media.Stack(files, *file); parts != nil {
		first := parts[0]
		file = &first
	}
	return file
}

// Options handles GET /api/v1/items/{id}/downloads[?file_id=]: the converted
// qualities and which of them this user may take.
func (h *DownloadsHandler) Options(w http.ResponseWriter, r *http.Request) {
	if !h.downloadsEnabled(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.gateItem(w, r, claims, itemID) == nil {
		return
	}
	file := h.pickFile(w, r, itemID, r.URL.Query().Get("file_id"))
	if file == nil {
		return
	}
	capKbps := h.bitrateCap(r, claims.UserID, file)
	out := DownloadOptionsJSON{FileID: file.ID.String(), BitrateCapKbps: capKbps}
	if downloads.Convertible(file) == nil {
		for _, q := range downloads.Qualities {
			out.Qualities = append(out.Qualities, DownloadQualityJSON{
				ID: q.ID, Label: q.Label, BitrateKbps: q.Profile.Bitrate, MaxHeight: q.Profile.MaxHeight,
				Allowed: q.AllowedUnder(capKbps),
			})
		}
	}
	if out.Qualities == nil {
		out.Qualities = []DownloadQualityJSON{}
	}
	respond.Success(w, r, out)
}

type createDownloadRequest struct {
	Quality string `json:"quality"`
	FileID  string `json:"file_id"`
}

// Create handles POST /api/v1/items/{id}/downloads {quality, file_id?}:
// creates the user's converted-download job, or returns the existing one
// (200 instead of 201) when they already asked for this file at this quality.
func (h *DownloadsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.downloadsEnabled(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	var body createDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.Quality == "original" {
		respond.BadRequest(w, r, "the original file downloads directly from /media/download/{file_id}")
		return
	}
	q, ok := downloads.QualityByID(body.Quality)
	if !ok {
		respond.ValidationError(w, r, "quality must be one of 1080p_8mbps, 720p_4mbps, 720p_2mbps, 480p_1mbps")
		return
	}
	if h.gateItem(w, r, claims, itemID) == nil {
		return
	}
	file := h.pickFile(w, r, itemID, body.FileID)
	if file == nil {
		return
	}
	if err := downloads.Convertible(file); err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "NOT_CONVERTIBLE",
			"this file can't be converted: "+err.Error())
		return
	}
	if capKbps := h.bitrateCap(r, claims.UserID, file); !q.AllowedUnder(capKbps) {
		respond.Error(w, r, http.StatusForbidden, "QUALITY_ABOVE_LIMIT",
			"this quality is above your streaming limit; pick a lower one")
		return
	}
	ctx := r.Context()
	if n, err := h.svc.ActiveCount(ctx, claims.UserID); err != nil {
		h.logger.ErrorContext(ctx, "downloads: count active", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	} else if n >= maxActiveDownloadJobs {
		respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_DOWNLOADS",
			"you already have downloads being prepared; wait for one to finish")
		return
	}
	job, created, err := h.svc.Request(ctx, claims.UserID, itemID, file.ID, q.ID)
	if err != nil {
		h.logger.ErrorContext(ctx, "downloads: request", "item_id", itemID, "file_id", file.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if created {
		respond.Created(w, r, toDownloadJobJSON(job))
		return
	}
	respond.Success(w, r, toDownloadJobJSON(job))
}

// ownJob loads the {job} path param's job and checks it is the caller's.
func (h *DownloadsHandler) ownJob(w http.ResponseWriter, r *http.Request, claims *auth.Claims) (downloads.Job, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "job"))
	if err != nil {
		respond.BadRequest(w, r, "invalid download id")
		return downloads.Job{}, false
	}
	j, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, downloads.ErrNotFound) {
			respond.NotFound(w, r)
			return downloads.Job{}, false
		}
		h.logger.ErrorContext(r.Context(), "downloads: get job", "job_id", id, "err", err)
		respond.InternalError(w, r)
		return downloads.Job{}, false
	}
	if j.UserID != claims.UserID {
		respond.NotFound(w, r) // someone else's: indistinguishable from missing
		return downloads.Job{}, false
	}
	return j, true
}

// Get handles GET /api/v1/downloads/{job}: status and progress.
func (h *DownloadsHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	j, ok := h.ownJob(w, r, claims)
	if !ok {
		return
	}
	respond.Success(w, r, toDownloadJobJSON(j))
}

// ListMine handles GET /api/v1/users/me/downloads: the caller's jobs, newest
// first.
func (h *DownloadsHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	jobs, err := h.svc.ListForUser(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "downloads: list", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]DownloadJobJSON, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toDownloadJobJSON(j))
	}
	respond.Success(w, r, out)
}

// Delete handles DELETE /api/v1/downloads/{job}: removes the job, cancelling
// its encode when nobody else is waiting for it.
func (h *DownloadsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "job"))
	if err != nil {
		respond.BadRequest(w, r, "invalid download id")
		return
	}
	if err := h.svc.Delete(r.Context(), claims.UserID, id); err != nil {
		if errors.Is(err, downloads.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "downloads: delete", "job_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// File handles GET /api/v1/downloads/{job}/file: the finished MP4, with Range
// support and an attachment filename. Re-applies every gate — the file may be
// fetched long after the job was created, from somewhere else.
func (h *DownloadsHandler) File(w http.ResponseWriter, r *http.Request) {
	if !h.downloadsEnabled(w, r) {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	j, ok := h.ownJob(w, r, claims)
	if !ok {
		return
	}
	item := h.gateItem(w, r, claims, j.ItemID)
	if item == nil {
		return
	}
	if q, ok := downloads.QualityByID(j.Quality); ok {
		file, _ := h.media.GetFile(r.Context(), j.FileID)
		if !q.AllowedUnder(h.bitrateCap(r, claims.UserID, file)) {
			respond.Error(w, r, http.StatusForbidden, "QUALITY_ABOVE_LIMIT",
				"this quality is above your streaming limit from here")
			return
		}
	}
	path, err := h.svc.ReadyFile(r.Context(), j)
	if err != nil {
		if errors.Is(err, downloads.ErrNotReady) {
			respond.Error(w, r, http.StatusConflict, "DOWNLOAD_NOT_READY",
				"this download is not ready (status "+j.Status+")")
			return
		}
		h.logger.ErrorContext(r.Context(), "downloads: ready file", "job_id", j.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		respond.Error(w, r, http.StatusConflict, "DOWNLOAD_NOT_READY", "this download is no longer available")
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		respond.InternalError(w, r)
		return
	}

	label := j.Quality
	if q, ok := downloads.QualityByID(j.Quality); ok {
		label = q.Label
	}
	name := downloadFilename(item.Title+" ("+label+")", "x.mp4")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+name+`"; filename*=UTF-8''`+url.PathEscape(name))
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	// Large files outlive the JSON handlers' write deadline (as in
	// DownloadFile).
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
