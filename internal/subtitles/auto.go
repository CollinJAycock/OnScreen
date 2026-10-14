package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// Automatic subtitle download: the subtitle_auto_download task looks at
// movies and episodes, newest first, and for each language the admin wants
// that the file has no subtitle in yet — embedded, a sidecar next to the
// file, or an earlier download — searches OpenSubtitles and downloads the
// best match into the same place an on-demand download goes. Off unless an
// admin turns it on (server-wide, with per-library overrides), and silent
// without OpenSubtitles credentials.
//
// Every look at an (item, language) is recorded in subtitle_auto_attempts, so
// nothing is searched on every run: a miss is retried after 1, 7 and 30 days
// and then given up on; a download, or a subtitle that was already there,
// ends it (the latter is looked at again after 30 days in case it was
// removed). The provider's daily download quota is the binding limit — on a
// quota or rate-limit answer the task stops and doesn't search again for a
// day.

// HearingImpaired preferences for automatic downloads.
const (
	HIPrefer = "prefer" // pick a hearing-impaired (SDH) subtitle when there is one
	HIAvoid  = "avoid"  // pick a plain one when there is one
	HIAny    = "any"    // don't weigh the flag
)

// AutoConfig is the server-wide automatic download setting.
type AutoConfig struct {
	Enabled bool
	// Languages wanted, normalised (ParseLanguageList).
	Languages []string
	// PreferHashMatch sends the file's moviehash and ranks a subtitle timed
	// for this exact release above every other.
	PreferHashMatch bool
	// HearingImpaired is HIPrefer, HIAvoid or HIAny.
	HearingImpaired string
}

// AutoQuerier is the database surface the task needs. Satisfied by
// *gen.Queries.
type AutoQuerier interface {
	ListLibrarySubtitleAutoDownload(ctx context.Context) ([]gen.LibrarySubtitleAutoDownload, error)
	ListSubtitleAutoCandidates(ctx context.Context, arg gen.ListSubtitleAutoCandidatesParams) ([]gen.ListSubtitleAutoCandidatesRow, error)
	ListSubtitleAutoAttempts(ctx context.Context, mediaIds []uuid.UUID) ([]gen.SubtitleAutoAttempt, error)
	UpsertSubtitleAutoAttempt(ctx context.Context, arg gen.UpsertSubtitleAutoAttemptParams) error
	ListExternalSubtitleLanguagesForFile(ctx context.Context, fileID uuid.UUID) ([]string, error)
}

// autoFinder is the part of Service the task drives (a fake in tests).
type autoFinder interface {
	Search(ctx context.Context, opts SearchOpts) ([]opensubtitles.SearchResult, error)
	Download(ctx context.Context, opts DownloadOpts) (gen.ExternalSubtitle, error)
	Configured() bool
}

// Configured reports whether a subtitle provider is set up (credentials
// present and the integration enabled).
func (s *Service) Configured() bool {
	return s.provider != nil && s.provider.Configured()
}

// Retry schedule after a search finds nothing: attempts 1, 2 and 3 wait
// these long; the 4th miss gives up.
var autoRetryAfter = []time.Duration{24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}

const (
	// autoMaxSearches bounds the provider searches one run makes. The task
	// runs every 6 hours, so this is at most ~160 searches a day — light next
	// to the API's limits, and plenty while the download quota (5–20 a day
	// on the free tiers) is the real ceiling.
	autoMaxSearches = 40
	// autoCandidateBatch is how many items one run reads per language group.
	autoCandidateBatch = 200
	// autoPresentRecheck: a language found already present is looked at
	// again after this long.
	autoPresentRecheck = 30 * 24 * time.Hour
	// autoQuotaPause: after a quota / rate-limit answer, no searches for a day.
	autoQuotaPause = 24 * time.Hour
	// autoMaxFailures in a row (provider errors other than quota) end a run.
	autoMaxFailures = 3
)

