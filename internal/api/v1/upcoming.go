package v1

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/arrcrypt"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// Range and fetch limits for GET /api/v1/upcoming.
const (
	upcomingDateLayout      = "2006-01-02"
	upcomingDefaultSpanDays = 30
	// 62 days covers a Sunday-first month grid (at most 42 days) with room
	// to spare, while keeping one request from asking an *arr app for a year
	// of calendar.
	upcomingMaxSpanDays  = 62
	upcomingFetchTimeout = 10 * time.Second
	upcomingCacheTTL     = 5 * time.Minute
	// A snapshot with a failed service is kept only briefly, so a Sonarr that
	// was restarting doesn't read as "unreachable" for the full five minutes.
	upcomingDegradedTTL = 30 * time.Second
	upcomingCacheCap    = 32
)

// Kinds, release types and statuses in the Upcoming response.
const (
	upcomingKindMovie   = "movie"
	upcomingKindEpisode = "episode"

	upcomingReleaseCinema   = "cinema"
	upcomingReleaseDigital  = "digital"
	upcomingReleasePhysical = "physical"
	upcomingReleaseAiring   = "airing"

	upcomingStatusDownloaded = "downloaded"
	upcomingStatusMissing    = "missing"
	upcomingStatusUpcoming   = "upcoming"
)

// UpcomingDB is the slice of generated queries the Upcoming view needs.
type UpcomingDB interface {
	ListArrServices(ctx context.Context) ([]gen.ArrService, error)
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
}

// UpcomingLibraries lists every library with its scan paths, so an *arr
// folder can be attributed to the OnScreen library that will hold it.
// Satisfied by *library.Service.
type UpcomingLibraries interface {
	List(ctx context.Context) ([]library.Library, error)
}

// UpcomingPathMappings reads the arr_path_mappings setting (remote prefix →
// local prefix) the arr webhook already uses. Satisfied by *settings.Service.
type UpcomingPathMappings interface {
	ArrPathMappings(ctx context.Context) map[string]string
}

// UpcomingHandler serves the Requests page's "Upcoming" calendar: what the
// enabled Radarr and Sonarr instances expect to arrive in a date window.
//
// The upstream fan-out is shared by everyone — the normalised, unfiltered
// result is cached per window, and concurrent misses for the same window
// collapse into one fetch — while the per-user filtering (content-rating
// ceiling, library ACL, item linking) runs on every request after the cache.
type UpcomingHandler struct {
	db        UpcomingDB
	libs      UpcomingLibraries
	access    LibraryAccessChecker
	paths     UpcomingPathMappings
	enc       *auth.Encryptor // optional; opens sealed arr_services.api_key
	arrClient func(baseURL, apiKey string) *arr.Client
	logger    *slog.Logger
	now       func() time.Time

	cache  *upcomingCache
	flight singleflight.Group
}

// NewUpcomingHandler builds the handler. libs, access and paths drive the
// library-ACL filter for non-admin callers and must all be non-nil.
func NewUpcomingHandler(db UpcomingDB, libs UpcomingLibraries, access LibraryAccessChecker, paths UpcomingPathMappings, logger *slog.Logger) *UpcomingHandler {
	return &UpcomingHandler{
		db:        db,
		libs:      libs,
		access:    access,
		paths:     paths,
		arrClient: arr.New,
		logger:    logger,
		now:       time.Now,
		cache:     newUpcomingCache(upcomingCacheCap),
	}
}

// WithEncryptor supplies the key that opens sealed arr_services.api_key
// values. Without it, sealed rows report as misconfigured.
func (h *UpcomingHandler) WithEncryptor(e *auth.Encryptor) *UpcomingHandler {
	h.enc = e
	return h
}

// ── DTOs ──────────────────────────────────────────────────────────────────

// UpcomingItem is one calendar entry. A movie contributes one item per
// release date inside the window; an episode contributes one airing.
type UpcomingItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	ReleaseType string `json:"release_type"`
	Date        string `json:"date"`
	// AllDay is true for movie release dates (group by the UTC date) and
	// false for episode air times (group by the viewer's local date).
	AllDay        bool    `json:"all_day"`
	Status        string  `json:"status"`
	PosterURL     *string `json:"poster_url"`
	Year          *int    `json:"year"`
	Overview      string  `json:"overview"`
	Certification string  `json:"certification"`
	Network       string  `json:"network"`
	Service       string  `json:"service"`
	ItemID        *string `json:"item_id"`
}

