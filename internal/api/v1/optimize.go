package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/downloads"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── Optimized versions ("Optimize…") ─────────────────────────────────────────
//
// An admin asks for a lower-bitrate, universally playable copy of a movie,
// episode, whole library or collection ahead of time (internal/downloads
// encodes it with the converted-download machinery). The copy is an extra
// version of the item, never a separate item and never one of its `files`:
// first-party clients that predate the feature pick files[0] or a file_id the
// play decision returns, and must keep getting the source. It reaches clients
// two additive ways:
//
//   - item detail's optimized_versions, for clients that list versions;
//   - the play decision, for clients that declare optimized=1 in
//     X-Client-Capabilities: when the source would need a video transcode (a
//     codec, HDR, resolution or bitrate-limit verdict) and a version fits the
//     client's profile and limits, the decision is directPlay of that version
//     with reason optimized_version.
//
// Versions are served by GET /media/optimized/{id} under the same gates as
// /media/stream (library ACL, rating ceiling, label restriction, watch limit,
// admin stop) and show on Now Playing as a direct play of the version.

// OptimizedLister is the read surface playback uses. Satisfied by
// *downloads.OptimizeService.
type OptimizedLister interface {
	PlayableForItem(ctx context.Context, itemID uuid.UUID) ([]downloads.OptimizedVersion, error)
	PlayableFile(ctx context.Context, id uuid.UUID) (downloads.OptimizedVersion, string, error)
}

// OptimizeStore is the admin surface. Satisfied by *downloads.OptimizeService.
type OptimizeStore interface {
	Targets(ctx context.Context, sel downloads.Selection) ([]uuid.UUID, bool, error)
	RequestItem(ctx context.Context, requestedBy, itemID uuid.UUID, profile string) (downloads.OptimizeOutcome, string, error)
	Wake()
	List(ctx context.Context, f downloads.ListFilter) ([]downloads.OptimizedVersion, map[string]int64, error)
	Get(ctx context.Context, id uuid.UUID) (downloads.OptimizedVersion, error)
	Delete(ctx context.Context, id uuid.UUID) (downloads.OptimizedVersion, error)
}

// ReasonOptimizedVersion is the play-decision reason for a verdict that plays
// an optimized version instead of transcoding the source.
const ReasonOptimizedVersion = "optimized_version"

// Every optimized version is the same kind of file (transcode.BuildProgressiveMP4).
const (
	optimizedContainer     = "mp4"
	optimizedVideoCodec    = "h264"
	optimizedAudioCodec    = "aac"
	optimizedAudioChannels = 2
)

// OptimizedVersionJSON is a playable optimized version, on item detail and in
// the play decision.
type OptimizedVersionJSON struct {
	ID string `json:"id"`
	// FileID is the source file the version was made from (for a stacked
	// movie, its first part — the version is the whole movie).
	FileID        string `json:"file_id"`
	Profile       string `json:"profile"`
	Label         string `json:"label"`
	Container     string `json:"container"`
	VideoCodec    string `json:"video_codec"`
	AudioCodec    string `json:"audio_codec"`
	AudioChannels int    `json:"audio_channels"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	BitrateKbps   int    `json:"bitrate_kbps,omitempty"`
	SizeBytes     *int64 `json:"size_bytes,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	// StreamURL serves the MP4 for direct play (cookie, Bearer, an asset
	// ?token= or StreamToken).
	StreamURL string `json:"stream_url"`
	// StreamToken is a 24 h token bound to this version, for players that
	// can only authenticate through the URL (like files[].stream_token).
	StreamToken string `json:"stream_token,omitempty"`
}

func optimizedProfileLabel(id string) string {
	if p, ok := downloads.OptimizeProfileByID(id); ok {
		return p.Label
	}
	return id
}

func optimizedStreamURL(id uuid.UUID) string { return "/media/optimized/" + id.String() }