// AutoDownloader runs the subtitle_auto_download task.
type AutoDownloader struct {
	q         AutoQuerier
	finder    autoFinder
	config    func(ctx context.Context) AutoConfig
	libraries func(ctx context.Context) ([]uuid.UUID, error)
	logger    *slog.Logger
	now       func() time.Time
	// hash and sidecars read the local file system; swapped in tests.
	hash     func(path string) (string, error)
	sidecars func(path string) []string

	maxSearches int

	// running keeps a manual "run now" from overlapping the scheduled run.
	running sync.Mutex
	// pausedUntil is set on a quota / rate-limit answer. Per process: the
	// scheduler runs a task on one instance at a time.
	pauseMu     sync.Mutex
	pausedUntil time.Time
}

// NewAutoDownloader wires the task. config is read on every run so a change
// saved in the admin UI applies to the next one; libraries lists every
// library (the candidate query keeps movies and episodes only).
func NewAutoDownloader(q AutoQuerier, svc *Service, config func(ctx context.Context) AutoConfig,
	libraries func(ctx context.Context) ([]uuid.UUID, error), logger *slog.Logger) *AutoDownloader {
	return &AutoDownloader{
		q:           q,
		finder:      svc,
		config:      config,
		libraries:   libraries,
		logger:      logger,
		now:         time.Now,
		hash:        MovieHash,
		sidecars:    SidecarLanguages,
		maxSearches: autoMaxSearches,
	}
}

// errAutoStop ends a run early (quota reached, provider failing).
var errAutoStop = errors.New("stop")

// autoRun is one run's state and tallies.
type autoRun struct {
	cfg        AutoConfig
	searches   int
	downloaded int
	present    int
	missed     int
	failures   int
	stopReason string
}

// Run is the task body: returns the summary the Tasks page shows.
func (a *AutoDownloader) Run(ctx context.Context) (string, error) {
	if !a.running.TryLock() {
		return "already running", nil
	}
	defer a.running.Unlock()

	cfg := a.config(ctx)
	if !a.finder.Configured() {
		return "OpenSubtitles is not set up; nothing to do", nil
	}
	if until := a.paused(); !until.IsZero() {
		return fmt.Sprintf("paused until %s: the OpenSubtitles daily limit was reached", until.Format(time.RFC3339)), nil
	}
	groups, err := a.libraryGroups(ctx, cfg)
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return "automatic subtitle download is off", nil
	}

	run := &autoRun{cfg: cfg}
	// Deterministic order so tests (and logs) are stable.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := a.runGroup(ctx, run, strings.Split(k, ","), groups[k]); err != nil {
			if errors.Is(err, errAutoStop) {
				break
			}
			return run.summary(), err
		}
	}
	return run.summary(), nil
}

func (r *autoRun) summary() string {
	s := fmt.Sprintf("%d searched, %d downloaded, %d not found, %d already had a subtitle",
		r.searches, r.downloaded, r.missed, r.present)
	if r.stopReason != "" {
		s += "; stopped: " + r.stopReason
	}
	return s
}

func (a *AutoDownloader) paused() time.Time {
	a.pauseMu.Lock()
	defer a.pauseMu.Unlock()
	if a.now().Before(a.pausedUntil) {
		return a.pausedUntil
	}
	return time.Time{}
}

func (a *AutoDownloader) pause() {
	a.pauseMu.Lock()
	a.pausedUntil = a.now().Add(autoQuotaPause)
	a.pauseMu.Unlock()
}

