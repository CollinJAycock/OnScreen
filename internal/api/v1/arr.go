package v1

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
)

// ArrSettingsReader reads the arr API key and path mappings from settings.
type ArrSettingsReader interface {
	ArrAPIKey(ctx context.Context) string
	ArrPathMappings(ctx context.Context) map[string]string // remote prefix → local prefix
}

// ArrLibraryFinder finds libraries and triggers scans.
type ArrLibraryFinder interface {
	FindLibraryByPath(ctx context.Context, filePath string) (uuid.UUID, error)
	TriggerDirectoryScan(ctx context.Context, libraryID uuid.UUID, dir string) error
}

// ArrRequestReconciler is the request-fulfillment hook fired after a webhook
// scan. The arr-side trigger directory scan is async, so the reconciler is
// fired via the same scan goroutine — wiring is done in the adapter that
// owns both the scanner and the requests service. Kept as an interface so
// the webhook stays decoupled from the requests package.
type ArrRequestReconciler interface {
	ReconcileFulfillments(ctx context.Context)
}

// ArrHandler handles incoming webhook notifications from Radarr, Sonarr, and Lidarr.
type ArrHandler struct {
	settings   ArrSettingsReader
	libs       ArrLibraryFinder
	reconciler ArrRequestReconciler
	logger     *slog.Logger
}

// NewArrHandler creates an ArrHandler.
func NewArrHandler(settings ArrSettingsReader, libs ArrLibraryFinder, logger *slog.Logger) *ArrHandler {
	return &ArrHandler{settings: settings, libs: libs, logger: logger}
}

// WithRequestReconciler attaches the fulfillment reconciler. When nil, the
// webhook only triggers a scan; request transitions rely on the periodic
// scheduled task instead.
func (h *ArrHandler) WithRequestReconciler(r ArrRequestReconciler) *ArrHandler {
	h.reconciler = r
	return h
}

// arrPayload is a minimal representation of a Radarr/Sonarr/Lidarr webhook body.
// We only parse the fields we need to identify the event and file path.
type arrPayload struct {
	EventType string `json:"eventType"`

	// Radarr
	Movie     *arrMovie     `json:"movie,omitempty"`
	MovieFile *arrMediaFile `json:"movieFile,omitempty"`

	// Sonarr
	Series      *arrSeries    `json:"series,omitempty"`
	EpisodeFile *arrMediaFile `json:"episodeFile,omitempty"`

	// Lidarr
	Artist    *arrArtist    `json:"artist,omitempty"`
	TrackFile *arrMediaFile `json:"trackFile,omitempty"`
}

type arrMovie struct {
	Title      string `json:"title"`
	Year       int    `json:"year"`
	FolderPath string `json:"folderPath"`
}

type arrSeries struct {
	Title string `json:"title"`
	Path  string `json:"path"`
}

type arrArtist struct {
	Name string `json:"artistName"`
	Path string `json:"path"`
}

type arrMediaFile struct {
	Path         string `json:"path"`
	RelativePath string `json:"relativePath"`
}

// Webhook handles POST /api/v1/arr/webhook.
func (h *ArrHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	if !h.authenticate(r) {
		respond.Unauthorized(w, r)
		return
	}

	var payload arrPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respond.BadRequest(w, r, "invalid JSON payload")
		return
	}

	h.logger.InfoContext(r.Context(), "arr webhook received",
		"event_type", payload.EventType,
		"source", h.identifySource(payload))

	switch payload.EventType {
	case "Test":
		respond.Success(w, r, map[string]string{"status": "ok"})
		return
	case "Download", "EpisodeFileDelete", "MovieFileDelete", "TrackFileDelete",
		"MovieDelete", "SeriesDelete", "ArtistDelete",
		"Rename", "SeriesAdd", "MovieAdded":
		// Actionable events — extract path and scan.
	default:
		// Grab, health events, etc. — acknowledge but don't scan.
		respond.NoContent(w)
		return
	}

	dir := h.extractScanDir(r.Context(), payload)
	if dir == "" {
		h.logger.WarnContext(r.Context(), "arr webhook: no scannable path found",
			"event_type", payload.EventType)
		respond.NoContent(w)
		return
	}

	libraryID, err := h.libs.FindLibraryByPath(r.Context(), dir)
	if err != nil {
		h.logger.WarnContext(r.Context(), "arr webhook: no matching library",
			"dir", dir, "err", err)
		respond.NoContent(w)
		return
	}

	if err := h.libs.TriggerDirectoryScan(r.Context(), libraryID, dir); err != nil {
		h.logger.ErrorContext(r.Context(), "arr webhook: scan trigger failed",
			"library_id", libraryID, "dir", dir, "err", err)
		respond.InternalError(w, r)
		return
	}

	// Reconcile request fulfillments. Picks up re-imports (the title was
	// already in the library before the webhook fired) immediately. Fresh
	// imports are caught by the scan-completion hook in the adapter, since
	// the scan triggered above runs asynchronously.
	if h.reconciler != nil && payload.EventType == "Download" {
		go h.reconciler.ReconcileFulfillments(context.WithoutCancel(r.Context()))
	}

	h.logger.InfoContext(r.Context(), "arr webhook: scan triggered",
		"library_id", libraryID, "dir", dir, "event_type", payload.EventType)
	respond.Success(w, r, map[string]string{"status": "scan_triggered", "directory": dir})
}