// UpcomingService reports one enabled *arr instance's fetch outcome. Error
// is a short generic phrase — the detail (which can include the base URL)
// only goes to the server log.
type UpcomingService struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type upcomingResponse struct {
	From     string            `json:"from"`
	To       string            `json:"to"`
	Items    []UpcomingItem    `json:"items"`
	Services []UpcomingService `json:"services"`
}

// upcomingEntry is a normalised calendar row plus the fields the per-user
// pass needs but the response must not carry. item.Status and item.ItemID
// are left empty in the cache and filled per request: status because it
// depends on the clock, ItemID because it depends on the caller.
type upcomingEntry struct {
	item    UpcomingItem
	date    time.Time
	hasFile bool
	arrPath string // the *arr app's folder for the movie / series, unmapped
	tmdbID  int32  // movie, or series for an episode; 0 = unknown
}

// upcomingSnapshot is the shared, cached result for one window. Treated as
// immutable once built.
type upcomingSnapshot struct {
	entries  []upcomingEntry
	services []UpcomingService
	degraded bool // at least one service failed
}

// ── Handler ───────────────────────────────────────────────────────────────

// Get handles GET /api/v1/upcoming?from=YYYY-MM-DD&to=YYYY-MM-DD.
func (h *UpcomingHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	from, to, msg := parseUpcomingRange(r.URL.Query(), h.now())
	if msg != "" {
		respond.Error(w, r, http.StatusBadRequest, "VALIDATION", msg)
		return
	}

	snap, err := h.snapshot(ctx, from, to)
	if err != nil {
		h.logger.ErrorContext(ctx, "upcoming: build calendar", "err", err)
		respond.InternalError(w, r)
		return
	}

	v, err := h.viewer(ctx, claims)
	if err != nil {
		// Fail closed: without the caller's grants we can't tell which
		// entries belong to libraries they may not see.
		h.logger.ErrorContext(ctx, "upcoming: resolve library access", "err", err)
		respond.InternalError(w, r)
		return
	}
	entries := v.visible(snap.entries)

	now := h.now()
	items := make([]UpcomingItem, len(entries))
	for i, e := range entries {
		items[i] = e.item
		items[i].Status = upcomingStatus(e.hasFile, e.date, now)
	}
	h.link(ctx, v, entries, items)

	respond.Success(w, r, upcomingResponse{
		From:     from.Format(upcomingDateLayout),
		To:       to.Format(upcomingDateLayout),
		Items:    items,
		Services: snap.services,
	})
}

// parseUpcomingRange resolves the inclusive [from, to] day window, both at
// 00:00 UTC. msg is non-empty when the query is invalid.
func parseUpcomingRange(q url.Values, now time.Time) (from, to time.Time, msg string) {
	from = utcDay(now)
	if s := q.Get("from"); s != "" {
		t, err := time.Parse(upcomingDateLayout, s)
		if err != nil {
			return time.Time{}, time.Time{}, "from must be a date in YYYY-MM-DD form"
		}
		from = t
	}
	to = from.AddDate(0, 0, upcomingDefaultSpanDays)
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(upcomingDateLayout, s)
		if err != nil {
			return time.Time{}, time.Time{}, "to must be a date in YYYY-MM-DD form"
		}
		to = t
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, "to must not be before from"
	}
	if to.After(from.AddDate(0, 0, upcomingMaxSpanDays)) {
		return time.Time{}, time.Time{}, "the range may span at most " + strconv.Itoa(upcomingMaxSpanDays) + " days"
	}
	return from, to, ""
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// upcomingStatus: a file on disk wins; otherwise the entry is missing once
// its date has arrived and upcoming before then. A movie released today
// counts as missing, matching how Radarr treats a title on its release day.
func upcomingStatus(hasFile bool, date, now time.Time) string {
	switch {
	case hasFile:
		return upcomingStatusDownloaded
	case date.After(now):
		return upcomingStatusUpcoming
	default:
		return upcomingStatusMissing
	}
}

// ── Shared snapshot ───────────────────────────────────────────────────────