// toOptimizedVersionJSON describes v; tokens (optional) mints its stream token.
func toOptimizedVersionJSON(v downloads.OptimizedVersion, tokens *auth.TokenMaker, claims *auth.Claims) OptimizedVersionJSON {
	out := OptimizedVersionJSON{
		ID: v.ID.String(), FileID: v.FileID.String(),
		Profile: v.Profile, Label: optimizedProfileLabel(v.Profile),
		Container: optimizedContainer, VideoCodec: optimizedVideoCodec,
		AudioCodec: optimizedAudioCodec, AudioChannels: optimizedAudioChannels,
		Width: v.Width, Height: v.Height, BitrateKbps: v.BitrateKbps,
		SizeBytes: v.SizeBytes, DurationMS: v.DurationMS,
		StreamURL: optimizedStreamURL(v.ID),
	}
	if tokens != nil && claims != nil {
		if tok, err := tokens.IssueStreamToken(*claims, v.ID); err == nil {
			out.StreamToken = tok
		}
	}
	return out
}

// optimizedAsFile describes an optimized version as a media.File, so the play
// decision can judge it with transcode.Decide like any source.
func optimizedAsFile(v downloads.OptimizedVersion) media.File {
	container, vc, ac := optimizedContainer, optimizedVideoCodec, optimizedAudioCodec
	depth := 8
	f := media.File{
		ID: v.ID, MediaItemID: v.ItemID, Status: "active",
		Container: &container, VideoCodec: &vc, AudioCodec: &ac,
		VideoBitDepth: &depth,
		AudioStreams:  []byte(fmt.Sprintf(`[{"codec":%q,"channels":%d}]`, ac, optimizedAudioChannels)),
	}
	if v.Width > 0 && v.Height > 0 {
		w, h := v.Width, v.Height
		f.ResolutionW, f.ResolutionH = &w, &h
	}
	if v.BitrateKbps > 0 {
		b := int64(v.BitrateKbps) * 1000
		f.Bitrate = &b
	}
	if v.DurationMS > 0 {
		d := v.DurationMS
		f.DurationMS = &d
	}
	return f
}

// wantsOptimized reports whether a verdict for the source is one an
// optimized version should replace: a video transcode, whatever forced it
// (codec, HDR, resolution, bit depth, a bitrate limit). Direct play and remux
// cost the server nothing worth saving; unsupported and damaged sources never
// get a version (internal/downloads refuses to encode them).
func wantsOptimized(d transcode.Decision) bool {
	return d == transcode.DecisionTranscode
}

// pickOptimizedVersion returns the best of an item's optimized versions for
// this client, or nil: made from sourceFileID, direct-playable under the
// client's profile (transcode.Decide on the version itself), and within the
// bitrate cap (0 = none) — a remote viewer held to 4 Mbps never gets the 8
// Mbps version. versions come highest bitrate first, so the first fit wins.
func pickOptimizedVersion(versions []downloads.OptimizedVersion, sourceFileID uuid.UUID, caps transcode.ClientCapabilities, serverCaps transcode.ServerCaps, capKbps int) *downloads.OptimizedVersion {
	for i := range versions {
		v := &versions[i]
		if v.FileID != sourceFileID || v.Status != downloads.StatusReady {
			continue
		}
		if capKbps > 0 && v.BitrateKbps > capKbps {
			continue
		}
		if transcode.Decide(optimizedAsFile(*v), caps, serverCaps) != transcode.DecisionDirectPlay {
			continue
		}
		return v
	}
	return nil
}

// ── Item detail + stream (ItemHandler) ───────────────────────────────────────

// WithOptimizedVersions lists an item's optimized versions on its detail
// (optimized_versions) and serves them from /media/optimized/{id}. nil =
// neither.
func (h *ItemHandler) WithOptimizedVersions(l OptimizedLister) *ItemHandler {
	h.optimized = l
	return h
}

