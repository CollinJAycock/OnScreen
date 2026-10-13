// Package podcast keeps podcast RSS subscriptions: an admin subscribes a
// podcast library to a feed, a refresh lists the feed's episodes, and chosen
// episodes download into the library's folder (<scan path>/<feed title>/).
// The scanner turns each downloaded file into an ordinary podcast_episode
// item, taking its title, show notes and date from the feed, so every client
// plays a subscribed episode the way it plays a file dropped into the folder.
//
// Downloads go through the media store (local disk or S3), feed and episode
// fetches through the SSRF-guarded client (internal/safehttp).
package podcast

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// Querier is the slice of generated queries the service uses.
type Querier interface {
	CreatePodcastFeed(ctx context.Context, arg gen.CreatePodcastFeedParams) (gen.PodcastFeed, error)
	GetPodcastFeed(ctx context.Context, id uuid.UUID) (gen.PodcastFeed, error)
	ListPodcastFeeds(ctx context.Context, libraryID pgtype.UUID) ([]gen.ListPodcastFeedsRow, error)
	UpdatePodcastFeedSettings(ctx context.Context, arg gen.UpdatePodcastFeedSettingsParams) (gen.PodcastFeed, error)
	UpdatePodcastFeedFromRefresh(ctx context.Context, arg gen.UpdatePodcastFeedFromRefreshParams) error
	SetPodcastFeedChecked(ctx context.Context, arg gen.SetPodcastFeedCheckedParams) error
	SetPodcastFeedShowItem(ctx context.Context, arg gen.SetPodcastFeedShowItemParams) error
	DeletePodcastFeed(ctx context.Context, id uuid.UUID) error
	GetPodcastFeedByFolder(ctx context.Context, folder string) (gen.GetPodcastFeedByFolderRow, error)
	ListPodcastEpisodeGUIDs(ctx context.Context, feedID uuid.UUID) ([]string, error)
	UpsertPodcastEpisode(ctx context.Context, arg gen.UpsertPodcastEpisodeParams) (gen.PodcastFeedEpisode, error)
	GetPodcastEpisode(ctx context.Context, id uuid.UUID) (gen.PodcastFeedEpisode, error)
	ListPodcastEpisodes(ctx context.Context, arg gen.ListPodcastEpisodesParams) ([]gen.PodcastFeedEpisode, error)
	CountPodcastEpisodes(ctx context.Context, feedID uuid.UUID) (int64, error)
	ListLatestPodcastEpisodes(ctx context.Context, arg gen.ListLatestPodcastEpisodesParams) ([]gen.PodcastFeedEpisode, error)
	ListDownloadedPodcastEpisodes(ctx context.Context, feedID uuid.UUID) ([]gen.PodcastFeedEpisode, error)
	ClaimPodcastEpisodeDownload(ctx context.Context, arg gen.ClaimPodcastEpisodeDownloadParams) (gen.PodcastFeedEpisode, error)
	FinishPodcastEpisodeDownload(ctx context.Context, id uuid.UUID) error
	FailPodcastEpisodeDownload(ctx context.Context, arg gen.FailPodcastEpisodeDownloadParams) error
	MarkPodcastEpisodeRemoved(ctx context.Context, id uuid.UUID) error
	ResetStalePodcastDownloads(ctx context.Context, updatedAt pgtype.Timestamptz) (int64, error)
	GetPodcastEpisodeByFilePath(ctx context.Context, filePath *string) (gen.GetPodcastEpisodeByFilePathRow, error)
	LinkPodcastEpisodeItem(ctx context.Context, arg gen.LinkPodcastEpisodeItemParams) error
}

// Libraries looks up the library a feed belongs to.
type Libraries interface {
	Get(ctx context.Context, id uuid.UUID) (*library.Library, error)
}

// Store is the media store the episodes are written to and deleted from.
type Store interface {
	mediastore.Store
	mediastore.StreamPutter
	mediastore.Deleter
}

// DirScanner rescans one folder of a library, so a downloaded or deleted
// episode shows up (or goes) without waiting for the next full scan.
type DirScanner interface {
	TriggerDirectoryScan(ctx context.Context, libraryID uuid.UUID, dir string) error
}

// PosterFetcher saves a feed's artwork as <folder>/poster.jpg, which the
// scanner uses as the show's poster. ReplaceShowPoster overwrites it (the
// feed changed its artwork); DownloadPoster keeps an existing file.
type PosterFetcher interface {
	DownloadPoster(ctx context.Context, itemID uuid.UUID, url, absDir string) (string, error)
	ReplaceShowPoster(ctx context.Context, itemID uuid.UUID, url, absDir string) (string, error)
}