// snapshot returns the cached calendar for the window, fetching it on a
// miss. Concurrent misses for one window share a single fetch, which runs
// detached from the first caller's context so that caller disconnecting
// can't fail the others.
func (h *UpcomingHandler) snapshot(ctx context.Context, from, to time.Time) (*upcomingSnapshot, error) {
	key := from.Format(upcomingDateLayout) + "/" + to.Format(upcomingDateLayout)
	if s, ok := h.cache.get(key, h.now()); ok {
		return s, nil
	}
	v, err, _ := h.flight.Do(key, func() (any, error) {
		if s, ok := h.cache.get(key, h.now()); ok {
			return s, nil
		}
		s, err := h.fetch(context.WithoutCancel(ctx), from, to)
		if err != nil {
			return nil, err
		}
		ttl := upcomingCacheTTL
		if s.degraded {
			ttl = upcomingDegradedTTL
		}
		h.cache.put(key, s, h.now(), ttl)
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*upcomingSnapshot), nil
}

// fetch queries every enabled Radarr / Sonarr concurrently and normalises
// the rows. A failing service is reported in services, never as an error:
// the calendar still shows what the healthy ones returned.
func (h *UpcomingHandler) fetch(ctx context.Context, from, to time.Time) (*upcomingSnapshot, error) {
	rows, err := h.db.ListArrServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list arr services: %w", err)
	}
	svcs := make([]gen.ArrService, 0, len(rows))
	for _, s := range rows {
		if s.Enabled && (s.Kind == "radarr" || s.Kind == "sonarr") {
			svcs = append(svcs, s)
		}
	}

	// The window is inclusive days; upstream wants instants. Both apps
	// treat end as inclusive, so a row stamped exactly at end can come
	// back — normalisation re-checks every date against the day window.
	start, end := from, to.AddDate(0, 0, 1)
	found := make([][]upcomingEntry, len(svcs))
	services := make([]UpcomingService, len(svcs))
	var g errgroup.Group
	for i, svc := range svcs {
		g.Go(func() error {
			entries, err := h.fetchService(ctx, svc, start, end)
			services[i] = UpcomingService{Name: svc.Name, Kind: svc.Kind, OK: err == nil}
			if err != nil {
				services[i].Error = upcomingServiceError(err)
				h.logger.WarnContext(ctx, "upcoming: arr calendar fetch failed",
					"service", svc.Name, "service_id", svc.ID, "kind", svc.Kind, "err", err)
				return nil
			}
			found[i] = entries
			return nil
		})
	}
	_ = g.Wait() // workers never return an error; failures land in services

	snap := &upcomingSnapshot{services: services}
	for i := range svcs {
		snap.entries = append(snap.entries, found[i]...)
		if !services[i].OK {
			snap.degraded = true
		}
	}
	slices.SortFunc(snap.entries, func(a, b upcomingEntry) int {
		if c := a.date.Compare(b.date); c != 0 {
			return c
		}
		if c := cmp.Compare(strings.ToLower(a.item.Title), strings.ToLower(b.item.Title)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.item.Subtitle, b.item.Subtitle); c != 0 {
			return c
		}
		return cmp.Compare(a.item.ID, b.item.ID)
	})
	return snap, nil
}

// errUpcomingKey marks a stored API key that could not be opened.
var errUpcomingKey = errors.New("upcoming: open arr api key")

func (h *UpcomingHandler) fetchService(ctx context.Context, svc gen.ArrService, start, end time.Time) ([]upcomingEntry, error) {
	apiKey, err := arrcrypt.Open(h.enc, svc.ID, svc.ApiKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUpcomingKey, err)
	}
	client := h.arrClient(svc.BaseUrl, apiKey)
	ctx, cancel := context.WithTimeout(ctx, upcomingFetchTimeout)
	defer cancel()

	if svc.Kind == "radarr" {
		movies, err := client.RadarrCalendar(ctx, start, end)
		if err != nil {
			return nil, err
		}
		return normaliseUpcomingMovies(svc, movies, start, end), nil
	}
	episodes, err := client.SonarrCalendar(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return normaliseUpcomingEpisodes(svc, episodes, start, end), nil
}

