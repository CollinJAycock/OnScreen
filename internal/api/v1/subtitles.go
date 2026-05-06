package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// SubtitleService is the contract the handler needs from the subtitle service.
// Defined here so tests can mock without pulling in the real opensubtitles client.
type SubtitleService interface {
	Search(ctx context.Context, opts subtitles.SearchOpts) ([]opensubtitles.SearchResult, error)
	Download(ctx context.Context, opts subtitles.DownloadOpts) (gen.ExternalSubtitle, error)
	List(ctx context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error)
	Get(ctx context.Context, id uuid.UUID) (gen.ExternalSubtitle, error)
	Delete(ctx context.Context, id uuid.UUID) error
	OCRStream(ctx context.Context, opts subtitles.OCROpts) (gen.ExternalSubtitle, error)
}

// SubtitleHandler exposes search/download/list endpoints for external subtitles.
// Library access is enforced via the items the subtitles attach to.
type SubtitleHandler struct {
	svc     SubtitleService
	media   ItemMediaService
	access  LibraryAccessChecker
	ocrJobs *subtitles.OCRJobStore
	logger  *slog.Logger
	// ocrInFlight tracks concurrent OCR jobs per user. Tesseract is
	// CPU-heavy; without a cap a single user can spawn N concurrent
	// jobs and pin every core. Decremented in runOCRJob's defer so
	// the slot frees up regardless of how the goroutine exits
	// (success, panic, ctx cancel).
	ocrInFlight   map[uuid.UUID]int
	ocrInFlightMu sync.Mutex
}

// MaxConcurrentOCRJobsPerUser caps how many simultaneous OCR jobs a
// single authenticated user may run. Tesseract is CPU-heavy and runs
// for tens of seconds per subtitle stream; this prevents a runaway
// client (or hostile script) from consuming every core.
const MaxConcurrentOCRJobsPerUser = 2

// NewSubtitleHandler constructs a SubtitleHandler. The OCR job store is
// created internally — it's per-process state with no shared dependency
// to surface in the constructor signature.
func NewSubtitleHandler(svc SubtitleService, media ItemMediaService, logger *slog.Logger) *SubtitleHandler {
	return &SubtitleHandler{
		svc:         svc,
		media:       media,
		ocrJobs:     subtitles.NewOCRJobStore(),
		logger:      logger,
		ocrInFlight: make(map[uuid.UUID]int),
	}
}

// WithLibraryAccess wires per-user library filtering.
func (h *SubtitleHandler) WithLibraryAccess(a LibraryAccessChecker) *SubtitleHandler {
	h.access = a
	return h
}

// SearchResultJSON is the API representation of a single search result.
type SearchResultJSON struct {
	ProviderFileID  int     `json:"provider_file_id"`
	FileName        string  `json:"file_name"`
	Language        string  `json:"language"`
	Release         string  `json:"release"`
	HearingImpaired bool    `json:"hearing_impaired"`
	HD              bool    `json:"hd"`
	FromTrusted     bool    `json:"from_trusted"`
	Rating          float32 `json:"rating"`
	DownloadCount   int32   `json:"download_count"`
	UploaderName    string  `json:"uploader_name"`
}

// ExternalSubtitleJSON is the API representation of a stored external subtitle.
type ExternalSubtitleJSON struct {
	ID       string  `json:"id"`
	FileID   string  `json:"file_id"`
	Language string  `json:"language"`
	Title    *string `json:"title,omitempty"`
	Forced   bool    `json:"forced"`
	SDH      bool    `json:"sdh"`
	Source   string  `json:"source"`
	SourceID *string `json:"source_id,omitempty"`
	URL      string  `json:"url"`
}

