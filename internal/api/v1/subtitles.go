package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
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

// SubtitleOffsetStore persists the admin timing offsets of external
// subtitles and embedded subtitle streams (migration 00055). Satisfied by
// *gen.Queries.
type SubtitleOffsetStore interface {
	SetExternalSubtitleOffset(ctx context.Context, arg gen.SetExternalSubtitleOffsetParams) (gen.ExternalSubtitle, error)
	UpsertSubtitleStreamOffset(ctx context.Context, arg gen.UpsertSubtitleStreamOffsetParams) error
	DeleteSubtitleStreamOffset(ctx context.Context, arg gen.DeleteSubtitleStreamOffsetParams) error
}

// SubtitleHandler exposes search/download/list endpoints for external subtitles.
// Library access is enforced via the items the subtitles attach to.
type SubtitleHandler struct {
	svc     SubtitleService
	media   ItemMediaService
	access  LibraryAccessChecker
	offsets SubtitleOffsetStore // optional; nil 404s the offset endpoints
	audit   *audit.Logger
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

// WithOffsets enables the admin subtitle-offset endpoints.
func (h *SubtitleHandler) WithOffsets(s SubtitleOffsetStore) *SubtitleHandler {
	h.offsets = s
	return h
}

// WithAudit records offset changes in the audit log.
func (h *SubtitleHandler) WithAudit(a *audit.Logger) *SubtitleHandler {
	h.audit = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired.
// A nil checker fails OPEN (serves every library), so api.Handlers.
// ValidateLibraryAccess asserts this at startup and refuses to boot
// without it.
func (h *SubtitleHandler) LibraryAccessWired() bool { return h.access != nil }

// Close releases background resources owned by the handler — currently the
// OCR job store's TTL-sweep goroutine. Wired into server shutdown so the
// process tears down cleanly (and so goroutine-leak-checked tests pass).
func (h *SubtitleHandler) Close() {
	if h.ocrJobs != nil {
		h.ocrJobs.Stop()
	}
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
	// OffsetMS is the server-wide timing correction (positive = later),
	// already applied to the cues URL serves.
	OffsetMS int32 `json:"offset_ms"`
}

// writeSubtitlesNotConfigured is the feature-off answer for search and
// download when OpenSubtitles isn't enabled (features.subtitles_external is
// false). Still a 503, but in the standard error envelope with a code: the
// old bare {"error": "..."} body had no message a client could read, so the
// web player showed "HTTP 503".
func writeSubtitlesNotConfigured(w http.ResponseWriter, r *http.Request) {
	respond.Error(w, r, http.StatusServiceUnavailable, "SUBTITLES_NOT_CONFIGURED",
		"online subtitle search is not configured on this server")
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
	if !h.checkItemAccess(w, r, item) {
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
			writeSubtitlesNotConfigured(w, r)
			return
		}
		h.logger.WarnContext(r.Context(), "subtitles: search", "id", itemID, "err", err)
		respond.Error(w, r, http.StatusBadGateway, "SUBTITLE_PROVIDER_ERROR", "the subtitle provider returned an error")
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
	if !h.checkItemAccess(w, r, item) {
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
			writeSubtitlesNotConfigured(w, r)
			return
		}
		if errors.Is(err, subtitles.ErrQuotaExhausted) {
			respond.Error(w, r, http.StatusTooManyRequests, "SUBTITLE_QUOTA_EXHAUSTED",
				"OpenSubtitles daily download quota is used up; try again after it resets (or add account credentials for a higher limit)")
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: download", "id", itemID, "err", err)
		respond.Error(w, r, http.StatusBadGateway, "SUBTITLE_PROVIDER_ERROR", "the subtitle provider returned an error")
		return
	}
	respond.Created(w, r, toExternalSubtitleJSON(row))
}

// ocrJobJSON is the response shape for both the create-job (POST) and
// poll-job (GET) endpoints. Result is omitempty so a still-running job
// doesn't carry a null subtitle field; status drives the client's
// decision to render success vs error vs spinner.
type ocrJobJSON struct {
	JobID       string                 `json:"job_id"`
	Status      subtitles.OCRJobStatus `json:"status"`
	FileID      string                 `json:"file_id"`
	StreamIndex int                    `json:"stream_index"`
	StartedAt   string                 `json:"started_at"`
	CompletedAt string                 `json:"completed_at,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Subtitle    *ExternalSubtitleJSON  `json:"subtitle,omitempty"`
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
	if !h.checkItemAccess(w, r, item) {
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
	var ocrFile *media.File
	for i := range files {
		if files[i].ID == fileID {
			inputPath = files[i].FilePath
			ocrFile = &files[i]
			break
		}
	}
	if inputPath == "" || ocrFile == nil {
		respond.NotFound(w, r)
		return
	}

	// The requested stream must EXIST and must be image-based.
	//
	// stream_index arrived as a bare int and went straight to ffmpeg as
	// `[0:N]` in an overlay filter with no -frames:v, no -t and no size cap.
	// Pointed at the video stream (index 0 on essentially every file), the
	// mpdecimate frac that exists to skip static subtitle canvases never
	// fires on real video, so the job wrote one full-resolution PNG per
	// decoded frame into the cache volume — which is shared with artwork,
	// trickplay and DVR recordings, has no free-space check anywhere, and is
	// only swept for transcode directories. The job also runs on a
	// server-lifetime context, so the client cannot stop it by disconnecting.
	//
	// Both other entry points to this pipeline already enforce exactly this
	// (items.go's text-subtitle path and the scheduled OCR handler), and
	// ocr.ErrNoStream was declared for it and never returned. Same shape as
	// items.go so the three stay comparable.
	var ocrStream *SubtitleStreamJSON
	for _, s := range parseJSONBSubtitleStreams(ocrFile.SubtitleStreams) {
		if s.Index == body.StreamIndex {
			s := s
			ocrStream = &s
			break
		}
	}
	if ocrStream == nil {
		respond.NotFound(w, r)
		return
	}
	if !ocr.IsImageBased(ocrStream.Codec) {
		respond.Error(w, r, http.StatusUnsupportedMediaType, "NOT_IMAGE_SUBTITLE",
			"OCR only applies to image-based subtitles (PGS/VOBSUB/DVB); this stream is already text")
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
	// SafeGo — runOCRJob shells out to ffmpeg + tesseract over user-
	// uploaded subtitle streams; a panic from a bad stream shouldn't
	// take the server down with it. The in-flight counter defer still
	// runs (recover unwinds the defer chain normally).
	observability.SafeGo(h.logger, "subtitles:ocr-job", func() {
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
	})

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
	if !h.checkItemAccess(w, r, item) {
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
	// External subtitles are shared by every user of the item and carry no
	// uploader, so deletion is admin-only: library access alone let any user
	// (including a restricted profile) remove subtitles others downloaded or
	// paid OCR time for.
	if c := middleware.ClaimsFromContext(r.Context()); c == nil || !c.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
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
	if !h.checkItemAccess(w, r, item) {
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
// ?start_ms= rebases the cues (see subtitleRebaseMS).
func (h *SubtitleHandler) Serve(w http.ResponseWriter, r *http.Request) {
	subID, err := uuid.Parse(chi.URLParam(r, "subId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid subtitle id")
		return
	}
	h.serveExternal(w, r, subID, uuid.Nil)
}

// ServeForFile handles GET /media/subtitles/{fileId}/external/{subId}: the
// same VTT as Serve, addressed through the file it belongs to. The route
// exists so a file-bound stream token (?token=, purpose=stream, bound to
// {fileId} by RequiredAllowQueryToken) authenticates an external subtitle:
// a cast hands the Chromecast receiver URLs it fetches without cookies, and
// the stream token it already holds for the video is its credential. A
// subtitle of any other file is a 404, so the token reaches no further than
// that file's own subtitles.
func (h *SubtitleHandler) ServeForFile(w http.ResponseWriter, r *http.Request) {
	fileID, err := uuid.Parse(chi.URLParam(r, "fileId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid file id")
		return
	}
	subID, err := uuid.Parse(chi.URLParam(r, "subId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid subtitle id")
		return
	}
	h.serveExternal(w, r, subID, fileID)
}

// serveExternal writes external subtitle subID after the item gates. When
// wantFile isn't uuid.Nil the subtitle must belong to that file.
func (h *SubtitleHandler) serveExternal(w http.ResponseWriter, r *http.Request, subID, wantFile uuid.UUID) {
	row, err := h.svc.Get(r.Context(), subID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if wantFile != uuid.Nil && row.FileID != wantFile {
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
	if !h.checkItemAccess(w, r, item) {
		return
	}
	// Content-rating ceiling: a restricted profile with library access to a
	// mixed-rating library shouldn't pull the subtitle text of an over-ceiling
	// item (the video bytes are already gated on StreamFile/transcode; this
	// closes the metadata-leak gap the security scan flagged).
	if claims := middleware.ClaimsFromContext(r.Context()); viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(r.Context(), claims, item.ID, cr) {
			respond.Forbidden(w, r)
			return
		}
	}
	data, err := os.ReadFile(row.StoragePath)
	if err != nil {
		h.logger.WarnContext(r.Context(), "subtitles: read file", "id", subID, "err", err)
		respond.NotFound(w, r)
		return
	}
	serveVTT(w, r, data, int(row.OffsetMs)-subtitleRebaseMS(r))
}

// maxSubtitleRebaseMS bounds ?start_ms: two days, far past any real title.
const maxSubtitleRebaseMS = 48 * 60 * 60 * 1000

// subtitleRebaseMS reads the optional ?start_ms= query parameter of the
// subtitle VTT routes: the content position a player's own timeline starts
// at. The served cues are moved that much earlier (cues that end before it
// are dropped), on top of the track's saved offset, so time 0 of the VTT is
// that position. A Chromecast plays a cast HLS session started mid-title on
// a timeline that begins at 0, and a side-loaded text track is timed on that
// timeline — without the rebase every cue would show start_ms late. Missing,
// malformed, negative or absurd values mean 0 (no rebase).
func subtitleRebaseMS(r *http.Request) int {
	raw := r.URL.Query().Get("start_ms")
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 || ms > maxSubtitleRebaseMS {
		return 0
	}
	return ms
}

// serveVTT writes a WebVTT body shifted by the track's server offset
// (subtitles.ShiftVTT). The ETag hashes what is sent, offset included, and
// clients must revalidate (no-cache): an admin's offset change then reaches
// every player on its next load instead of after a day-long max-age, and an
// unchanged track still costs only a 304.
func serveVTT(w http.ResponseWriter, r *http.Request, data []byte, offsetMS int) {
	data = subtitles.ShiftVTT(data, offsetMS)
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	// `private`: subtitle routes are ACL + rating gated per request on
	// token-free same-origin URLs, so a shared cache must not serve one
	// user's authorized cues to another.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// etagMatches reports whether an If-None-Match header names etag (or "*").
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == etag || t == "*" {
			return true
		}
	}
	return false
}

// decodeOffset reads {"offset_ms": n} and validates the range, writing the
// 400 / 422 itself. ok is false when it did.
func decodeOffset(w http.ResponseWriter, r *http.Request) (int, bool) {
	var body struct {
		OffsetMS *int `json:"offset_ms"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.OffsetMS == nil {
		respond.BadRequest(w, r, `body must be {"offset_ms": <milliseconds>}`)
		return 0, false
	}
	if err := subtitles.ValidateOffset(*body.OffsetMS); err != nil {
		respond.ValidationError(w, r, err.Error())
		return 0, false
	}
	return *body.OffsetMS, true
}

// SetOffset handles PUT /api/v1/subtitles/{id}/offset with
// {"offset_ms": n}: the server-wide timing correction of one external
// subtitle, positive = later, within ±10 minutes. Admin-only. Every client
// gets the shifted cues from /media/external-subtitles/{id}. Returns the
// updated subtitle.
func (h *SubtitleHandler) SetOffset(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	if h.offsets == nil {
		respond.NotFound(w, r)
		return
	}
	subID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid subtitle id")
		return
	}
	offset, ok := decodeOffset(w, r)
	if !ok {
		return
	}
	prev, err := h.svc.Get(r.Context(), subID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	row, err := h.offsets.SetExternalSubtitleOffset(r.Context(), gen.SetExternalSubtitleOffsetParams{
		ID: subID, OffsetMs: int32(offset),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: set offset", "id", subID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionSubtitleOffset, subID.String(),
			map[string]any{
				"kind": "external", "file_id": row.FileID.String(),
				"from_ms": prev.OffsetMs, "offset_ms": offset,
			}, audit.ClientIP(r))
	}
	respond.Success(w, r, toExternalSubtitleJSON(row))
}

// SetStreamOffset handles PUT
// /api/v1/files/{file_id}/subtitle-streams/{index}/offset with
// {"offset_ms": n}: the same correction for an embedded text subtitle
// stream, by its absolute index (subtitle_streams[].index). Admin-only.
// /media/subtitles/{file_id}/{index} serves the shifted cues. Image-based
// streams (PGS, VobSub) are 415: they are never served as text, so an
// offset would do nothing — their OCR'd copy is an external subtitle with an
// offset of its own. Responds {"file_id", "stream_index", "offset_ms"}.
func (h *SubtitleHandler) SetStreamOffset(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	if h.offsets == nil {
		respond.NotFound(w, r)
		return
	}
	fileID, err := uuid.Parse(chi.URLParam(r, "file_id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid file id")
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || idx < 0 {
		respond.BadRequest(w, r, "invalid stream index")
		return
	}
	offset, ok := decodeOffset(w, r)
	if !ok {
		return
	}
	file, err := h.media.GetFile(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "subtitles: get file for offset", "id", fileID, "err", err)
		respond.InternalError(w, r)
		return
	}
	var stream *SubtitleStreamJSON
	for _, s := range parseJSONBSubtitleStreams(file.SubtitleStreams) {
		if s.Index == idx {
			s := s
			stream = &s
			break
		}
	}
	if stream == nil {
		respond.NotFound(w, r)
		return
	}
	if ocr.IsImageBased(stream.Codec) {
		respond.Error(w, r, http.StatusUnsupportedMediaType, "IMAGE_SUBTITLE",
			"image-based subtitles aren't served as text; set the offset on the OCR'd subtitle instead")
		return
	}
	if offset == 0 {
		err = h.offsets.DeleteSubtitleStreamOffset(r.Context(), gen.DeleteSubtitleStreamOffsetParams{
			FileID: fileID, StreamIndex: int32(idx),
		})
	} else {
		err = h.offsets.UpsertSubtitleStreamOffset(r.Context(), gen.UpsertSubtitleStreamOffsetParams{
			FileID: fileID, StreamIndex: int32(idx), OffsetMs: int32(offset),
		})
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "subtitles: set stream offset",
			"file_id", fileID, "stream", idx, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionSubtitleOffset, fileID.String(),
			map[string]any{"kind": "embedded", "stream_index": idx, "offset_ms": offset}, audit.ClientIP(r))
	}
	respond.Success(w, r, map[string]any{
		"file_id":      fileID.String(),
		"stream_index": idx,
		"offset_ms":    offset,
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

// checkItemAccess applies the library ACL AND the content-rating ceiling for an
// item — the same pair the streaming and embedded-subtitle routes enforce.
// Subtitle search/download/OCR/serve previously checked only the library, so a
// restricted profile could pull (and trigger paid OpenSubtitles / OCR work for)
// subtitles of titles above its ceiling.
func (h *SubtitleHandler) checkItemAccess(w http.ResponseWriter, r *http.Request, item *media.Item) bool {
	if !h.checkAccess(w, r, item.LibraryID) {
		return false
	}
	if claims := middleware.ClaimsFromContext(r.Context()); viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(r.Context(), claims, item.ID, cr) {
			respond.NotFound(w, r)
			return false
		}
	}
	return true
}

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
		OffsetMS: row.OffsetMs,
	}
}