// authenticate checks the API key from the X-Api-Key header.
//
// Header-only — query-string secrets land in nginx access logs, browser
// history, referer headers, and OTel span attributes. Sonarr / Radarr /
// Lidarr all support custom headers in their notification connection
// settings; configure the webhook to send `X-Api-Key: <key>`.
func (h *ArrHandler) authenticate(r *http.Request) bool {
	expected := h.settings.ArrAPIKey(r.Context())
	if expected == "" {
		return false // no key configured — reject all
	}
	provided := r.Header.Get("X-Api-Key")
	if provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

// identifySource returns "radarr", "sonarr", "lidarr", or "unknown".
func (h *ArrHandler) identifySource(p arrPayload) string {
	if p.Movie != nil || p.MovieFile != nil {
		return "radarr"
	}
	if p.Series != nil || p.EpisodeFile != nil {
		return "sonarr"
	}
	if p.Artist != nil || p.TrackFile != nil {
		return "lidarr"
	}
	return "unknown"
}

// applyPathMapping translates a remote path to a local path using configured prefix mappings.
func (h *ArrHandler) applyPathMapping(ctx context.Context, path string) string {
	mappings := h.settings.ArrPathMappings(ctx)
	if len(mappings) == 0 {
		return path
	}
	// Normalise to forward slashes for comparison.
	norm := strings.ReplaceAll(path, `\`, `/`)
	for remote, local := range mappings {
		remoteNorm := strings.ReplaceAll(remote, `\`, `/`)
		if strings.HasPrefix(norm, remoteNorm) {
			rest := norm[len(remoteNorm):]
			// Ensure we join cleanly (no double separators).
			local = strings.TrimRight(local, `/\`)
			rest = strings.TrimLeft(rest, `/\`)
			var mapped string
			if rest == "" {
				mapped = local
			} else {
				mapped = local + string(filepath.Separator) + filepath.FromSlash(rest)
			}
			h.logger.InfoContext(ctx, "arr path mapped",
				"original", path, "mapped", mapped, "rule", remote+" → "+local)
			return mapped
		}
	}
	return path
}

// extractScanDir returns the directory to scan based on the webhook payload.
// Remote paths from arr apps are translated via configured path mappings.
func (h *ArrHandler) extractScanDir(ctx context.Context, p arrPayload) string {
	var dir string

	// Prefer the specific file path → its parent directory.
	switch {
	case p.MovieFile != nil && p.MovieFile.Path != "":
		dir = filepath.Dir(p.MovieFile.Path)
	case p.EpisodeFile != nil && p.EpisodeFile.Path != "":
		dir = filepath.Dir(p.EpisodeFile.Path)
	case p.TrackFile != nil && p.TrackFile.Path != "":
		dir = filepath.Dir(p.TrackFile.Path)
	// Fall back to the media root folder.
	case p.Movie != nil && p.Movie.FolderPath != "":
		dir = strings.TrimRight(p.Movie.FolderPath, `/\`)
	case p.Series != nil && p.Series.Path != "":
		dir = strings.TrimRight(p.Series.Path, `/\`)
	case p.Artist != nil && p.Artist.Path != "":
		dir = strings.TrimRight(p.Artist.Path, `/\`)
	}

	if dir == "" {
		return ""
	}
	return h.applyPathMapping(ctx, dir)
}