// Search handles GET /api/v1/items/{id}/subtitles/search?lang=en&query=...
// The item is used to derive the title/year/episode metadata sent upstream.
func (h *SubtitleHandler) Search(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}

	item, err := h.media.GetItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: get item", "id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}

	opts := subtitles.SearchOpts{
		Languages: r.URL.Query().Get("lang"),
	}
	if q := r.URL.Query().Get("query"); q != "" {
		opts.Query = q
	} else {
		opts.Query = item.Title
	}
	if item.Year != nil {
		opts.Year = *item.Year
	}
	if item.IMDBID != nil {
		opts.IMDBID = *item.IMDBID
	}
	if item.TMDBID != nil {
		opts.TMDBID = *item.TMDBID
	}
	// For episodes, use the show title and season/episode numbers if we can resolve them.
	if item.Type == "episode" {
		if season, episode, ok := h.deriveEpisodeNumbers(r, item); ok {
			opts.Season = season
			opts.Episode = episode
			if showTitle := h.deriveShowTitle(r, item); showTitle != "" {
				opts.Query = showTitle
			}
		}
	}

	results, err := h.svc.Search(r.Context(), opts)
	if err != nil {
		if errors.Is(err, subtitles.ErrNoProvider) {
			respond.JSON(w, r, http.StatusServiceUnavailable, map[string]string{
				"error": "subtitle provider not configured",
			})
			return
		}
		h.logger.WarnContext(r.Context(), "subtitles: search", "id", itemID, "err", err)
		respond.JSON(w, r, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	out := make([]SearchResultJSON, 0, len(results))
	for _, x := range results {
		out = append(out, SearchResultJSON{
			ProviderFileID:  x.FileID,
			FileName:        x.FileName,
			Language:        x.Language,
			Release:         x.Release,
			HearingImpaired: x.HearingImpaired,
			HD:              x.HD,
			FromTrusted:     x.FromTrusted,
			Rating:          x.Rating,
			DownloadCount:   x.DownloadCount,
			UploaderName:    x.UploaderName,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Download handles POST /api/v1/items/{id}/subtitles/download
// Body: { file_id, provider_file_id, language, title?, hearing_impaired?, rating?, download_count? }
// Persists the subtitle to disk + DB and returns the resulting row.
func (h *SubtitleHandler) Download(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}

	item, err := h.media.GetItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: get item", "id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}

	var body struct {
		FileID          string  `json:"file_id"`
		ProviderFileID  int     `json:"provider_file_id"`
		Language        string  `json:"language"`
		Title           string  `json:"title"`
		HearingImpaired bool    `json:"hearing_impaired"`
		Rating          float32 `json:"rating"`
		DownloadCount   int32   `json:"download_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	fileID, err := uuid.Parse(body.FileID)
	if err != nil || body.ProviderFileID == 0 || body.Language == "" {
		respond.BadRequest(w, r, "file_id, provider_file_id, and language are required")
		return
	}

	// Verify the file belongs to the item to prevent attaching subtitles
	// from one item to another item's files.
	files, err := h.media.GetFiles(r.Context(), itemID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "subtitles: list files", "id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !fileBelongsToItem(fileID, files) {
		respond.NotFound(w, r)
		return
	}

	row, err := h.svc.Download(r.Context(), subtitles.DownloadOpts{
		FileID:          fileID,
		ProviderFileID:  body.ProviderFileID,
		Language:        body.Language,
		Title:           body.Title,
		HearingImpaired: body.HearingImpaired,
		Rating:          body.Rating,
		DownloadCount:   body.DownloadCount,
	})
	if err != nil {
		if errors.Is(err, subtitles.ErrNoProvider) {
			respond.JSON(w, r, http.StatusServiceUnavailable, map[string]string{
				"error": "subtitle provider not configured",
			})
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: download", "id", itemID, "err", err)
		respond.JSON(w, r, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	respond.Created(w, r, toExternalSubtitleJSON(row))
}

// ocrJobJSON is the response shape for both the create-job (POST) and
// poll-job (GET) endpoints. Result is omitempty so a still-running job
// doesn't carry a null subtitle field; status drives the client's
// decision to render success vs error vs spinner.
type ocrJobJSON struct {
	JobID       string                `json:"job_id"`
	Status      subtitles.OCRJobStatus `json:"status"`
	FileID      string                `json:"file_id"`
	StreamIndex int                   `json:"stream_index"`
	StartedAt   string                `json:"started_at"`
	CompletedAt string                `json:"completed_at,omitempty"`
	Error       string                `json:"error,omitempty"`
	Subtitle    *ExternalSubtitleJSON `json:"subtitle,omitempty"`
}

func toOCRJobJSON(job subtitles.OCRJob) ocrJobJSON {
	out := ocrJobJSON{
		JobID:       job.ID,
		Status:      job.Status,
		FileID:      job.FileID.String(),
		StreamIndex: job.StreamIndex,
		StartedAt:   job.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if job.CompletedAt != nil {
		out.CompletedAt = job.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if job.Error != "" {
		out.Error = job.Error
	}
	if job.Result != nil {
		s := toExternalSubtitleJSON(*job.Result)
		out.Subtitle = &s
	}
	return out
}

// OCR handles POST /api/v1/items/{id}/subtitles/ocr.
// Body: { file_id, stream_index, language?, title?, forced?, sdh? }
//
// Returns 202 Accepted with a job descriptor. The OCR pipeline runs
// in a server-lifetime goroutine using context.Background(), so a
// client disconnect (e.g. Cloudflare Tunnel free-tier 100 s response
// timeout, browser tab close, fetch() abort) doesn't kill tesseract.
// Clients poll GET /api/v1/items/{id}/subtitles/ocr/{jobId} for the
// terminal state and the resulting external_subtitles row.
//
// This is a v2.1 conversion from the synchronous v2.0 endpoint, which
// 524'd behind any reverse proxy with a sub-multi-minute response
// timeout for feature-length PGS tracks.
func (h *SubtitleHandler) OCR(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	item, err := h.media.GetItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}

	var body struct {
		FileID      string `json:"file_id"`
		StreamIndex int    `json:"stream_index"`
		Language    string `json:"language"`
		Title       string `json:"title"`
		Forced      bool   `json:"forced"`
		SDH         bool   `json:"sdh"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	fileID, err := uuid.Parse(body.FileID)
	if err != nil {
		respond.BadRequest(w, r, "invalid file_id")
		return
	}

	files, err := h.media.GetFiles(r.Context(), itemID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "ocr: list files", "id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	var inputPath string
	for _, f := range files {
		if f.ID == fileID {
			inputPath = f.FilePath
			break
		}
	}
	if inputPath == "" {
		respond.NotFound(w, r)
		return
	}

	// Per-user concurrent-OCR cap. Tesseract is CPU-heavy and runs
	// for tens of seconds per subtitle stream — without this cap a
	// runaway client (or hostile script) can spawn arbitrary
	// concurrent OCR jobs and pin every core. The rate-limit on the
	// route gates start frequency; this gates concurrent in-flight.
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	h.ocrInFlightMu.Lock()
	if h.ocrInFlight[claims.UserID] >= MaxConcurrentOCRJobsPerUser {
		h.ocrInFlightMu.Unlock()
		respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_OCR_JOBS",
			"you already have the maximum number of OCR jobs running; wait for one to finish")
		return
	}
	h.ocrInFlight[claims.UserID]++
	h.ocrInFlightMu.Unlock()

	job, err := h.ocrJobs.Create(fileID, body.StreamIndex)
	if err != nil {
		// Roll back the in-flight increment on early failure.
		h.ocrInFlightMu.Lock()
		h.ocrInFlight[claims.UserID]--
		h.ocrInFlightMu.Unlock()
		h.logger.ErrorContext(r.Context(), "ocr: create job", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Spawn the OCR work with a server-lifetime context so client
	// disconnects (Cloudflare 100 s, browser cancel, network blip)
	// don't kill the tesseract subprocess. The job store is the
	// single source of truth; the client polls it on its own clock.
	uid := claims.UserID
	go func() {
		defer func() {
			h.ocrInFlightMu.Lock()
			h.ocrInFlight[uid]--
			if h.ocrInFlight[uid] <= 0 {
				delete(h.ocrInFlight, uid)
			}
			h.ocrInFlightMu.Unlock()
		}()
		h.runOCRJob(job.ID, subtitles.OCROpts{
			FileID:         fileID,
			InputPath:      inputPath,
			AbsStreamIndex: body.StreamIndex,
			Language:       body.Language,
			Title:          body.Title,
			Forced:         body.Forced,
			SDH:            body.SDH,
		})
	}()

	respond.Accepted(w, r, toOCRJobJSON(*job))
}

// runOCRJob is the goroutine that actually runs tesseract and writes
// the result back to the job store. context.Background() keeps the
// pipeline alive past the original HTTP request — exactly the bug
// the v2.1 conversion exists to fix.
func (h *SubtitleHandler) runOCRJob(jobID string, opts subtitles.OCROpts) {
	ctx := context.Background()
	row, err := h.svc.OCRStream(ctx, opts)
	if err != nil {
		h.logger.ErrorContext(ctx, "ocr: run", "job_id", jobID, "stream", opts.AbsStreamIndex, "err", err)
		h.ocrJobs.Fail(jobID, err)
		return
	}
	h.ocrJobs.Complete(jobID, row)
	h.logger.InfoContext(ctx, "ocr: complete", "job_id", jobID, "stream", opts.AbsStreamIndex, "subtitle_id", row.ID)
}

// OCRStatus handles GET /api/v1/items/{id}/subtitles/ocr/{jobId}.
// Returns the job's current state. Clients poll until status is
// "completed" or "failed"; "running" means try again in a few seconds.
//
// Library access is checked the same way as the POST so a non-admin
// can't poll a job that wasn't theirs to start. Returns 404 for unknown
// or expired job ids — same shape regardless of "never existed" vs
// "evicted by TTL" so the polling client doesn't need to distinguish.
func (h *SubtitleHandler) OCRStatus(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	item, err := h.media.GetItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}

	jobID := chi.URLParam(r, "jobId")
	job, ok := h.ocrJobs.Get(jobID)
	if !ok {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, toOCRJobJSON(job))
}

// Delete handles DELETE /api/v1/items/{id}/subtitles/{subId}.
func (h *SubtitleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	itemID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	subID, err := uuid.Parse(chi.URLParam(r, "subId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid subtitle id")
		return
	}

	item, err := h.media.GetItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}

	row, err := h.svc.Get(r.Context(), subID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	files, err := h.media.GetFiles(r.Context(), itemID)
	if err != nil || !fileBelongsToItem(row.FileID, files) {
		respond.NotFound(w, r)
		return
	}
	if err := h.svc.Delete(r.Context(), subID); err != nil {
		h.logger.ErrorContext(r.Context(), "subtitles: delete", "id", subID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Serve handles GET /media/external-subtitles/{subId}. Returns the on-disk VTT.
// Requires auth — browsers send same-origin cookies on <track> requests.
func (h *SubtitleHandler) Serve(w http.ResponseWriter, r *http.Request) {
	subID, err := uuid.Parse(chi.URLParam(r, "subId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid subtitle id")
		return
	}
	row, err := h.svc.Get(r.Context(), subID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	// Resolve the parent item so we can enforce per-library ACL.
	file, err := h.media.GetFile(r.Context(), row.FileID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	item, err := h.media.GetItem(r.Context(), file.MediaItemID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.checkAccess(w, r, item.LibraryID) {
		return
	}
	data, err := os.ReadFile(row.StoragePath)
	if err != nil {
		h.logger.WarnContext(r.Context(), "subtitles: read file", "id", subID, "err", err)
		respond.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ── helpers ────────────────────────────────────────────────────────────────

func (h *SubtitleHandler) checkAccess(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
	if h.access == nil {
		return true
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		respond.InternalError(w, r)
		return false
	}
	if allowed != nil {
		if _, ok := allowed[libraryID]; !ok {
			respond.NotFound(w, r)
			return false
		}
	}
	return true
}

// deriveEpisodeNumbers walks up to the parent season/show to surface the
// canonical (season, episode) numbers for the upstream search.
func (h *SubtitleHandler) deriveEpisodeNumbers(r *http.Request, ep *media.Item) (int, int, bool) {
	if ep.Index == nil || ep.ParentID == nil {
		return 0, 0, false
	}
	season, err := h.media.GetItem(r.Context(), *ep.ParentID)
	if err != nil || season.Index == nil {
		return 0, 0, false
	}
	return *season.Index, *ep.Index, true
}

// deriveShowTitle walks episode → season → show to find the show title.
// Returns "" if any step fails.
func (h *SubtitleHandler) deriveShowTitle(r *http.Request, ep *media.Item) string {
	if ep.ParentID == nil {
		return ""
	}
	season, err := h.media.GetItem(r.Context(), *ep.ParentID)
	if err != nil || season.ParentID == nil {
		return ""
	}
	show, err := h.media.GetItem(r.Context(), *season.ParentID)
	if err != nil {
		return ""
	}
	return show.Title
}

func fileBelongsToItem(fileID uuid.UUID, files []media.File) bool {
	for _, f := range files {
		if f.ID == fileID {
			return true
		}
	}
	return false
}

func toExternalSubtitleJSON(row gen.ExternalSubtitle) ExternalSubtitleJSON {
	return ExternalSubtitleJSON{
		ID:       row.ID.String(),
		FileID:   row.FileID.String(),
		Language: row.Language,
		Title:    row.Title,
		Forced:   row.Forced,
		SDH:      row.Sdh,
		Source:   row.Source,
		SourceID: row.SourceID,
		URL:      "/media/external-subtitles/" + row.ID.String(),
	}
}