// LibraryFiles drops the library's record of a file the service deleted, the
// way a full scan drops a file that vanished: a folder rescan doesn't look for
// missing files, so without this a deleted episode stayed listed until the
// next full scan. CleanupEmptyItems then hides a show left with no episodes.
// Satisfied by *media.Service.
type LibraryFiles interface {
	GetFileByPath(ctx context.Context, path string) (*media.File, error)
	DeleteFile(ctx context.Context, id uuid.UUID) error
	SoftDeleteItemIfEmpty(ctx context.Context, id uuid.UUID) error
	CleanupEmptyItems(ctx context.Context, libraryID uuid.UUID) error
}

// Errors the API maps to client responses.
var (
	ErrNotPodcastLibrary   = errors.New("podcast feeds can only be added to a podcast library")
	ErrNoLibraryPath       = errors.New("the library has no folder to download episodes into")
	ErrInvalidFeedURL      = errors.New("the feed URL must be an http or https URL")
	ErrAlreadySubscribed   = errors.New("this library is already subscribed to that feed")
	ErrNoAudioEpisodes     = errors.New("the feed has no audio episodes")
	ErrFeedNotFound        = errors.New("podcast feed not found")
	ErrEpisodeNotFound     = errors.New("podcast episode not found")
	ErrNotDownloadable     = errors.New("the episode is already downloading or downloaded")
	ErrNotDownloaded       = errors.New("the episode isn't downloaded")
	ErrInvalidKeepLatest   = errors.New("keep_latest must be between 0 and 1000")
	ErrInvalidDownloadSize = errors.New("download_latest must be between 0 and 50")
)

// FetchError is a feed that couldn't be fetched or read, with a message fit
// for an admin.
type FetchError struct{ Msg string }

func (e *FetchError) Error() string { return e.Msg }

const (
	// maxFeedBytes caps a feed document. Large back catalogues run to a few
	// MB; 32 MB is far past any real feed.
	maxFeedBytes = 32 << 20
	// maxEpisodeBytes caps one episode download.
	maxEpisodeBytes = 4 << 30
	// feedTimeout bounds a feed fetch; downloadTimeout one episode.
	feedTimeout     = 60 * time.Second
	downloadTimeout = 3 * time.Hour
	// maxAutoDownloadsPerRefresh bounds what one refresh downloads on its
	// own, so a feed that re-keys its whole back catalogue doesn't download
	// all of it.
	maxAutoDownloadsPerRefresh = 10
	// maxConcurrentDownloads is downloads running at once on this server.
	maxConcurrentDownloads = 2
	// staleDownloadAfter is how long a claimed download may go without
	// finishing before a refresh treats it as interrupted.
	staleDownloadAfter = downloadTimeout + 30*time.Minute

	userAgent = "OnScreen/2 (+https://github.com/CollinJAycock/OnScreen) podcast"
)

// Service manages podcast subscriptions. Safe for concurrent use.
type Service struct {
	q      Querier
	libs   Libraries
	store  Store
	scans  DirScanner
	poster PosterFetcher
	files  LibraryFiles
	logger *slog.Logger

	feedClient     *http.Client
	downloadClient *http.Client

	// bg outlives the request that starts a download; downloads holds the
	// download slots.
	bg        context.Context
	downloads chan struct{}
}

// New builds the service. bg is the server's lifetime context: downloads run
// in the background under it. poster may be nil (no show artwork).
func New(bg context.Context, q Querier, libs Libraries, store Store, scans DirScanner, poster PosterFetcher, logger *slog.Logger) *Service {
	return &Service{
		q:              q,
		libs:           libs,
		store:          store,
		scans:          scans,
		poster:         poster,
		logger:         logger,
		feedClient:     safehttp.NewClient(safehttp.DialPolicy{}, feedTimeout),
		downloadClient: safehttp.NewClient(safehttp.DialPolicy{}, 0),
		bg:             bg,
		downloads:      make(chan struct{}, maxConcurrentDownloads),
	}
}

// WithLibraryFiles lets the service take deleted episodes out of the library
// at once (see LibraryFiles). Without it they go at the next full scan.
func (s *Service) WithLibraryFiles(f LibraryFiles) *Service {
	s.files = f
	return s
}

// WithHTTPClient replaces the feed and download clients — tests point them
// at an httptest server, which the SSRF guard would refuse.
func (s *Service) WithHTTPClient(c *http.Client) *Service {
	s.feedClient = c
	s.downloadClient = c
	return s
}

// SubscribeParams is a new subscription.
type SubscribeParams struct {
	LibraryID    uuid.UUID
	FeedURL      string
	AutoDownload bool
	KeepLatest   int
	// DownloadLatest is how many of the newest episodes to download now.
	DownloadLatest int
	CreatedBy      *uuid.UUID
}

