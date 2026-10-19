package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/onscreen/onscreen/internal/audit"
)

// EnrichToolName is the tool a metadata plugin must advertise. OnScreen calls
// it once per item during enrichment, after the built-in agents.
const EnrichToolName = "enrich_item"

// Audit actions for the metadata role. Per-item calls are not audited (a scan
// makes thousands); breaker transitions are, like the notification role's.
const (
	ActionPluginMetadataBreakerOpen  = "plugin.metadata.breaker_open"
	ActionPluginMetadataBreakerClose = "plugin.metadata.breaker_close"
)

// metadataListTTL is how long the enabled metadata-plugin list is cached.
// Enrichment asks for it once per item, so without the cache a 10 000-file
// scan would make 10 000 identical registry queries; with it, an admin's
// add/disable takes effect within this window.
const metadataListTTL = 15 * time.Second

// MetadataRequest is the argument object of the enrich_item tool. Every
// string is untrusted on the plugin side too — titles come from filenames.
type MetadataRequest struct {
	CorrelationID string `json:"correlation_id"`
	ItemID        string `json:"item_id"`
	LibraryID     string `json:"library_id"`
	// ItemType is movie, show, season, episode, artist, album, track, book,
	// audiobook, … — the media_items.type value.
	ItemType      string `json:"item_type"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title,omitempty"`
	Year          int    `json:"year,omitempty"`
	// Index is the episode number, season number or track number.
	Index *int `json:"index,omitempty"`
	// ParentTitle / ParentIndex describe the parent row (the season of an
	// episode, the album of a track, the show of a season);
	// GrandparentTitle is the show of an episode or the artist of a track.
	ParentTitle      string      `json:"parent_title,omitempty"`
	ParentIndex      *int        `json:"parent_index,omitempty"`
	GrandparentTitle string      `json:"grandparent_title,omitempty"`
	ProviderIDs      ProviderIDs `json:"provider_ids"`
	// FileName is the basename of one of the item's files — never the full
	// path, which would disclose the server's directory layout.
	FileName string `json:"file_name,omitempty"`
	// LockedFields names the fields an admin locked. Values returned for
	// them are ignored; the list lets a plugin skip the work.
	LockedFields []string `json:"locked_fields"`
}

// ProviderIDs are the external IDs OnScreen already knows for an item.
type ProviderIDs struct {
	TMDB        int    `json:"tmdb,omitempty"`
	TVDB        int    `json:"tvdb,omitempty"`
	IMDB        string `json:"imdb,omitempty"`
	AniList     int    `json:"anilist,omitempty"`
	MAL         int    `json:"mal,omitempty"`
	MusicBrainz string `json:"musicbrainz,omitempty"`
}

// MetadataMode is how a plugin's values combine with what the item has.
type MetadataMode string

const (
	// MetadataModeFill (the default) sets a field only where the item has
	// no value yet.
	MetadataModeFill MetadataMode = "fill"
	// MetadataModeOverride replaces the item's value with the plugin's.
	MetadataModeOverride MetadataMode = "override"
)

// MetadataResult is one plugin's validated answer. nil / empty fields were
// not returned (or were dropped by validation). The merge into the item lives
// with the enricher (scanner.mergePluginMetadata), which applies the locks.
type MetadataResult struct {
	PluginID   uuid.UUID
	PluginName string
	Mode       MetadataMode

	Title                 *string
	OriginalTitle         *string
	Year                  *int
	Summary               *string
	Tagline               *string
	Rating                *float64
	ContentRating         *string
	Genres                []string
	OriginallyAvailableAt *time.Time
	// PosterURL has passed the plugin's host allowlist (see validPosterURL);
	// the enricher downloads it through the SSRF-guarded artwork client.
	PosterURL string

	TMDBID    *int
	TVDBID    *int
	AniListID *int
	MALID     *int
}