// optimizedVersionsFor is the detail response's optimized_versions: the
// item's playable versions, each with its stream URL and token.
func (h *ItemHandler) optimizedVersionsFor(ctx context.Context, claims *auth.Claims, item *media.Item) []OptimizedVersionJSON {
	if h.optimized == nil {
		return nil
	}
	switch item.Type {
	case "movie", "episode", "home_video", "music_video":
	default:
		return nil
	}
	vs, err := h.optimized.PlayableForItem(ctx, item.ID)
	if err != nil {
		h.logger.WarnContext(ctx, "list optimized versions", "item_id", item.ID, "err", err)
		return nil
	}
	if len(vs) == 0 {
		return nil
	}
	out := make([]OptimizedVersionJSON, 0, len(vs))
	for _, v := range vs {
		out = append(out, toOptimizedVersionJSON(v, h.tokens, claims))
	}
	return out
}

// StreamOptimized handles GET /media/optimized/{id}: an optimized version's
// MP4, with Range support, for direct play. It applies every gate StreamFile
// applies to the source — library ACL, rating ceiling + label restriction,
// parental watch limit, admin stop — and records the stream for Now Playing.
func (h *ItemHandler) StreamOptimized(w http.ResponseWriter, r *http.Request) {
	if h.optimized == nil {
		respond.NotFound(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid optimized version id")
		return
	}
	ctx := r.Context()
	v, path, err := h.optimized.PlayableFile(ctx, id)
	if err != nil {
		if errors.Is(err, downloads.ErrOptimizedNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "get optimized version for stream", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	item, err := h.media.GetItem(ctx, v.ItemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "get item for optimized stream", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	claims := middleware.ClaimsFromContext(ctx)
	if viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(ctx, claims, item.ID, cr) {
			respond.Forbidden(w, r)
			return
		}
	}
	// Every request, like StreamFile: a Range continuation must not slip
	// past the watch limit or an admin stop.
	if claims != nil {
		if watchLimitBlocks(w, r, h.watchLimit, h.logger, claims.UserID) {
			return
		}
		if playbackStopBlocks(w, r, h.stops, claims.UserID, item.ID) {
			return
		}
		h.usageAccrue.Tick(ctx, h.logger, claims.UserID)
		h.noteDirectPlay(r, claims.UserID, item.ID)
	}
	if h.tracker != nil && claims != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		clientIP := r.RemoteAddr
		if host, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = host
		}
		clientName := r.Header.Get("User-Agent")
		if idx := strings.IndexByte(clientName, '/'); idx > 0 {
			clientName = clientName[:idx]
		}
		h.tracker.TouchOptimized(claims.UserID, canonicalIP(clientIP), item.ID, v.FileID, v.ID, path, clientName)
	}

	f, err := os.Open(path)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	// A streamed body outlives the JSON handlers' write deadline (StreamFile).
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// ── Play decision (NativeTranscodeHandler) ───────────────────────────────────

// WithOptimizedVersions lets the play decision choose an optimized version
// for clients that declare optimized=1. nil = never.
func (h *NativeTranscodeHandler) WithOptimizedVersions(l OptimizedLister) *NativeTranscodeHandler {
	h.optimized = l
	return h
}

// optimizedChoice looks for an optimized version to play instead of a
// transcode of file. nil when the client didn't opt in, the verdict isn't a
// transcode, or nothing fits.
func (h *NativeTranscodeHandler) optimizedChoice(ctx context.Context, itemID uuid.UUID, file *media.File, decision transcode.Decision, caps transcode.ClientCapabilities, serverCaps transcode.ServerCaps, capKbps int) *downloads.OptimizedVersion {
	if h.optimized == nil || !caps.AcceptsOptimized || !wantsOptimized(decision) {
		return nil
	}
	vs, err := h.optimized.PlayableForItem(ctx, itemID)
	if err != nil {
		h.logger.WarnContext(ctx, "playback-decision: list optimized versions", "item_id", itemID, "err", err)
		return nil
	}
	return pickOptimizedVersion(vs, file.ID, caps, serverCaps, capKbps)
}

// ── Admin: /optimize ─────────────────────────────────────────────────────────

// OptimizeHandler serves the admin's /optimize endpoints.
type OptimizeHandler struct {
	svc    OptimizeStore
	audit  *audit.Logger
	logger *slog.Logger
}

// NewOptimizeHandler builds the handler.
func NewOptimizeHandler(svc OptimizeStore, logger *slog.Logger) *OptimizeHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &OptimizeHandler{svc: svc, logger: logger}
}

// WithAudit records optimize requests and deletes.
func (h *OptimizeHandler) WithAudit(a *audit.Logger) *OptimizeHandler {
	h.audit = a
	return h
}

// OptimizeProfileJSON is one entry of the "Optimize…" menu.
type OptimizeProfileJSON struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// MaxHeight / BitrateKbps describe the target; the original-quality
	// profile keeps the source's resolution (keep_resolution) and picks the
	// bitrate per file.
	MaxHeight      int  `json:"max_height"`
	BitrateKbps    int  `json:"bitrate_kbps"`
	KeepResolution bool `json:"keep_resolution,omitempty"`
}