// Subscribe fetches the feed, records it and its episodes, and starts the
// first downloads.
func (s *Service) Subscribe(ctx context.Context, p SubscribeParams) (gen.PodcastFeed, error) {
	feedURL, err := normalizeFeedURL(p.FeedURL)
	if err != nil {
		return gen.PodcastFeed{}, err
	}
	if p.KeepLatest < 0 || p.KeepLatest > 1000 {
		return gen.PodcastFeed{}, ErrInvalidKeepLatest
	}
	if p.DownloadLatest < 0 || p.DownloadLatest > 50 {
		return gen.PodcastFeed{}, ErrInvalidDownloadSize
	}
	// Don't download what retention would delete at the next refresh.
	if p.KeepLatest > 0 && p.DownloadLatest > p.KeepLatest {
		p.DownloadLatest = p.KeepLatest
	}
	lib, err := s.libs.Get(ctx, p.LibraryID)
	if err != nil {
		return gen.PodcastFeed{}, err
	}
	if lib.Type != "podcast" {
		return gen.PodcastFeed{}, ErrNotPodcastLibrary
	}
	if len(lib.Paths) == 0 || strings.TrimSpace(lib.Paths[0]) == "" {
		return gen.PodcastFeed{}, ErrNoLibraryPath
	}

	res, err := s.fetch(ctx, feedURL, nil, nil)
	if err != nil {
		return gen.PodcastFeed{}, err
	}
	if len(res.feed.Episodes) == 0 {
		return gen.PodcastFeed{}, ErrNoAudioEpisodes
	}

	folder, err := s.feedFolder(ctx, lib, res.feed.Title, feedURL)
	if err != nil {
		return gen.PodcastFeed{}, err
	}
	createdBy := pgtype.UUID{}
	if p.CreatedBy != nil {
		createdBy = pgtype.UUID{Bytes: *p.CreatedBy, Valid: true}
	}
	f := res.feed
	row, err := s.q.CreatePodcastFeed(ctx, gen.CreatePodcastFeedParams{
		LibraryID:    lib.ID,
		FeedUrl:      feedURL,
		Title:        f.Title,
		Author:       nonEmpty(f.Author),
		Description:  nonEmpty(f.Description),
		ImageUrl:     nonEmpty(f.ImageURL),
		SiteUrl:      nonEmpty(f.SiteURL),
		Language:     nonEmpty(f.Language),
		Folder:       folder,
		AutoDownload: p.AutoDownload,
		KeepLatest:   int32(p.KeepLatest),
		Etag:         nonEmpty(res.etag),
		LastModified: nonEmpty(res.lastModified),
		CreatedBy:    createdBy,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return gen.PodcastFeed{}, ErrAlreadySubscribed
		}
		return gen.PodcastFeed{}, fmt.Errorf("create podcast feed: %w", err)
	}
	if _, err := s.storeEpisodes(ctx, row.ID, f.Episodes); err != nil {
		return row, err
	}
	s.savePoster(ctx, row.Folder, f.ImageURL, false)

	if p.DownloadLatest > 0 {
		latest, err := s.q.ListLatestPodcastEpisodes(ctx, gen.ListLatestPodcastEpisodesParams{
			FeedID: row.ID, Limit: int32(p.DownloadLatest),
		})
		if err != nil {
			return row, fmt.Errorf("list latest episodes: %w", err)
		}
		s.startDownloads(row, latest)
	}
	s.logger.InfoContext(ctx, "podcast subscribed",
		"feed_id", row.ID, "library_id", lib.ID, "title", row.Title,
		"episodes", len(f.Episodes), "downloading", p.DownloadLatest)
	return row, nil
}

// feedFolder is <first scan path>/<feed title>, made unique among all feeds:
// two podcast libraries can share a scan path, and a folder belongs to one
// feed.
func (s *Service) feedFolder(ctx context.Context, lib *library.Library, title, feedURL string) (string, error) {
	root := filepath.Clean(lib.Paths[0])
	name := SafeName(title, "Podcast")
	folder := filepath.Join(root, name)
	existing, err := s.q.ListPodcastFeeds(ctx, pgtype.UUID{})
	if err != nil {
		return "", fmt.Errorf("list podcast feeds: %w", err)
	}
	for _, f := range existing {
		if strings.EqualFold(filepath.Clean(f.Folder), folder) {
			return filepath.Join(root, Disambiguate(name, "", feedURL)), nil
		}
	}
	return folder, nil
}