// Empty reports whether the result carries nothing to apply.
func (r MetadataResult) Empty() bool {
	return r.Title == nil && r.OriginalTitle == nil && r.Year == nil &&
		r.Summary == nil && r.Tagline == nil && r.Rating == nil &&
		r.ContentRating == nil && len(r.Genres) == 0 &&
		r.OriginallyAvailableAt == nil && r.PosterURL == "" &&
		r.TMDBID == nil && r.TVDBID == nil && r.AniListID == nil && r.MALID == nil
}

// metadataWire is the JSON shape of an enrich_item result.
type metadataWire struct {
	Mode                  string   `json:"mode"`
	Title                 *string  `json:"title"`
	OriginalTitle         *string  `json:"original_title"`
	Year                  *int     `json:"year"`
	Summary               *string  `json:"summary"`
	Tagline               *string  `json:"tagline"`
	Rating                *float64 `json:"rating"`
	ContentRating         *string  `json:"content_rating"`
	Genres                []string `json:"genres"`
	OriginallyAvailableAt *string  `json:"originally_available_at"`
	PosterURL             *string  `json:"poster_url"`
	ProviderIDs           *struct {
		TMDB    *int `json:"tmdb"`
		TVDB    *int `json:"tvdb"`
		AniList *int `json:"anilist"`
		MAL     *int `json:"mal"`
	} `json:"provider_ids"`
}

// Validation limits for plugin-returned values. Generous for real metadata,
// small enough that a broken plugin can't write megabytes into a row.
const (
	maxTitleLen    = 500
	maxSummaryLen  = 20000
	maxTaglineLen  = 1000
	maxRatingLen   = 32
	maxGenres      = 32
	maxGenreLen    = 100
	maxPosterURLen = 2048
	minYear        = 1800
	maxYear        = 2200
)

// decodeMetadataResult reads an enrich_item result: structuredContent when
// the plugin sent it, otherwise the text content parsed as a JSON object. An
// empty result (no content, "", "{}" or "null") means "nothing to add".
func decodeMetadataResult(res *mcp.CallToolResult) (metadataWire, error) {
	var w metadataWire
	if res == nil {
		return w, nil
	}
	var raw []byte
	switch {
	case len(res.RawStructuredContent) > 0:
		raw = res.RawStructuredContent
	case res.StructuredContent != nil:
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return w, fmt.Errorf("structured content: %w", err)
		}
		raw = b
	default:
		raw = []byte(strings.TrimSpace(resultText(res)))
	}
	if len(raw) == 0 || string(raw) == "null" {
		return w, nil
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return metadataWire{}, fmt.Errorf("enrich_item result is not a JSON object: %w", err)
	}
	return w, nil
}