// OptimizeJobJSON is one optimized version or the job producing it.
type OptimizeJobJSON struct {
	ID           string `json:"id"`
	ItemID       string `json:"item_id"`
	FileID       string `json:"file_id"`
	LibraryID    string `json:"library_id,omitempty"`
	Title        string `json:"title,omitempty"`
	ItemType     string `json:"item_type,omitempty"`
	ShowTitle    string `json:"show_title,omitempty"`
	SeasonIndex  *int   `json:"season_index,omitempty"`
	EpisodeIndex *int   `json:"episode_index,omitempty"`
	Profile      string `json:"profile"`
	ProfileLabel string `json:"profile_label"`
	// Status: queued | running | ready | failed.
	Status      string     `json:"status"`
	Progress    float64    `json:"progress"`
	Error       string     `json:"error,omitempty"`
	SizeBytes   *int64     `json:"size_bytes,omitempty"`
	Width       int        `json:"width,omitempty"`
	Height      int        `json:"height,omitempty"`
	BitrateKbps int        `json:"bitrate_kbps,omitempty"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func toOptimizeJobJSON(v downloads.OptimizedVersion) OptimizeJobJSON {
	out := OptimizeJobJSON{
		ID: v.ID.String(), ItemID: v.ItemID.String(), FileID: v.FileID.String(),
		Title: v.ItemTitle, ItemType: v.ItemType, ShowTitle: v.ShowTitle,
		SeasonIndex: v.SeasonIndex, EpisodeIndex: v.EpisodeIndex,
		Profile: v.Profile, ProfileLabel: optimizedProfileLabel(v.Profile),
		Status: v.Status, Progress: v.Progress, Error: v.Error,
		SizeBytes: v.SizeBytes, Width: v.Width, Height: v.Height,
		BitrateKbps: v.BitrateKbps, DurationMS: v.DurationMS,
		CreatedAt: v.CreatedAt, StartedAt: v.StartedAt, CompletedAt: v.CompletedAt,
	}
	if v.LibraryID != uuid.Nil {
		out.LibraryID = v.LibraryID.String()
	}
	return out
}

// Profiles handles GET /api/v1/optimize/profiles.
func (h *OptimizeHandler) Profiles(w http.ResponseWriter, r *http.Request) {
	out := make([]OptimizeProfileJSON, 0, len(downloads.OptimizeProfiles))
	for _, p := range downloads.OptimizeProfiles {
		out = append(out, OptimizeProfileJSON{
			ID: p.ID, Label: p.Label, MaxHeight: p.Profile.MaxHeight,
			BitrateKbps: p.Profile.Bitrate, KeepResolution: p.KeepResolution,
		})
	}
	respond.Success(w, r, out)
}

type optimizeRequest struct {
	ItemIDs      []string `json:"item_ids"`
	LibraryID    string   `json:"library_id"`
	CollectionID string   `json:"collection_id"`
	Profile      string   `json:"profile"`
}

// optimizeSkip names an item the request couldn't optimize.
type optimizeSkip struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}

// OptimizeResultJSON is POST /optimize's answer.
type OptimizeResultJSON struct {
	Profile string `json:"profile"`
	// Items is how many video items the selection expanded to.
	Items    int `json:"items"`
	Queued   int `json:"queued"`
	Existing int `json:"existing"`
	Skipped  int `json:"skipped"`
	// Truncated: the selection held more than the per-request limit; the
	// rest weren't looked at (ask again once these are queued).
	Truncated bool `json:"truncated,omitempty"`
	// SkippedItems explains up to 50 of the skips.
	SkippedItems []optimizeSkip `json:"skipped_items,omitempty"`
}

// maxSkipDetails bounds how many skip reasons POST /optimize returns.
const maxSkipDetails = 50

// Create handles POST /api/v1/optimize {item_ids | library_id |
// collection_id, profile}: queues an optimized version of every video item
// the selection covers (shows and seasons expand to their episodes). Items
// that already have the version are left alone; items that can't be
// converted (no video, damaged, Dolby Vision) are reported as skipped.
func (h *OptimizeHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body optimizeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if _, ok := downloads.OptimizeProfileByID(body.Profile); !ok {
		respond.ValidationError(w, r, "profile must be one of "+downloads.OptimizeProfileIDs())
		return
	}
	sel, msg := parseOptimizeSelection(body)
	if msg != "" {
		respond.ValidationError(w, r, msg)
		return
	}
	ctx := r.Context()
	targets, truncated, err := h.svc.Targets(ctx, sel)
	if err != nil {
		if errors.Is(err, downloads.ErrOptimizedNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "optimize: resolve selection", "err", err)
		respond.InternalError(w, r)
		return
	}
	res := OptimizeResultJSON{Profile: body.Profile, Items: len(targets), Truncated: truncated}
	for _, itemID := range targets {
		outcome, reason, err := h.svc.RequestItem(ctx, claims.UserID, itemID, body.Profile)
		if err != nil {
			h.logger.ErrorContext(ctx, "optimize: request item", "item_id", itemID, "err", err)
			respond.InternalError(w, r)
			return
		}
		switch outcome {
		case downloads.OptimizeQueued:
			res.Queued++
		case downloads.OptimizeExisting:
			res.Existing++
		default:
			res.Skipped++
			if len(res.SkippedItems) < maxSkipDetails {
				res.SkippedItems = append(res.SkippedItems, optimizeSkip{ItemID: itemID.String(), Reason: reason})
			}
		}
	}
	if res.Queued > 0 {
		h.svc.Wake()
	}
	h.logger.InfoContext(ctx, "optimize: requested", "profile", body.Profile, "items", res.Items,
		"queued", res.Queued, "existing", res.Existing, "skipped", res.Skipped, "user_id", claims.UserID)
	if h.audit != nil {
		detail := map[string]any{
			"profile": body.Profile, "items": res.Items,
			"queued": res.Queued, "existing": res.Existing, "skipped": res.Skipped,
		}
		target := ""
		switch {
		case sel.LibraryID != nil:
			detail["library_id"] = sel.LibraryID.String()
			target = sel.LibraryID.String()
		case sel.CollectionID != nil:
			detail["collection_id"] = sel.CollectionID.String()
			target = sel.CollectionID.String()
		default:
			detail["item_ids"] = len(sel.ItemIDs)
			if len(sel.ItemIDs) == 1 {
				target = sel.ItemIDs[0].String()
			}
		}
		h.audit.Log(ctx, &claims.UserID, audit.ActionOptimizeRequest, target, detail, audit.ClientIP(r))
	}
	respond.Success(w, r, res)
}

// maxOptimizeItemIDs bounds item_ids in one request.
const maxOptimizeItemIDs = 500

// parseOptimizeSelection checks that exactly one selector is set and parses
// it. msg is the validation error, "" when fine.
func parseOptimizeSelection(body optimizeRequest) (downloads.Selection, string) {
	set := 0
	if len(body.ItemIDs) > 0 {
		set++
	}
	if body.LibraryID != "" {
		set++
	}
	if body.CollectionID != "" {
		set++
	}
	if set != 1 {
		return downloads.Selection{}, "set exactly one of item_ids, library_id or collection_id"
	}
	var sel downloads.Selection
	switch {
	case body.LibraryID != "":
		id, err := uuid.Parse(body.LibraryID)
		if err != nil {
			return sel, "invalid library_id"
		}
		sel.LibraryID = &id
	case body.CollectionID != "":
		id, err := uuid.Parse(body.CollectionID)
		if err != nil {
			return sel, "invalid collection_id"
		}
		sel.CollectionID = &id
	default:
		if len(body.ItemIDs) > maxOptimizeItemIDs {
			return sel, fmt.Sprintf("at most %d item_ids per request", maxOptimizeItemIDs)
		}
		for _, s := range body.ItemIDs {
			id, err := uuid.Parse(s)
			if err != nil {
				return downloads.Selection{}, "invalid item id " + s
			}
			sel.ItemIDs = append(sel.ItemIDs, id)
		}
	}
	return sel, ""
}

// OptimizeJobsJSON is GET /optimize/jobs.
type OptimizeJobsJSON struct {
	Jobs []OptimizeJobJSON `json:"jobs"`
	// Counts are per-status totals under the item/library filter.
	Counts map[string]int64 `json:"counts"`
}

// ListJobs handles GET /api/v1/optimize/jobs[?item_id=&library_id=&status=
// &limit=&offset=]: optimized versions and the jobs still producing them,
// newest first.
func (h *OptimizeHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f downloads.ListFilter
	for key, dst := range map[string]**uuid.UUID{"item_id": &f.ItemID, "library_id": &f.LibraryID} {
		if v := q.Get(key); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				respond.BadRequest(w, r, "invalid "+key)
				return
			}
			*dst = &id
		}
	}
	switch st := q.Get("status"); st {
	case "", downloads.StatusQueued, downloads.StatusRunning, downloads.StatusReady, downloads.StatusFailed:
		f.Status = st
	default:
		respond.BadRequest(w, r, "status must be queued, running, ready or failed")
		return
	}
	page := respond.ParsePagination(r, 100, 500)
	f.Limit, f.Offset = int(page.Limit), int(page.Offset)
	vs, counts, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "optimize: list jobs", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := OptimizeJobsJSON{Jobs: make([]OptimizeJobJSON, 0, len(vs)), Counts: counts}
	for _, v := range vs {
		out.Jobs = append(out.Jobs, toOptimizeJobJSON(v))
	}
	respond.Success(w, r, out)
}

// GetJob handles GET /api/v1/optimize/jobs/{id}.
func (h *OptimizeHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	v, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, downloads.ErrOptimizedNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "optimize: get job", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toOptimizeJobJSON(v))
}

// Delete handles DELETE /api/v1/optimize/jobs/{id} and
// /api/v1/optimize/versions/{id} (one id space): cancels a queued or running
// job, or deletes a finished version and its file.
func (h *OptimizeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	ctx := r.Context()
	v, err := h.svc.Delete(ctx, id)
	if err != nil {
		if errors.Is(err, downloads.ErrOptimizedNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "optimize: delete", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	h.logger.InfoContext(ctx, "optimize: deleted", "id", id, "item_id", v.ItemID, "profile", v.Profile, "status", v.Status)
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionOptimizeDelete, v.ItemID.String(), map[string]any{
				"optimized_id": id.String(), "profile": v.Profile, "status": v.Status,
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