// libraryGroups resolves each library's effective setting (its override, else
// the server's) and groups the enabled ones by wanted languages, so each
// group is one candidate query.
func (a *AutoDownloader) libraryGroups(ctx context.Context, cfg AutoConfig) (map[string][]uuid.UUID, error) {
	libs, err := a.libraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	overrides, err := a.q.ListLibrarySubtitleAutoDownload(ctx)
	if err != nil {
		return nil, fmt.Errorf("list library overrides: %w", err)
	}
	byLib := make(map[uuid.UUID]gen.LibrarySubtitleAutoDownload, len(overrides))
	for _, o := range overrides {
		byLib[o.LibraryID] = o
	}
	groups := map[string][]uuid.UUID{}
	for _, id := range libs {
		enabled, langs := cfg.Enabled, cfg.Languages
		if o, ok := byLib[id]; ok {
			if o.Enabled != nil {
				enabled = *o.Enabled
			}
			if o.Languages != nil {
				if l := ParseLanguageList(*o.Languages); len(l) > 0 {
					langs = l
				}
			}
		}
		if !enabled || len(langs) == 0 {
			continue
		}
		k := strings.Join(langs, ",")
		groups[k] = append(groups[k], id)
	}
	return groups, nil
}

// runGroup processes the candidates of libraries sharing one language list.
func (a *AutoDownloader) runGroup(ctx context.Context, run *autoRun, langs []string, libs []uuid.UUID) error {
	if run.searches >= a.maxSearches {
		run.stopReason = "search limit for this run reached"
		return errAutoStop
	}
	cands, err := a.q.ListSubtitleAutoCandidates(ctx, gen.ListSubtitleAutoCandidatesParams{
		LibraryIds:  libs,
		Languages:   langs,
		ResultLimit: autoCandidateBatch,
	})
	if err != nil {
		return fmt.Errorf("list candidates: %w", err)
	}
	if len(cands) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(cands))
	for i, c := range cands {
		ids[i] = c.ID
	}
	rows, err := a.q.ListSubtitleAutoAttempts(ctx, ids)
	if err != nil {
		return fmt.Errorf("list attempts: %w", err)
	}
	attempts := make(map[string]gen.SubtitleAutoAttempt, len(rows))
	for _, row := range rows {
		attempts[row.MediaID.String()+"|"+row.Language] = row
	}
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.processItem(ctx, run, c, langs, attempts); err != nil {
			return err
		}
	}
	return nil
}

// processItem handles every wanted language of one item.
func (a *AutoDownloader) processItem(ctx context.Context, run *autoRun, c gen.ListSubtitleAutoCandidatesRow,
	langs []string, attempts map[string]gen.SubtitleAutoAttempt) error {
	now := a.now()
	var have []string
	haveLoaded := false
	movieHash := ""
	hashTried := false
	for _, lang := range langs {
		prev, seen := attempts[c.ID.String()+"|"+lang]
		if seen {
			if prev.Status == "downloaded" || prev.Status == "gave_up" {
				continue
			}
			if prev.NextAttemptAt.Valid && prev.NextAttemptAt.Time.After(now) {
				continue
			}
		}
		if !haveLoaded {
			have = a.existingLanguages(ctx, c)
			haveLoaded = true
		}
		if containsLang(have, lang) {
			run.present++
			a.record(ctx, c.ID, lang, prev.Attempts, "present", now.Add(autoPresentRecheck), "a subtitle in this language is already there")
			continue
		}
		if run.searches >= a.maxSearches {
			run.stopReason = "search limit for this run reached"
			return errAutoStop
		}
		if run.cfg.PreferHashMatch && !hashTried {
			hashTried = true
			if h, err := a.hash(c.FilePath); err == nil {
				movieHash = h
			}
		}
		run.searches++
		results, err := a.finder.Search(ctx, a.searchOpts(c, lang, movieHash))
		if err != nil {
			return a.providerError(ctx, run, c, lang, "search", err)
		}
		run.failures = 0
		best, ok := pickAutoSubtitle(results, lang, filepath.Base(c.FilePath), run.cfg)
		if !ok {
			run.missed++
			a.recordMiss(ctx, c.ID, lang, prev.Attempts+1, "no matching subtitle")
			continue
		}
		_, err = a.finder.Download(ctx, DownloadOpts{
			FileID:          c.FileID,
			ProviderFileID:  best.FileID,
			Language:        lang,
			Title:           best.Release,
			HearingImpaired: best.HearingImpaired,
			Rating:          best.Rating,
			DownloadCount:   best.DownloadCount,
		})
		if err != nil {
			if isQuotaError(err) {
				return a.providerError(ctx, run, c, lang, "download", err)
			}
			// A download that fails for another reason (a dead link, a file
			// the provider lost) costs the attempt, so a broken result isn't
			// retried on every run.
			a.logger.WarnContext(ctx, "auto subtitle download failed",
				"item_id", c.ID, "lang", lang, "provider_file_id", best.FileID, "err", err)
			run.missed++
			a.recordMiss(ctx, c.ID, lang, prev.Attempts+1, "download failed")
			continue
		}
		run.downloaded++
		a.record(ctx, c.ID, lang, prev.Attempts+1, "downloaded", time.Time{}, truncateResult("downloaded "+best.Release))
		a.logger.InfoContext(ctx, "auto subtitle downloaded",
			"item_id", c.ID, "lang", lang, "release", best.Release, "hash_match", best.MovieHashMatch)
	}
	return nil
}