// upcomingServiceError maps a fetch failure to the generic phrase shown to
// users. Upstream error strings carry the base URL and response bodies, so
// they never leave the server log.
func upcomingServiceError(err error) string {
	var netErr net.Error
	var urlErr *url.Error
	switch {
	case errors.Is(err, errUpcomingKey):
		return "misconfigured"
	case errors.Is(err, arr.ErrUnauthorized):
		return "authentication failed"
	case errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	case errors.As(err, &urlErr):
		return "unreachable"
	case errors.Is(err, arr.ErrNotFound):
		return "calendar not available"
	default:
		return "unexpected response"
	}
}

// normaliseUpcomingMovies turns Radarr rows into one entry per release date
// inside [start, end). Radarr stores release dates as UTC midnights; the
// date is truncated to its UTC day regardless, so a stray time component
// can't move an entry across the window edge.
func normaliseUpcomingMovies(svc gen.ArrService, movies []arr.CalendarMovie, start, end time.Time) []upcomingEntry {
	var out []upcomingEntry
	for _, m := range movies {
		releases := []struct {
			kind string
			at   arr.Timestamp
		}{
			{upcomingReleaseCinema, m.InCinemas},
			{upcomingReleaseDigital, m.DigitalRelease},
			{upcomingReleasePhysical, m.PhysicalRelease},
		}
		for _, rel := range releases {
			if rel.at.IsZero() {
				continue
			}
			day := utcDay(rel.at.Time)
			if day.Before(start) || !day.Before(end) {
				continue
			}
			out = append(out, upcomingEntry{
				item: UpcomingItem{
					ID:            "radarr:" + svc.ID.String() + ":" + strconv.Itoa(m.ID) + ":" + rel.kind,
					Kind:          upcomingKindMovie,
					Title:         m.Title,
					ReleaseType:   rel.kind,
					Date:          day.Format(time.RFC3339),
					AllDay:        true,
					PosterURL:     upcomingPoster(m.Images),
					Year:          positiveInt(m.Year),
					Overview:      m.Overview,
					Certification: m.Certification,
					Service:       svc.Name,
				},
				date:    day,
				hasFile: m.HasFile,
				arrPath: m.Path,
				tmdbID:  int32(m.TMDBID),
			})
		}
	}
	return out
}

// normaliseUpcomingEpisodes turns Sonarr rows into one airing entry each,
// keeping only air times inside [start, end). Everything but the episode
// number and title comes from the embedded series.
func normaliseUpcomingEpisodes(svc gen.ArrService, episodes []arr.CalendarEpisode, start, end time.Time) []upcomingEntry {
	var out []upcomingEntry
	for _, e := range episodes {
		at := e.AirDateUTC.Time
		if at.IsZero() || at.Before(start) || !at.Before(end) {
			continue
		}
		var series arr.CalendarSeries
		if e.Series != nil {
			series = *e.Series
		}
		title := series.Title
		if title == "" {
			title = e.Title
		}
		subtitle := fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
		if t := strings.TrimSpace(e.Title); t != "" {
			subtitle += " · " + t
		}
		out = append(out, upcomingEntry{
			item: UpcomingItem{
				ID:            "sonarr:" + svc.ID.String() + ":" + strconv.Itoa(e.ID),
				Kind:          upcomingKindEpisode,
				Title:         title,
				Subtitle:      subtitle,
				ReleaseType:   upcomingReleaseAiring,
				Date:          at.UTC().Format(time.RFC3339),
				PosterURL:     upcomingPoster(series.Images),
				Year:          positiveInt(series.Year),
				Overview:      e.Overview,
				Certification: series.Certification,
				Network:       series.Network,
				Service:       svc.Name,
			},
			date:    at,
			hasFile: e.HasFile,
			arrPath: series.Path,
			tmdbID:  int32(series.TMDBID),
		})
	}
	return out
}

// upcomingPoster returns the first poster's remote URL, and only when it is
// an absolute https URL. The *arr apps' local /MediaCover paths need the API
// key to fetch, and a plain-http or scheme-relative URL would be mixed
// content (or an arbitrary scheme) in the browser.
func upcomingPoster(images []arr.MovieImage) *string {
	for _, img := range images {
		if !strings.EqualFold(img.CoverType, "poster") || img.RemoteURL == "" {
			continue
		}
		u, err := url.Parse(img.RemoteURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			continue
		}
		s := img.RemoteURL
		return &s
	}
	return nil
}