// storeEpisodes records the feed's episodes, returning those it hadn't
// listed before.
func (s *Service) storeEpisodes(ctx context.Context, feedID uuid.UUID, eps []Episode) ([]gen.PodcastFeedEpisode, error) {
	known, err := s.q.ListPodcastEpisodeGUIDs(ctx, feedID)
	if err != nil {
		return nil, fmt.Errorf("list episode guids: %w", err)
	}
	seen := make(map[string]bool, len(known))
	for _, g := range known {
		seen[g] = true
	}
	var added []gen.PodcastFeedEpisode
	for _, ep := range eps {
		row, err := s.q.UpsertPodcastEpisode(ctx, gen.UpsertPodcastEpisodeParams{
			FeedID:          feedID,
			Guid:            ep.GUID,
			Title:           ep.Title,
			Description:     nonEmpty(ep.Description),
			PublishedAt:     timestamptz(ep.PublishedAt),
			DurationMs:      ep.DurationMS,
			EnclosureUrl:    ep.EnclosureURL,
			EnclosureType:   nonEmpty(ep.EnclosureType),
			EnclosureLength: ep.EnclosureLength,
			Season:          ep.Season,
			Episode:         ep.Episode,
			ImageUrl:        nonEmpty(ep.ImageURL),
		})
		if err != nil {
			return added, fmt.Errorf("store episode %q: %w", ep.GUID, err)
		}
		if !seen[ep.GUID] {
			added = append(added, row)
		}
	}
	return added, nil
}

// RefreshResult reports one feed refresh.
type RefreshResult struct {
	NewEpisodes int  `json:"new_episodes"`
	Downloading int  `json:"downloading"`
	Removed     int  `json:"removed"`
	NotModified bool `json:"not_modified"`
}

// Refresh re-reads one feed: new episodes are listed (and downloaded, with
// auto-download on), the feed's details updated, and retention applied.
func (s *Service) Refresh(ctx context.Context, feedID uuid.UUID) (RefreshResult, error) {
	feed, err := s.getFeed(ctx, feedID)
	if err != nil {
		return RefreshResult{}, err
	}
	return s.refresh(ctx, feed)
}

func (s *Service) refresh(ctx context.Context, feed gen.PodcastFeed) (RefreshResult, error) {
	var out RefreshResult
	res, err := s.fetch(ctx, feed.FeedUrl, feed.Etag, feed.LastModified)
	if err != nil {
		msg := err.Error()
		if cerr := s.q.SetPodcastFeedChecked(ctx, gen.SetPodcastFeedCheckedParams{ID: feed.ID, LastError: &msg}); cerr != nil {
			s.logger.WarnContext(ctx, "record podcast refresh error", "feed_id", feed.ID, "err", cerr)
		}
		return out, err
	}
	if res.notModified {
		out.NotModified = true
		if err := s.q.SetPodcastFeedChecked(ctx, gen.SetPodcastFeedCheckedParams{ID: feed.ID}); err != nil {
			return out, fmt.Errorf("record podcast refresh: %w", err)
		}
	} else {
		f := res.feed
		added, err := s.storeEpisodes(ctx, feed.ID, f.Episodes)
		if err != nil {
			return out, err
		}
		out.NewEpisodes = len(added)
		title := f.Title
		if title == "" {
			title = feed.Title
		}
		if err := s.q.UpdatePodcastFeedFromRefresh(ctx, gen.UpdatePodcastFeedFromRefreshParams{
			ID:           feed.ID,
			Title:        title,
			Author:       nonEmpty(f.Author),
			Description:  nonEmpty(f.Description),
			ImageUrl:     nonEmpty(f.ImageURL),
			SiteUrl:      nonEmpty(f.SiteURL),
			Language:     nonEmpty(f.Language),
			Etag:         nonEmpty(res.etag),
			LastModified: nonEmpty(res.lastModified),
		}); err != nil {
			return out, fmt.Errorf("update podcast feed: %w", err)
		}
		if f.ImageURL != "" && (feed.ImageUrl == nil || *feed.ImageUrl != f.ImageURL) {
			s.savePoster(ctx, feed.Folder, f.ImageURL, feed.ImageUrl != nil)
		}
		if feed.AutoDownload && len(added) > 0 {
			queue := newestFirst(added)
			if len(queue) > maxAutoDownloadsPerRefresh {
				queue = queue[:maxAutoDownloadsPerRefresh]
			}
			out.Downloading = len(queue)
			s.startDownloads(feed, queue)
		}
	}
	removed, err := s.applyRetention(ctx, feed)
	out.Removed = removed
	if err != nil {
		return out, err
	}
	return out, nil
}