// providerError decides what a failed search / download means for the run:
// a quota or rate-limit answer pauses the task for a day; repeated other
// failures end this run. Neither costs the item an attempt.
func (a *AutoDownloader) providerError(ctx context.Context, run *autoRun, c gen.ListSubtitleAutoCandidatesRow, lang, op string, err error) error {
	if isQuotaError(err) {
		a.pause()
		run.stopReason = "OpenSubtitles daily limit reached; resuming in 24 hours"
		a.logger.InfoContext(ctx, "auto subtitles: provider limit reached, pausing for a day", "op", op, "err", err)
		return errAutoStop
	}
	if errors.Is(err, ErrNoProvider) {
		run.stopReason = "OpenSubtitles is not set up"
		return errAutoStop
	}
	run.failures++
	a.logger.WarnContext(ctx, "auto subtitles: provider error", "op", op, "item_id", c.ID, "lang", lang, "err", err)
	if run.failures >= autoMaxFailures {
		run.stopReason = "OpenSubtitles is failing; will try again next run"
		return errAutoStop
	}
	return nil
}

// isQuotaError: the provider said the daily download quota is spent or we're
// rate limited (or the client's breaker is open after one of those).
func isQuotaError(err error) bool {
	return errors.Is(err, ErrQuotaExhausted) ||
		errors.Is(err, opensubtitles.ErrRateLimited) ||
		errors.Is(err, opensubtitles.ErrCircuitOpen)
}

func (a *AutoDownloader) recordMiss(ctx context.Context, id uuid.UUID, lang string, attempts int32, result string) {
	if int(attempts) > len(autoRetryAfter) {
		a.record(ctx, id, lang, attempts, "gave_up", time.Time{}, result)
		return
	}
	a.record(ctx, id, lang, attempts, "retry", a.now().Add(autoRetryAfter[attempts-1]), result)
}

func (a *AutoDownloader) record(ctx context.Context, id uuid.UUID, lang string, attempts int32, status string, next time.Time, result string) {
	var nextTS pgtype.Timestamptz
	if !next.IsZero() {
		nextTS = pgtype.Timestamptz{Time: next, Valid: true}
	}
	res := result
	if err := a.q.UpsertSubtitleAutoAttempt(ctx, gen.UpsertSubtitleAutoAttemptParams{
		MediaID:       id,
		Language:      lang,
		Attempts:      attempts,
		Status:        status,
		NextAttemptAt: nextTS,
		LastResult:    &res,
	}); err != nil {
		a.logger.WarnContext(ctx, "auto subtitles: record attempt", "item_id", id, "lang", lang, "err", err)
	}
}