func positiveInt(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// ── Per-user filtering ────────────────────────────────────────────────────

// upcomingViewer is the caller's view of the catalogue.
type upcomingViewer struct {
	admin   bool
	ceiling string                 // max content rating; "" = unrestricted
	allowed map[uuid.UUID]struct{} // nil = every library
	acl     *upcomingACL           // nil = no library is hidden from the caller
}

// viewer resolves the caller's grants once per request. Admins skip every
// filter.
func (h *UpcomingHandler) viewer(ctx context.Context, claims *auth.Claims) (upcomingViewer, error) {
	if claims.IsAdmin {
		return upcomingViewer{admin: true}, nil
	}
	v := upcomingViewer{ceiling: claims.MaxContentRating}
	allowed, err := h.access.AllowedLibraryIDs(ctx, claims.UserID, false)
	if err != nil {
		return v, fmt.Errorf("allowed libraries: %w", err)
	}
	if allowed == nil {
		return v, nil
	}
	v.allowed = allowed
	libs, err := h.libs.List(ctx)
	if err != nil {
		return v, fmt.Errorf("list libraries: %w", err)
	}
	v.acl = newUpcomingACL(libs, allowed, h.paths.ArrPathMappings(ctx))
	return v, nil
}

// visible drops what the caller may not see: entries above their rating
// ceiling (an empty or unknown certification is most restrictive, so a
// capped profile never sees unrated titles) and entries whose *arr folder
// lies in a library they have no grant on. The response says nothing about
// how many entries were dropped.
func (v upcomingViewer) visible(entries []upcomingEntry) []upcomingEntry {
	if v.admin {
		return entries
	}
	out := make([]upcomingEntry, 0, len(entries))
	for _, e := range entries {
		if !contentrating.IsAllowed(e.item.Certification, v.ceiling) {
			continue
		}
		if v.acl != nil && v.acl.denies(e.arrPath) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// upcomingACL attributes *arr folders to OnScreen libraries.
type upcomingACL struct {
	mappings map[string]string
	roots    []upcomingRoot // longest path first, so nested roots win
	allowed  map[uuid.UUID]struct{}
}

type upcomingRoot struct {
	path      string
	libraryID uuid.UUID
}

// newUpcomingACL returns nil when the caller can see every library — the
// common case, which then skips path mapping entirely.
func newUpcomingACL(libs []library.Library, allowed map[uuid.UUID]struct{}, mappings map[string]string) *upcomingACL {
	a := &upcomingACL{mappings: mappings, allowed: allowed}
	hidden := false
	for _, lib := range libs {
		if _, ok := allowed[lib.ID]; !ok {
			hidden = true
		}
		for _, p := range lib.Paths {
			if p = normUpcomingPath(p); p != "" {
				a.roots = append(a.roots, upcomingRoot{path: p, libraryID: lib.ID})
			}
		}
	}
	if !hidden {
		return nil
	}
	slices.SortFunc(a.roots, func(x, y upcomingRoot) int { return cmp.Compare(len(y.path), len(x.path)) })
	return a
}

// denies reports whether arrPath maps into a library the caller has no
// grant on. A folder no library claims (not imported yet, or outside every
// scan path) is allowed: there is nothing to protect.
func (a *upcomingACL) denies(arrPath string) bool {
	local := mapUpcomingPath(arrPath, a.mappings)
	if local == "" {
		return false
	}
	for _, root := range a.roots {
		if pathWithin(local, root.path) {
			_, ok := a.allowed[root.libraryID]
			return !ok
		}
	}
	return false
}

// mapUpcomingPath rewrites an *arr path through arr_path_mappings, using the
// longest remote prefix that matches on a path boundary. An unmatched path
// is returned normalised but otherwise unchanged — it is already local when
// the *arr app and OnScreen share a mount layout.
func mapUpcomingPath(p string, mappings map[string]string) string {
	np := normUpcomingPath(p)
	if np == "" {
		return ""
	}
	bestRemote, bestLocal := "", ""
	for remote, local := range mappings {
		nr := normUpcomingPath(remote)
		if nr == "" || !pathWithin(np, nr) || len(nr) <= len(bestRemote) {
			continue
		}
		bestRemote, bestLocal = nr, local
	}
	if bestRemote == "" {
		return np
	}
	return normUpcomingPath(bestLocal + "/" + strings.TrimPrefix(np, bestRemote))
}

// normUpcomingPath puts a path in a comparable form: forward slashes, no
// duplicate or trailing separators. Both *arr apps and OnScreen may run on
// either OS, so backslashes are folded before comparing.
func normUpcomingPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// pathWithin reports whether p is root or lies beneath it.
func pathWithin(p, root string) bool {
	return p == root || root == "/" || strings.HasPrefix(p, root+"/")
}

// ── Item linking ──────────────────────────────────────────────────────────

// link sets item_id to the OnScreen movie, or the show for an episode, when
// one exists by TMDB id — one batched lookup per kind, scoped to the
// caller's libraries and rating ceiling so an item they can't open is never
// linked. A lookup failure leaves the items unlinked rather than failing
// the calendar.
func (h *UpcomingHandler) link(ctx context.Context, v upcomingViewer, entries []upcomingEntry, items []UpcomingItem) {
	var movieIDs, showIDs []int32
	for _, e := range entries {
		if e.tmdbID <= 0 {
			continue
		}
		if e.item.Kind == upcomingKindMovie {
			if !slices.Contains(movieIDs, e.tmdbID) {
				movieIDs = append(movieIDs, e.tmdbID)
			}
		} else if !slices.Contains(showIDs, e.tmdbID) {
			showIDs = append(showIDs, e.tmdbID)
		}
	}
	movies := h.lookupItems(ctx, v, "movie", movieIDs)
	shows := h.lookupItems(ctx, v, "show", showIDs)
	for i, e := range entries {
		hits := shows
		if e.item.Kind == upcomingKindMovie {
			hits = movies
		}
		if id, ok := hits[e.tmdbID]; ok {
			s := id.String()
			items[i].ItemID = &s
		}
	}
}

func (h *UpcomingHandler) lookupItems(ctx context.Context, v upcomingViewer, itemType string, tmdbIDs []int32) map[int32]uuid.UUID {
	if len(tmdbIDs) == 0 {
		return nil
	}
	params := gen.ListMediaItemsByTMDBIDsParams{Type: itemType, TmdbIds: tmdbIDs}
	if !v.admin {
		params.MaxRatingRank = maxRatingRankFromClaims(v.ceiling)
		if v.allowed != nil {
			if len(v.allowed) == 0 {
				return nil
			}
			params.LibraryIds = make([]uuid.UUID, 0, len(v.allowed))
			for id := range v.allowed {
				params.LibraryIds = append(params.LibraryIds, id)
			}
		}
	}
	rows, err := h.db.ListMediaItemsByTMDBIDs(ctx, params)
	if err != nil {
		h.logger.WarnContext(ctx, "upcoming: link library items", "type", itemType, "err", err)
		return nil
	}
	out := make(map[int32]uuid.UUID, len(rows))
	for _, row := range rows {
		if row.TmdbID != nil {
			out[*row.TmdbID] = row.ID
		}
	}
	return out
}

// ── Cache ─────────────────────────────────────────────────────────────────

// upcomingCache is a small bounded map of window → snapshot. The key space
// is whatever windows callers ask for, so the cap keeps a client cycling
// through ranges from growing it without bound; when full, expired entries
// go first and then the one closest to expiry.
type upcomingCache struct {
	mu      sync.Mutex
	cap     int
	entries map[string]upcomingCacheEntry
}

type upcomingCacheEntry struct {
	snap    *upcomingSnapshot
	expires time.Time
}

func newUpcomingCache(capacity int) *upcomingCache {
	return &upcomingCache{cap: capacity, entries: make(map[string]upcomingCacheEntry, capacity)}
}

func (c *upcomingCache) get(key string, now time.Time) (*upcomingSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !now.Before(e.expires) {
		return nil, false
	}
	return e.snap, true
}

func (c *upcomingCache) put(key string, snap *upcomingSnapshot, now time.Time, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.cap {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.cap {
			var oldest string
			var oldestAt time.Time
			for k, e := range c.entries {
				if oldest == "" || e.expires.Before(oldestAt) {
					oldest, oldestAt = k, e.expires
				}
			}
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = upcomingCacheEntry{snap: snap, expires: now.Add(ttl)}
}