// RefreshAll refreshes every feed: the hourly podcast_refresh task. It also
// hands back downloads a crash left claimed, so they can be retried.
func (s *Service) RefreshAll(ctx context.Context) (string, error) {
	if n, err := s.q.ResetStalePodcastDownloads(ctx, pgtype.Timestamptz{
		Time: time.Now().Add(-staleDownloadAfter), Valid: true,
	}); err != nil {
		s.logger.WarnContext(ctx, "reset stale podcast downloads", "err", err)
	} else if n > 0 {
		s.logger.InfoContext(ctx, "podcast downloads interrupted, marked failed", "count", n)
	}
	feeds, err := s.q.ListPodcastFeeds(ctx, pgtype.UUID{})
	if err != nil {
		return "", fmt.Errorf("list podcast feeds: %w", err)
	}
	var newEps, downloading, removed, failed int
	for _, row := range feeds {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		res, err := s.refresh(ctx, feedFromRow(row))
		newEps += res.NewEpisodes
		downloading += res.Downloading
		removed += res.Removed
		if err != nil {
			failed++
			s.logger.WarnContext(ctx, "podcast refresh failed", "feed_id", row.ID, "title", row.Title, "err", err)
		}
	}
	summary := fmt.Sprintf("%d feeds: %d new episodes, %d downloading, %d removed by retention",
		len(feeds), newEps, downloading, removed)
	if failed > 0 {
		summary += fmt.Sprintf(", %d failed to refresh", failed)
	}
	return summary, nil
}

// Download starts downloading one episode in the background.
func (s *Service) Download(ctx context.Context, episodeID uuid.UUID) (gen.PodcastFeedEpisode, error) {
	ep, err := s.q.GetPodcastEpisode(ctx, episodeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ep, ErrEpisodeNotFound
		}
		return ep, fmt.Errorf("get podcast episode: %w", err)
	}
	feed, err := s.getFeed(ctx, ep.FeedID)
	if err != nil {
		return ep, err
	}
	claimed, err := s.claim(ctx, feed, ep)
	if err != nil {
		return ep, err
	}
	go s.runDownload(feed, claimed)
	return claimed, nil
}

// DeleteDownload deletes a downloaded episode's file. The episode stays
// listed, marked removed, so auto-download won't fetch it again.
func (s *Service) DeleteDownload(ctx context.Context, episodeID uuid.UUID) error {
	ep, err := s.q.GetPodcastEpisode(ctx, episodeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEpisodeNotFound
		}
		return fmt.Errorf("get podcast episode: %w", err)
	}
	if ep.Status != "downloaded" || ep.FilePath == nil {
		return ErrNotDownloaded
	}
	feed, err := s.getFeed(ctx, ep.FeedID)
	if err != nil {
		return err
	}
	if err := s.removeFile(ctx, ep); err != nil {
		return err
	}
	s.tidyLibrary(ctx, feed.LibraryID)
	s.rescan(feed)
	return nil
}

// UpdateSettings changes a feed's auto-download and retention, applying the
// new retention at once.
func (s *Service) UpdateSettings(ctx context.Context, feedID uuid.UUID, autoDownload bool, keepLatest int) (gen.PodcastFeed, error) {
	if keepLatest < 0 || keepLatest > 1000 {
		return gen.PodcastFeed{}, ErrInvalidKeepLatest
	}
	row, err := s.q.UpdatePodcastFeedSettings(ctx, gen.UpdatePodcastFeedSettingsParams{
		ID: feedID, AutoDownload: autoDownload, KeepLatest: int32(keepLatest),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return row, ErrFeedNotFound
		}
		return row, fmt.Errorf("update podcast feed: %w", err)
	}
	if _, err := s.applyRetention(ctx, row); err != nil {
		return row, err
	}
	return row, nil
}

// Unsubscribe deletes a feed. With deleteFiles its downloaded episodes are
// deleted too; without, they stay in the library as plain files.
func (s *Service) Unsubscribe(ctx context.Context, feedID uuid.UUID, deleteFiles bool) error {
	feed, err := s.getFeed(ctx, feedID)
	if err != nil {
		return err
	}
	if deleteFiles {
		eps, err := s.q.ListDownloadedPodcastEpisodes(ctx, feed.ID)
		if err != nil {
			return fmt.Errorf("list downloaded episodes: %w", err)
		}
		for _, ep := range eps {
			if err := s.removeFile(ctx, ep); err != nil {
				return err
			}
		}
	}
	if err := s.q.DeletePodcastFeed(ctx, feed.ID); err != nil {
		return fmt.Errorf("delete podcast feed: %w", err)
	}
	if deleteFiles {
		s.tidyLibrary(ctx, feed.LibraryID)
		s.rescan(feed)
	}
	s.logger.InfoContext(ctx, "podcast unsubscribed", "feed_id", feed.ID, "title", feed.Title, "files_deleted", deleteFiles)
	return nil
}

// Feeds lists feeds, all or one library's.
func (s *Service) Feeds(ctx context.Context, libraryID *uuid.UUID) ([]gen.ListPodcastFeedsRow, error) {
	arg := pgtype.UUID{}
	if libraryID != nil {
		arg = pgtype.UUID{Bytes: *libraryID, Valid: true}
	}
	return s.q.ListPodcastFeeds(ctx, arg)
}

// Feed returns one feed.
func (s *Service) Feed(ctx context.Context, feedID uuid.UUID) (gen.PodcastFeed, error) {
	return s.getFeed(ctx, feedID)
}