// toResult validates a decoded wire result. Bad fields are dropped one by one
// (and reported in dropped) rather than discarding the whole answer.
func (w metadataWire) toResult(p Plugin, allowed allowedHostSet) (r MetadataResult, dropped []string) {
	r = MetadataResult{PluginID: p.ID, PluginName: p.Name, Mode: MetadataModeFill}
	switch MetadataMode(strings.ToLower(strings.TrimSpace(w.Mode))) {
	case "", MetadataModeFill:
	case MetadataModeOverride:
		r.Mode = MetadataModeOverride
	default:
		dropped = append(dropped, "mode")
	}

	text := func(name string, v *string, max int) *string {
		if v == nil {
			return nil
		}
		s := strings.TrimSpace(*v)
		if s == "" {
			return nil
		}
		if len(s) > max || strings.ContainsRune(s, 0) {
			dropped = append(dropped, name)
			return nil
		}
		return &s
	}
	r.Title = text("title", w.Title, maxTitleLen)
	r.OriginalTitle = text("original_title", w.OriginalTitle, maxTitleLen)
	r.Summary = text("summary", w.Summary, maxSummaryLen)
	r.Tagline = text("tagline", w.Tagline, maxTaglineLen)
	r.ContentRating = text("content_rating", w.ContentRating, maxRatingLen)

	if w.Year != nil {
		if *w.Year >= minYear && *w.Year <= maxYear {
			y := *w.Year
			r.Year = &y
		} else {
			dropped = append(dropped, "year")
		}
	}
	if w.Rating != nil {
		if *w.Rating >= 0 && *w.Rating <= 10 {
			v := *w.Rating
			r.Rating = &v
		} else {
			dropped = append(dropped, "rating")
		}
	}
	if len(w.Genres) > 0 {
		if len(w.Genres) > maxGenres {
			dropped = append(dropped, "genres")
		} else {
			seen := map[string]bool{}
			for _, g := range w.Genres {
				g = strings.TrimSpace(g)
				if g == "" || len(g) > maxGenreLen || seen[strings.ToLower(g)] {
					continue
				}
				seen[strings.ToLower(g)] = true
				r.Genres = append(r.Genres, g)
			}
		}
	}
	if w.OriginallyAvailableAt != nil && strings.TrimSpace(*w.OriginallyAvailableAt) != "" {
		if t, err := time.Parse("2006-01-02", strings.TrimSpace(*w.OriginallyAvailableAt)); err == nil &&
			t.Year() >= minYear && t.Year() <= maxYear {
			r.OriginallyAvailableAt = &t
		} else {
			dropped = append(dropped, "originally_available_at")
		}
	}
	if w.PosterURL != nil && strings.TrimSpace(*w.PosterURL) != "" {
		if u, ok := validPosterURL(strings.TrimSpace(*w.PosterURL), allowed); ok {
			r.PosterURL = u
		} else {
			dropped = append(dropped, "poster_url")
		}
	}
	if ids := w.ProviderIDs; ids != nil {
		pos := func(name string, v *int) *int {
			if v == nil || *v == 0 {
				return nil
			}
			if *v < 0 || *v > 1<<31-1 {
				dropped = append(dropped, "provider_ids."+name)
				return nil
			}
			n := *v
			return &n
		}
		r.TMDBID = pos("tmdb", ids.TMDB)
		r.TVDBID = pos("tvdb", ids.TVDB)
		r.AniListID = pos("anilist", ids.AniList)
		r.MALID = pos("mal", ids.MAL)
	}
	return r, dropped
}

// validPosterURL accepts an absolute http(s) URL whose host is in the
// plugin's allowlist (its endpoint host plus allowed_hosts). The download
// itself then goes through the artwork manager's safehttp client, which
// refuses loopback / private / link-local addresses at dial time — so a
// plugin can neither point OnScreen at an arbitrary third-party host nor at
// the operator's own network.
func validPosterURL(raw string, allowed allowedHostSet) (string, bool) {
	if len(raw) > maxPosterURLen {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", false
	}
	if !allowed.allows(u.Hostname()) {
		return "", false
	}
	return u.String(), true
}

// MetadataDispatcher calls every enabled metadata plugin for an item.
// Calls are synchronous (the enricher needs the answer) with the standard
// per-call timeout and per-plugin breaker; a failed, slow or broken plugin
// yields no result and never an error the scan has to handle.
type MetadataDispatcher struct {
	registry *Registry
	logger   *slog.Logger
	pool     *clientPool

	// timeout is the per-call deadline (defaultCallTimeout; tests shorten it).
	timeout time.Duration

	listMu   sync.Mutex
	listAt   time.Time
	listed   []Plugin
	now      func() time.Time
	listTTL  time.Duration
	closeOne sync.Once
}

// NewMetadataDispatcher builds a dispatcher. auditLog may be nil.
func NewMetadataDispatcher(reg *Registry, logger *slog.Logger, auditLog *audit.Logger) *MetadataDispatcher {
	return &MetadataDispatcher{
		registry: reg,
		logger:   logger,
		pool: newClientPool(RoleMetadata, logger, auditLog,
			ActionPluginMetadataBreakerOpen, ActionPluginMetadataBreakerClose),
		timeout: defaultCallTimeout,
		now:     time.Now,
		listTTL: metadataListTTL,
	}
}