// existingLanguages is every language the item's file already has a subtitle
// in: embedded streams, sidecar files, stored downloads / OCR.
func (a *AutoDownloader) existingLanguages(ctx context.Context, c gen.ListSubtitleAutoCandidatesRow) []string {
	var out []string
	if len(c.SubtitleStreams) > 0 {
		var streams []struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(c.SubtitleStreams, &streams); err == nil {
			for _, s := range streams {
				out = append(out, NormalizeLang(s.Language))
			}
		}
	}
	out = append(out, a.sidecars(c.FilePath)...)
	if ext, err := a.q.ListExternalSubtitleLanguagesForFile(ctx, c.FileID); err == nil {
		for _, l := range ext {
			out = append(out, NormalizeLang(l))
		}
	}
	return out
}

func containsLang(have []string, want string) bool {
	for _, h := range have {
		if langMatches(h, want) {
			return true
		}
	}
	return false
}

// searchOpts builds the provider query: a movie by its IDs (title + year as
// the fallback), an episode by its show's IDs (title as the fallback) plus
// season / episode numbers.
func (a *AutoDownloader) searchOpts(c gen.ListSubtitleAutoCandidatesRow, lang, movieHash string) SearchOpts {
	opts := SearchOpts{Languages: lang, MovieHash: movieHash}
	if c.Type == "episode" {
		opts.Query = c.ShowTitle
		if opts.Query == "" {
			opts.Query = c.Title
		}
		if c.SeasonNumber != nil {
			opts.Season = int(*c.SeasonNumber)
		}
		if c.EpisodeNumber != nil {
			opts.Episode = int(*c.EpisodeNumber)
		}
		if c.ShowImdbID != nil {
			opts.ParentIMDBID = *c.ShowImdbID
		}
		if c.ShowTmdbID != nil {
			opts.ParentTMDBID = int(*c.ShowTmdbID)
		}
		if c.ImdbID != nil {
			opts.IMDBID = *c.ImdbID
		}
		return opts
	}
	opts.Query = c.Title
	if c.Year != nil {
		opts.Year = int(*c.Year)
	}
	if c.ImdbID != nil {
		opts.IMDBID = *c.ImdbID
	}
	if c.TmdbID != nil {
		opts.TMDBID = int(*c.TmdbID)
	}
	return opts
}

// pickAutoSubtitle chooses the result to download for lang: a moviehash
// match first (when preferred), then the release name closest to the file's
// own name, with the hearing-impaired preference, trusted uploaders and
// popularity as tie-breakers. ok=false when no result is in the language.
func pickAutoSubtitle(results []opensubtitles.SearchResult, lang, fileName string, cfg AutoConfig) (opensubtitles.SearchResult, bool) {
	fileTokens := releaseTokens(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	best := -1
	bestScore := math.Inf(-1)
	for i, r := range results {
		if r.FileID == 0 || !langMatches(NormalizeLang(r.Language), lang) {
			continue
		}
		score := 0.0
		if cfg.PreferHashMatch && r.MovieHashMatch {
			score += 1000
		}
		name := r.Release
		if name == "" {
			name = strings.TrimSuffix(r.FileName, filepath.Ext(r.FileName))
		}
		score += 100 * tokenSimilarity(fileTokens, releaseTokens(name))
		switch cfg.HearingImpaired {
		case HIPrefer:
			if r.HearingImpaired {
				score += 50
			}
		case HIAvoid:
			if r.HearingImpaired {
				score -= 50
			}
		}
		if r.FromTrusted {
			score += 10
		}
		score += math.Log10(float64(r.DownloadCount)+1) + float64(r.Rating)/10
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return opensubtitles.SearchResult{}, false
	}
	return results[best], true
}

// releaseTokens splits a release name into lower-case words
// ("Movie.2019.1080p.BluRay.x264-GRP" → movie 2019 1080p bluray x264 grp).
func releaseTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		out[t] = true
	}
	return out
}

// tokenSimilarity is the Jaccard index of two token sets (0..1).
func tokenSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// truncateResult bounds the stored last_result (a release name can be long)
// on a rune boundary.
func truncateResult(s string) string {
	if r := []rune(s); len(r) > 300 {
		return string(r[:300])
	}
	return s
}