// Episodes pages through a feed's episodes, newest first.
func (s *Service) Episodes(ctx context.Context, feedID uuid.UUID, limit, offset int32) ([]gen.PodcastFeedEpisode, int64, error) {
	if _, err := s.getFeed(ctx, feedID); err != nil {
		return nil, 0, err
	}
	rows, err := s.q.ListPodcastEpisodes(ctx, gen.ListPodcastEpisodesParams{FeedID: feedID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, fmt.Errorf("list podcast episodes: %w", err)
	}
	total, err := s.q.CountPodcastEpisodes(ctx, feedID)
	if err != nil {
		return nil, 0, fmt.Errorf("count podcast episodes: %w", err)
	}
	return rows, total, nil
}

func (s *Service) getFeed(ctx context.Context, id uuid.UUID) (gen.PodcastFeed, error) {
	f, err := s.q.GetPodcastFeed(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return f, ErrFeedNotFound
		}
		return f, fmt.Errorf("get podcast feed: %w", err)
	}
	return f, nil
}

// ── Downloads ─────────────────────────────────────────────────────────────

// claim picks the episode's file path and marks it downloading. The path is
// recorded before any byte is written so the scanner can match the file to
// the episode whenever it sees it.
func (s *Service) claim(ctx context.Context, feed gen.PodcastFeed, ep gen.PodcastFeedEpisode) (gen.PodcastFeedEpisode, error) {
	ext := EpisodeExt(ep.EnclosureUrl, deref(ep.EnclosureType))
	var published *time.Time
	if ep.PublishedAt.Valid {
		published = &ep.PublishedAt.Time
	}
	name := EpisodeFileName(ep.Title, published, ext)
	path := filepath.Join(feed.Folder, name)
	if s.pathTaken(ctx, path, ep.ID) {
		path = filepath.Join(feed.Folder, Disambiguate(name, ext, ep.Guid))
	}
	claimed, err := s.q.ClaimPodcastEpisodeDownload(ctx, gen.ClaimPodcastEpisodeDownloadParams{ID: ep.ID, FilePath: &path})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ep, ErrNotDownloadable
		}
		return ep, fmt.Errorf("claim podcast download: %w", err)
	}
	return claimed, nil
}

// pathTaken reports whether path belongs to another episode or holds a file
// already (one an admin dropped in, say).
func (s *Service) pathTaken(ctx context.Context, path string, episodeID uuid.UUID) bool {
	if other, err := s.q.GetPodcastEpisodeByFilePath(ctx, &path); err == nil && other.ID != episodeID {
		return true
	}
	if _, err := s.store.Stat(ctx, path); err == nil {
		return true
	}
	return false
}

// startDownloads claims episodes and downloads them in the background.
// Episodes already downloading or downloaded are skipped.
func (s *Service) startDownloads(feed gen.PodcastFeed, eps []gen.PodcastFeedEpisode) {
	var claimed []gen.PodcastFeedEpisode
	for _, ep := range eps {
		if ep.Status != "available" && ep.Status != "failed" {
			continue
		}
		c, err := s.claim(s.bg, feed, ep)
		if err != nil {
			if !errors.Is(err, ErrNotDownloadable) {
				s.logger.Warn("claim podcast download", "episode_id", ep.ID, "err", err)
			}
			continue
		}
		claimed = append(claimed, c)
	}
	if len(claimed) == 0 {
		return
	}
	go func() {
		for _, ep := range claimed {
			s.runDownload(feed, ep)
		}
	}()
}

// runDownload downloads a claimed episode, then rescans the feed's folder.
// It waits for a free download slot first.
func (s *Service) runDownload(feed gen.PodcastFeed, ep gen.PodcastFeedEpisode) {
	select {
	case s.downloads <- struct{}{}:
	case <-s.bg.Done():
		return
	}
	defer func() { <-s.downloads }()

	ctx, cancel := context.WithTimeout(s.bg, downloadTimeout)
	defer cancel()
	start := time.Now()
	n, err := s.download(ctx, ep)
	if err != nil {
		msg := err.Error()
		if ferr := s.q.FailPodcastEpisodeDownload(context.WithoutCancel(ctx), gen.FailPodcastEpisodeDownloadParams{
			ID: ep.ID, Error: &msg,
		}); ferr != nil {
			s.logger.Error("record podcast download failure", "episode_id", ep.ID, "err", ferr)
		}
		s.logger.Warn("podcast download failed",
			"feed_id", feed.ID, "episode_id", ep.ID, "title", ep.Title, "err", err)
		return
	}
	if err := s.q.FinishPodcastEpisodeDownload(context.WithoutCancel(ctx), ep.ID); err != nil {
		s.logger.Error("record podcast download", "episode_id", ep.ID, "err", err)
		return
	}
	s.logger.Info("podcast episode downloaded",
		"feed_id", feed.ID, "episode_id", ep.ID, "title", ep.Title,
		"bytes", n, "duration_ms", time.Since(start).Milliseconds())
	s.rescan(feed)
}