// plugins returns the enabled metadata plugins, cached for listTTL. A
// registry error yields the last good list (or none) and is logged.
func (d *MetadataDispatcher) plugins(ctx context.Context) []Plugin {
	d.listMu.Lock()
	defer d.listMu.Unlock()
	if !d.listAt.IsZero() && d.now().Sub(d.listAt) < d.listTTL {
		return d.listed
	}
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ps, err := d.registry.ListEnabledByRole(listCtx, RoleMetadata)
	if err != nil {
		d.logger.WarnContext(ctx, "metadata plugins: list", "err", err)
		return d.listed
	}
	d.listed = ps
	d.listAt = d.now()
	return ps
}

// Active reports whether any metadata plugin is enabled, so the enricher can
// skip building a request (a few extra reads) when there is none.
func (d *MetadataDispatcher) Active(ctx context.Context) bool {
	if d == nil {
		return false
	}
	return len(d.plugins(ctx)) > 0
}

// Enrich calls enrich_item on every enabled metadata plugin, in name order,
// and returns their non-empty validated results in that order. Plugins are
// called one after another, never concurrently for one item, so results
// are deterministic.
func (d *MetadataDispatcher) Enrich(ctx context.Context, req MetadataRequest) []MetadataResult {
	if d == nil {
		return nil
	}
	if req.CorrelationID == "" {
		req.CorrelationID = uuid.NewString()
	}
	if req.LockedFields == nil {
		req.LockedFields = []string{}
	}
	var out []MetadataResult
	for _, p := range d.plugins(ctx) {
		if ctx.Err() != nil {
			break
		}
		if r, ok := d.enrichOne(ctx, p, req); ok {
			out = append(out, r)
		}
	}
	return out
}

func (d *MetadataDispatcher) enrichOne(ctx context.Context, p Plugin, req MetadataRequest) (MetadataResult, bool) {
	e, err := d.pool.entryFor(p)
	if err != nil {
		if !errors.Is(err, errDispatcherClosed) {
			d.logger.WarnContext(ctx, "metadata plugin: build client", "plugin", p.Name, "err", err)
		}
		return MetadataResult{}, false
	}
	res, err := d.pool.call(ctx, e, EnrichToolName, req, d.timeout)
	if err != nil {
		var open *BreakerOpenError
		var notAdvertised *ToolNotAdvertisedError
		switch {
		case errors.As(err, &open):
			d.logger.DebugContext(ctx, "metadata plugin skipped, breaker open",
				"plugin", p.Name, "item_id", req.ItemID, "open_until", open.Until)
		case errors.As(err, &notAdvertised):
			if e.noToolWarned.CompareAndSwap(false, true) {
				d.logger.WarnContext(ctx, "metadata plugin does not advertise enrich_item; skipping it",
					"plugin", p.Name)
			}
		}
		// Other failures were logged by pool.call with the breaker count.
		return MetadataResult{}, false
	}
	wire, err := decodeMetadataResult(res)
	if err != nil {
		d.logger.WarnContext(ctx, "metadata plugin returned an unreadable result",
			"plugin", p.Name, "item_id", req.ItemID, "correlation_id", req.CorrelationID, "err", err)
		return MetadataResult{}, false
	}
	r, dropped := wire.toResult(p, e.allowed)
	if len(dropped) > 0 {
		d.logger.WarnContext(ctx, "metadata plugin fields rejected",
			"plugin", p.Name, "item_id", req.ItemID, "fields", dropped)
	}
	if r.Empty() {
		return MetadataResult{}, false
	}
	return r, true
}

// Close shuts the cached plugin connections down.
func (d *MetadataDispatcher) Close() {
	if d == nil {
		return
	}
	d.closeOne.Do(d.pool.close)
}
