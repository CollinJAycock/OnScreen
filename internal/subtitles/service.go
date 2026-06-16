// Package subtitles orchestrates external subtitle search, download, and
// on-disk storage. The provider client (e.g. opensubtitles.Client) handles
// remote IO; this package handles persistence, format conversion, and
// the database row that points the player at the file.
package subtitles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// searchCacheTTL bounds how long a subtitle search result is reused. Searches
// are user-triggered (the player's "find more online"), but the picker can
// re-issue the same query on reopen, and OpenSubtitles' free tier has a tight
// daily allowance — memoizing identical queries for a few minutes cuts that
// burn (and ban risk) without making results feel stale. A NEGATIVE result
// (no matches) is cached too, so re-opening the picker for a title
// OpenSubtitles doesn't have doesn't re-hit the API every time.
const searchCacheTTL = 10 * time.Minute

// ErrNoProvider is returned when the service has no configured provider.
var ErrNoProvider = errors.New("subtitle provider not configured")

// langCodeRe restricts a subtitle language tag to ISO-639 alpha codes with an
// optional region/script subtag (e.g. "en", "pt-BR", "zh-Hant"). The
// security-critical property is that it forbids path separators and ".":
// opts.Language is request-controlled and is interpolated into an on-disk
// filename in Download, so an unrestricted value ("../../…") would traverse
// out of the per-file cache directory.
var langCodeRe = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,4})?$`)

func validSubtitleLang(s string) bool { return langCodeRe.MatchString(s) }

// Provider abstracts the remote subtitle source so tests can swap in a fake.
type Provider interface {
	Configured() bool
	Search(ctx context.Context, opts opensubtitles.SearchOpts) ([]opensubtitles.SearchResult, error)
	Download(ctx context.Context, fileID int) (*opensubtitles.DownloadInfo, error)
	FetchFile(ctx context.Context, link string) ([]byte, error)
}

// Store is the subset of the generated DB layer we need.
type Store interface {
	InsertExternalSubtitle(ctx context.Context, arg gen.InsertExternalSubtitleParams) (gen.ExternalSubtitle, error)
	ListExternalSubtitlesForFile(ctx context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error)
	GetExternalSubtitle(ctx context.Context, id uuid.UUID) (gen.ExternalSubtitle, error)
	DeleteExternalSubtitle(ctx context.Context, id uuid.UUID) error
}

// Service ties a Provider to the Store and the on-disk cache.
type Service struct {
	provider Provider
	store    Store
	cacheDir string // root for *.vtt files, under CACHE_PATH (e.g. /var/cache/onscreen/subtitles)
	logger   *slog.Logger
	ocr      OCREngine // optional; nil disables OCRStream

	searchMu    sync.Mutex
	searchCache map[string]searchCacheEntry
}

type searchCacheEntry struct {
	results []opensubtitles.SearchResult
	fetched time.Time
}

// New constructs a Service. provider may be nil — in that case Search/Download
// return ErrNoProvider, but List/Delete still work for already-stored rows.
func New(provider Provider, store Store, cacheDir string, logger *slog.Logger) *Service {
	return &Service{
		provider:    provider,
		store:       store,
		cacheDir:    cacheDir,
		logger:      logger,
		searchCache: make(map[string]searchCacheEntry),
	}
}

// SearchOpts is what callers pass to Search. Mirrors opensubtitles.SearchOpts
// so handlers don't have to import the provider package.
type SearchOpts struct {
	Query     string
	Year      int
	Season    int
	Episode   int
	IMDBID    string
	TMDBID    int
	Languages string
}

// Search proxies to the provider, memoizing identical queries for
// searchCacheTTL. Returns ErrNoProvider if no provider is wired.
func (s *Service) Search(ctx context.Context, opts SearchOpts) ([]opensubtitles.SearchResult, error) {
	if s.provider == nil || !s.provider.Configured() {
		return nil, ErrNoProvider
	}
	key := fmt.Sprintf("%s|%d|%d|%d|%s|%d|%s",
		opts.Query, opts.Year, opts.Season, opts.Episode, opts.IMDBID, opts.TMDBID, opts.Languages)
	if cached, ok := s.searchCacheGet(key); ok {
		return cached, nil
	}
	results, err := s.provider.Search(ctx, opensubtitles.SearchOpts{
		Query:     opts.Query,
		Year:      opts.Year,
		Season:    opts.Season,
		Episode:   opts.Episode,
		IMDBID:    opts.IMDBID,
		TMDBID:    opts.TMDBID,
		Languages: opts.Languages,
	})
	if err != nil {
		return nil, err
	}
	// Cache successes only (including empty result sets — the negative case).
	// An error (circuit open, network) is never cached, so it self-heals.
	s.searchCacheSet(key, results)
	return results, nil
}

func (s *Service) searchCacheGet(key string) ([]opensubtitles.SearchResult, bool) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	e, ok := s.searchCache[key]
	if ok && time.Since(e.fetched) < searchCacheTTL {
		return e.results, true
	}
	return nil, false
}

func (s *Service) searchCacheSet(key string, results []opensubtitles.SearchResult) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.searchCache == nil {
		s.searchCache = make(map[string]searchCacheEntry)
	}
	s.searchCache[key] = searchCacheEntry{results: results, fetched: time.Now()}
}

// DownloadOpts identifies a search result to fetch and which media file to
// attach it to. Language overrides the provider-reported language when the
// caller knows better.
type DownloadOpts struct {
	FileID          uuid.UUID // media_files.id this subtitle belongs to
	ProviderFileID  int       // remote file id from a SearchResult
	Language        string    // ISO-639-1; defaults to result.Language if empty
	Title           string
	HearingImpaired bool
	Rating          float32
	DownloadCount   int32
}

// Download requests, fetches, normalizes (SRT→VTT), persists to disk, and
// inserts a DB row. Returns the inserted ExternalSubtitle.
func (s *Service) Download(ctx context.Context, opts DownloadOpts) (gen.ExternalSubtitle, error) {
	if s.provider == nil || !s.provider.Configured() {
		return gen.ExternalSubtitle{}, ErrNoProvider
	}
	if opts.FileID == uuid.Nil || opts.ProviderFileID == 0 || opts.Language == "" {
		return gen.ExternalSubtitle{}, errors.New("file_id, provider_file_id, and language are required")
	}
	if !validSubtitleLang(opts.Language) {
		// Path-traversal guard: Language is interpolated into the on-disk
		// filename below; reject anything that isn't a plain language tag.
		return gen.ExternalSubtitle{}, fmt.Errorf("invalid language code %q", opts.Language)
	}

	info, err := s.provider.Download(ctx, opts.ProviderFileID)
	if err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("request download: %w", err)
	}
	raw, err := s.provider.FetchFile(ctx, info.Link)
	if err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("fetch file: %w", err)
	}

	vtt := normalizeToVTT(raw, info.FileName)

	dir := filepath.Join(s.cacheDir, opts.FileID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("mkdir cache: %w", err)
	}
	filename := fmt.Sprintf("%s_%d.vtt", opts.Language, opts.ProviderFileID)
	path := filepath.Join(dir, filename)
	// Defense in depth: ensure the join stayed inside the per-file cache dir
	// even if the language allowlist above is ever loosened.
	if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
		return gen.ExternalSubtitle{}, fmt.Errorf("subtitle path escapes cache dir")
	}
	if err := os.WriteFile(path, vtt, 0o644); err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("write subtitle: %w", err)
	}

	srcID := strconv.Itoa(opts.ProviderFileID)
	titlePtr := nilIfEmpty(opts.Title)
	srcIDPtr := &srcID
	rating := opts.Rating
	dlCount := opts.DownloadCount
	row, err := s.store.InsertExternalSubtitle(ctx, gen.InsertExternalSubtitleParams{
		FileID:        opts.FileID,
		Language:      opts.Language,
		Title:         titlePtr,
		Forced:        false,
		Sdh:           opts.HearingImpaired,
		Source:        "opensubtitles",
		SourceID:      srcIDPtr,
		StoragePath:   path,
		Rating:        &rating,
		DownloadCount: &dlCount,
	})
	if err != nil {
		_ = os.Remove(path)
		return gen.ExternalSubtitle{}, fmt.Errorf("persist row: %w", err)
	}
	if info.Remaining >= 0 {
		s.logger.InfoContext(ctx, "fetched subtitle",
			"file_id", opts.FileID, "lang", opts.Language, "remaining", info.Remaining)
	}
	return row, nil
}

// List returns all stored external subtitles for a file.
func (s *Service) List(ctx context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error) {
	return s.store.ListExternalSubtitlesForFile(ctx, fileID)
}

// Get returns a single external subtitle row by id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (gen.ExternalSubtitle, error) {
	return s.store.GetExternalSubtitle(ctx, id)
}

// Delete removes the DB row and best-effort removes the on-disk file.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	row, err := s.store.GetExternalSubtitle(ctx, id)
	if err == nil && row.StoragePath != "" {
		_ = os.Remove(row.StoragePath)
	}
	return s.store.DeleteExternalSubtitle(ctx, id)
}

// normalizeToVTT converts SRT to WebVTT. WebVTT input passes through untouched.
// We sniff by file extension first, then by content (SRT lacks the WEBVTT header).
func normalizeToVTT(raw []byte, filename string) []byte {
	text := string(raw)
	// Strip BOM that some providers prepend.
	text = strings.TrimPrefix(text, "\ufeff")
	text = sanitizeActiveContent(text)

	if strings.HasPrefix(text, "WEBVTT") {
		return []byte(text)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".vtt" {
		return []byte("WEBVTT\n\n" + text)
	}
	// Treat everything else as SRT — this covers the vast majority of providers.
	return []byte(srtToVTT(text))
}

// srtToVTT converts SRT cue timing (HH:MM:SS,mmm) to VTT (HH:MM:SS.mmm).
// We don't try to validate cue ordering or strip numeric indices — most VTT
// players ignore stray digits between cues.
func srtToVTT(srt string) string {
	srt = strings.ReplaceAll(srt, "\r\n", "\n")
	out := strings.Builder{}
	out.Grow(len(srt) + 16)
	out.WriteString("WEBVTT\n\n")
	for _, line := range strings.Split(srt, "\n") {
		if strings.Contains(line, "-->") {
			line = strings.ReplaceAll(line, ",", ".")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// Active-content patterns stripped from subtitle text before it is cached and
// served. These constructs are never valid in a subtitle but are live XSS
// vectors if a client renders cue text as raw HTML.
var (
	// Paired <script>…</script> (dotall, non-greedy) and any stray script tag.
	cueScriptBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	cueScriptTag   = regexp.MustCompile(`(?i)</?script\b[^>]*>`)
	// Any tag carrying an on*= event handler (e.g. <img onerror=…>) or a
	// javascript: URI (e.g. <a href="javascript:…">). The [^>]* stays inside
	// a single tag, so plain angle-bracket text ("5 < 10", "<MUSIC>") and the
	// legal formatting tags (<i>/<b>/<font color>/…) are left untouched.
	cueEventAttrTag = regexp.MustCompile(`(?i)<[^>]*\son\w+\s*=[^>]*>`)
	cueJSURITag     = regexp.MustCompile(`(?i)<[^>]*javascript:[^>]*>`)
)

// sanitizeActiveContent removes script tags, inline event handlers, and
// javascript: URIs from subtitle text. It is deliberately conservative — it
// targets only constructs that execute code, not the WebVTT/SRT formatting
// tags — so it adds defense-in-depth (the bundled web player already escapes
// cue text) for any future client that renders cue text as HTML without
// corrupting legitimate subtitles.
func sanitizeActiveContent(text string) string {
	text = cueScriptBlock.ReplaceAllString(text, "")
	text = cueScriptTag.ReplaceAllString(text, "")
	text = cueEventAttrTag.ReplaceAllString(text, "")
	text = cueJSURITag.ReplaceAllString(text, "")
	return text
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