// errTooLarge stops a download past maxEpisodeBytes.
var errTooLarge = fmt.Errorf("the episode is larger than %d GB", maxEpisodeBytes>>30)

func (s *Service) download(ctx context.Context, ep gen.PodcastFeedEpisode) (int64, error) {
	if ep.FilePath == nil {
		return 0, errors.New("no file path claimed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.EnclosureUrl, nil)
	if err != nil {
		return 0, fmt.Errorf("bad episode URL: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.downloadClient.Do(req)
	if err != nil {
		return 0, fetchErr("download", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the episode URL answered HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxEpisodeBytes {
		return 0, errTooLarge
	}
	body := &capReader{r: resp.Body, left: maxEpisodeBytes}
	if err := s.store.PutStream(ctx, *ep.FilePath, body, resp.ContentLength); err != nil {
		if errors.Is(err, errTooLarge) {
			return body.n, errTooLarge
		}
		return body.n, fmt.Errorf("save episode: %w", err)
	}
	if body.n == 0 {
		_ = s.store.Delete(context.WithoutCancel(ctx), *ep.FilePath)
		return 0, errors.New("the episode URL returned an empty file")
	}
	return body.n, nil
}

// capReader counts bytes and fails past left.
type capReader struct {
	r    io.Reader
	left int64
	n    int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.left -= int64(n)
	if c.left < 0 {
		return n, errTooLarge
	}
	return n, err
}

// applyRetention deletes the downloads past the feed's keep_latest newest
// episodes (by publish date). An older episode downloaded by hand goes at the
// next refresh too: the setting keeps the newest N, whatever fetched them.
func (s *Service) applyRetention(ctx context.Context, feed gen.PodcastFeed) (int, error) {
	if feed.KeepLatest <= 0 {
		return 0, nil
	}
	eps, err := s.q.ListDownloadedPodcastEpisodes(ctx, feed.ID)
	if err != nil {
		return 0, fmt.Errorf("list downloaded episodes: %w", err)
	}
	if len(eps) <= int(feed.KeepLatest) {
		return 0, nil
	}
	removed := 0
	for _, ep := range eps[feed.KeepLatest:] {
		if err := s.removeFile(ctx, ep); err != nil {
			s.logger.WarnContext(ctx, "podcast retention: delete failed", "episode_id", ep.ID, "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.logger.InfoContext(ctx, "podcast retention removed episodes", "feed_id", feed.ID, "count", removed)
		s.tidyLibrary(ctx, feed.LibraryID)
		s.rescan(feed)
	}
	return removed, nil
}

// removeFile deletes an episode's file, takes it out of the library and
// marks the episode removed.
func (s *Service) removeFile(ctx context.Context, ep gen.PodcastFeedEpisode) error {
	if ep.FilePath != nil {
		if err := s.store.Delete(ctx, *ep.FilePath); err != nil {
			return fmt.Errorf("delete episode file: %w", err)
		}
		s.retireFile(ctx, *ep.FilePath)
	}
	if err := s.q.MarkPodcastEpisodeRemoved(ctx, ep.ID); err != nil {
		return fmt.Errorf("mark episode removed: %w", err)
	}
	return nil
}

// retireFile drops the library record of a deleted file and hides its item
// once it has no files left; tidyLibrary then hides an emptied show.
// Best effort: what it misses, the next full scan finds.
func (s *Service) retireFile(ctx context.Context, path string) {
	if s.files == nil {
		return
	}
	f, err := s.files.GetFileByPath(ctx, path)
	if err != nil {
		if !errors.Is(err, media.ErrNotFound) {
			s.logger.WarnContext(ctx, "podcast: look up deleted file", "path", path, "err", err)
		}
		return
	}
	if err := s.files.DeleteFile(ctx, f.ID); err != nil {
		s.logger.WarnContext(ctx, "podcast: drop deleted file", "file_id", f.ID, "err", err)
		return
	}
	if err := s.files.SoftDeleteItemIfEmpty(ctx, f.MediaItemID); err != nil {
		s.logger.WarnContext(ctx, "podcast: hide emptied episode", "item_id", f.MediaItemID, "err", err)
	}
}

// tidyLibrary hides episodes and shows left with no files once episodes are
// deleted. The folder rescan can't be relied on for it: a scan that finds no
// media stops before its cleanup (an empty walk may be an unmounted share),
// and a folder whose last episode went holds only its poster. Best effort.
func (s *Service) tidyLibrary(ctx context.Context, libraryID uuid.UUID) {
	if s.files == nil {
		return
	}
	if err := s.files.CleanupEmptyItems(ctx, libraryID); err != nil {
		s.logger.WarnContext(ctx, "podcast: hide emptied shows", "library_id", libraryID, "err", err)
	}
}

func (s *Service) rescan(feed gen.PodcastFeed) {
	if s.scans == nil {
		return
	}
	if err := s.scans.TriggerDirectoryScan(s.bg, feed.LibraryID, feed.Folder); err != nil {
		s.logger.Warn("podcast folder rescan", "feed_id", feed.ID, "err", err)
	}
}

// savePoster writes the feed's artwork to <folder>/poster.jpg. Best effort:
// a show without a poster still plays.
func (s *Service) savePoster(ctx context.Context, folder, imageURL string, replace bool) {
	if s.poster == nil || imageURL == "" {
		return
	}
	var err error
	if replace {
		_, err = s.poster.ReplaceShowPoster(ctx, uuid.Nil, imageURL, folder)
	} else {
		_, err = s.poster.DownloadPoster(ctx, uuid.Nil, imageURL, folder)
	}
	if err != nil {
		s.logger.WarnContext(ctx, "podcast artwork download failed", "folder", folder, "err", err)
	}
}

// ── Feed fetch ────────────────────────────────────────────────────────────

type fetchResult struct {
	feed         *Feed
	etag         string
	lastModified string
	notModified  bool
}

func (s *Service) fetch(ctx context.Context, feedURL string, etag, lastModified *string) (fetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return fetchResult{}, ErrInvalidFeedURL
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.1")
	if etag != nil && *etag != "" {
		req.Header.Set("If-None-Match", *etag)
	}
	if lastModified != nil && *lastModified != "" {
		req.Header.Set("If-Modified-Since", *lastModified)
	}
	resp, err := s.feedClient.Do(req)
	if err != nil {
		return fetchResult{}, fetchErr("fetch the feed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return fetchResult{notModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return fetchResult{}, &FetchError{Msg: fmt.Sprintf("the feed URL answered HTTP %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return fetchResult{}, fetchErr("read the feed", err)
	}
	if len(data) > maxFeedBytes {
		return fetchResult{}, &FetchError{Msg: "the feed is larger than 32 MB"}
	}
	feed, err := Parse(bytes.NewReader(data), resp.Request.URL)
	if err != nil {
		return fetchResult{}, &FetchError{Msg: "the URL isn't an RSS podcast feed"}
	}
	return fetchResult{
		feed:         feed,
		etag:         resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// fetchErr words a transport error for an admin, naming a blocked address
// plainly instead of as a dial error.
func fetchErr(what string, err error) error {
	if errors.Is(err, safehttp.ErrBlockedAddress) {
		return &FetchError{Msg: "couldn't " + what + ": the address is on a private or local network, which OnScreen doesn't fetch from"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &FetchError{Msg: "couldn't " + what + ": timed out"}
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return &FetchError{Msg: "couldn't " + what + ": " + err.Error()}
}

// normalizeFeedURL accepts http(s) URLs, and the feed:// and pcast://
// schemes podcast directories link with (as https).
func normalizeFeedURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	for _, scheme := range []string{"feed://", "pcast://", "podcast://"} {
		if len(raw) > len(scheme) && strings.EqualFold(raw[:len(scheme)], scheme) {
			raw = "https://" + raw[len(scheme):]
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", ErrInvalidFeedURL
	}
	u.Fragment = ""
	return u.String(), nil
}

// ── Helpers ───────────────────────────────────────────────────────────────

// newestFirst orders episodes by publish date, newest first, undated last.
func newestFirst(eps []gen.PodcastFeedEpisode) []gen.PodcastFeedEpisode {
	out := append([]gen.PodcastFeedEpisode(nil), eps...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && newer(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func newer(a, b gen.PodcastFeedEpisode) bool {
	if a.PublishedAt.Valid != b.PublishedAt.Valid {
		return a.PublishedAt.Valid
	}
	return a.PublishedAt.Time.After(b.PublishedAt.Time)
}

func feedFromRow(r gen.ListPodcastFeedsRow) gen.PodcastFeed {
	return gen.PodcastFeed{
		ID: r.ID, LibraryID: r.LibraryID, FeedUrl: r.FeedUrl, Title: r.Title,
		Author: r.Author, Description: r.Description, ImageUrl: r.ImageUrl,
		SiteUrl: r.SiteUrl, Language: r.Language, Folder: r.Folder,
		AutoDownload: r.AutoDownload, KeepLatest: r.KeepLatest, Etag: r.Etag,
		LastModified: r.LastModified, LastCheckedAt: r.LastCheckedAt,
		LastError: r.LastError, ShowItemID: r.ShowItemID, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func nonEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
