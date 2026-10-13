package podcast

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// ── Fakes ─────────────────────────────────────────────────────────────────

// fakeDB keeps feeds and episodes in memory with the semantics of the SQL in
// internal/db/queries/podcasts.sql.
type fakeDB struct {
	mu       sync.Mutex
	feeds    map[uuid.UUID]*gen.PodcastFeed
	episodes map[uuid.UUID]*gen.PodcastFeedEpisode
	seq      int // orders downloaded_at deterministically
}

func newFakeDB() *fakeDB {
	return &fakeDB{feeds: map[uuid.UUID]*gen.PodcastFeed{}, episodes: map[uuid.UUID]*gen.PodcastFeedEpisode{}}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (f *fakeDB) CreatePodcastFeed(_ context.Context, a gen.CreatePodcastFeedParams) (gen.PodcastFeed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.feeds {
		if x.LibraryID == a.LibraryID && x.FeedUrl == a.FeedUrl {
			return gen.PodcastFeed{}, &pgconn.PgError{Code: "23505"}
		}
	}
	row := gen.PodcastFeed{
		ID: uuid.New(), LibraryID: a.LibraryID, FeedUrl: a.FeedUrl, Title: a.Title,
		Author: a.Author, Description: a.Description, ImageUrl: a.ImageUrl, SiteUrl: a.SiteUrl,
		Language: a.Language, Folder: a.Folder, AutoDownload: a.AutoDownload, KeepLatest: a.KeepLatest,
		Etag: a.Etag, LastModified: a.LastModified, LastCheckedAt: ts(time.Now()),
		CreatedBy: a.CreatedBy, CreatedAt: ts(time.Now()), UpdatedAt: ts(time.Now()),
	}
	f.feeds[row.ID] = &row
	return row, nil
}

func (f *fakeDB) GetPodcastFeed(_ context.Context, id uuid.UUID) (gen.PodcastFeed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if x, ok := f.feeds[id]; ok {
		return *x, nil
	}
	return gen.PodcastFeed{}, pgx.ErrNoRows
}

func (f *fakeDB) ListPodcastFeeds(_ context.Context, lib pgtype.UUID) ([]gen.ListPodcastFeedsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.ListPodcastFeedsRow
	for _, x := range f.feeds {
		if lib.Valid && x.LibraryID != uuid.UUID(lib.Bytes) {
			continue
		}
		var total, downloaded int64
		for _, e := range f.episodes {
			if e.FeedID == x.ID {
				total++
				if e.Status == "downloaded" {
					downloaded++
				}
			}
		}
		out = append(out, gen.ListPodcastFeedsRow{
			ID: x.ID, LibraryID: x.LibraryID, FeedUrl: x.FeedUrl, Title: x.Title, Folder: x.Folder,
			AutoDownload: x.AutoDownload, KeepLatest: x.KeepLatest, Etag: x.Etag,
			LastModified: x.LastModified, ImageUrl: x.ImageUrl, ShowItemID: x.ShowItemID,
			EpisodeCount: total, DownloadedCount: downloaded,
		})
	}
	return out, nil
}

func (f *fakeDB) UpdatePodcastFeedSettings(_ context.Context, a gen.UpdatePodcastFeedSettingsParams) (gen.PodcastFeed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.feeds[a.ID]
	if !ok {
		return gen.PodcastFeed{}, pgx.ErrNoRows
	}
	x.AutoDownload, x.KeepLatest = a.AutoDownload, a.KeepLatest
	return *x, nil
}

func (f *fakeDB) UpdatePodcastFeedFromRefresh(_ context.Context, a gen.UpdatePodcastFeedFromRefreshParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.feeds[a.ID]
	x.Title, x.Author, x.Description, x.ImageUrl = a.Title, a.Author, a.Description, a.ImageUrl
	x.SiteUrl, x.Language, x.Etag, x.LastModified = a.SiteUrl, a.Language, a.Etag, a.LastModified
	x.LastError = nil
	return nil
}

func (f *fakeDB) SetPodcastFeedChecked(_ context.Context, a gen.SetPodcastFeedCheckedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[a.ID].LastError = a.LastError
	return nil
}

func (f *fakeDB) SetPodcastFeedShowItem(_ context.Context, a gen.SetPodcastFeedShowItemParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[a.ID].ShowItemID = a.ShowItemID
	return nil
}

func (f *fakeDB) DeletePodcastFeed(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.feeds, id)
	for eid, e := range f.episodes {
		if e.FeedID == id {
			delete(f.episodes, eid)
		}
	}
	return nil
}

func (f *fakeDB) GetPodcastFeedByFolder(_ context.Context, folder string) (gen.GetPodcastFeedByFolderRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.feeds {
		if x.Folder == folder {
			return gen.GetPodcastFeedByFolderRow{ID: x.ID, Title: x.Title, Description: x.Description, ShowItemID: x.ShowItemID}, nil
		}
	}
	return gen.GetPodcastFeedByFolderRow{}, pgx.ErrNoRows
}

func (f *fakeDB) ListPodcastEpisodeGUIDs(_ context.Context, feedID uuid.UUID) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.episodes {
		if e.FeedID == feedID {
			out = append(out, e.Guid)
		}
	}
	return out, nil
}

func (f *fakeDB) UpsertPodcastEpisode(_ context.Context, a gen.UpsertPodcastEpisodeParams) (gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.episodes {
		if e.FeedID == a.FeedID && e.Guid == a.Guid {
			e.Title, e.Description, e.PublishedAt, e.EnclosureUrl = a.Title, a.Description, a.PublishedAt, a.EnclosureUrl
			return *e, nil
		}
	}
	row := gen.PodcastFeedEpisode{
		ID: uuid.New(), FeedID: a.FeedID, Guid: a.Guid, Title: a.Title, Description: a.Description,
		PublishedAt: a.PublishedAt, DurationMs: a.DurationMs, EnclosureUrl: a.EnclosureUrl,
		EnclosureType: a.EnclosureType, EnclosureLength: a.EnclosureLength, Season: a.Season,
		Episode: a.Episode, ImageUrl: a.ImageUrl, Status: "available", CreatedAt: ts(time.Now()),
	}
	f.episodes[row.ID] = &row
	return row, nil
}

func (f *fakeDB) GetPodcastEpisode(_ context.Context, id uuid.UUID) (gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.episodes[id]; ok {
		return *e, nil
	}
	return gen.PodcastFeedEpisode{}, pgx.ErrNoRows
}

// sorted returns a feed's episodes newest first (published, then created).
func (f *fakeDB) sorted(feedID uuid.UUID, keep func(*gen.PodcastFeedEpisode) bool) []gen.PodcastFeedEpisode {
	var out []gen.PodcastFeedEpisode
	for _, e := range f.episodes {
		if e.FeedID == feedID && (keep == nil || keep(e)) {
			out = append(out, *e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j]) })
	return out
}

func (f *fakeDB) ListPodcastEpisodes(_ context.Context, a gen.ListPodcastEpisodesParams) ([]gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.sorted(a.FeedID, nil)
	lo := min(int(a.Offset), len(all))
	hi := min(lo+int(a.Limit), len(all))
	return all[lo:hi], nil
}

func (f *fakeDB) CountPodcastEpisodes(_ context.Context, feedID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.sorted(feedID, nil))), nil
}

func (f *fakeDB) ListLatestPodcastEpisodes(_ context.Context, a gen.ListLatestPodcastEpisodesParams) ([]gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.sorted(a.FeedID, nil)
	return all[:min(int(a.Limit), len(all))], nil
}

func (f *fakeDB) ListDownloadedPodcastEpisodes(_ context.Context, feedID uuid.UUID) ([]gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sorted(feedID, func(e *gen.PodcastFeedEpisode) bool { return e.Status == "downloaded" }), nil
}

func (f *fakeDB) ClaimPodcastEpisodeDownload(_ context.Context, a gen.ClaimPodcastEpisodeDownloadParams) (gen.PodcastFeedEpisode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.episodes[a.ID]
	if !ok || (e.Status != "available" && e.Status != "failed" && e.Status != "removed") {
		return gen.PodcastFeedEpisode{}, pgx.ErrNoRows
	}
	e.Status, e.FilePath, e.Error = "downloading", a.FilePath, nil
	return *e, nil
}

func (f *fakeDB) FinishPodcastEpisodeDownload(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	e := f.episodes[id]
	e.Status = "downloaded"
	e.DownloadedAt = ts(time.Unix(int64(f.seq), 0))
	return nil
}

func (f *fakeDB) FailPodcastEpisodeDownload(_ context.Context, a gen.FailPodcastEpisodeDownloadParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.episodes[a.ID]
	e.Status, e.FilePath, e.Error = "failed", nil, a.Error
	return nil
}

func (f *fakeDB) MarkPodcastEpisodeRemoved(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.episodes[id]
	e.Status, e.FilePath, e.MediaItemID = "removed", nil, pgtype.UUID{}
	return nil
}

func (f *fakeDB) ResetStalePodcastDownloads(context.Context, pgtype.Timestamptz) (int64, error) {
	return 0, nil
}

func (f *fakeDB) GetPodcastEpisodeByFilePath(_ context.Context, path *string) (gen.GetPodcastEpisodeByFilePathRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.episodes {
		if e.FilePath != nil && *e.FilePath == *path {
			fd := f.feeds[e.FeedID]
			return gen.GetPodcastEpisodeByFilePathRow{
				ID: e.ID, FeedID: e.FeedID, Title: e.Title, Description: e.Description,
				PublishedAt: e.PublishedAt, DurationMs: e.DurationMs, Season: e.Season, Episode: e.Episode,
				MediaItemID: e.MediaItemID, LibraryID: fd.LibraryID, FeedTitle: fd.Title,
				FeedDescription: fd.Description, FeedFolder: fd.Folder, ShowItemID: fd.ShowItemID,
			}, nil
		}
	}
	return gen.GetPodcastEpisodeByFilePathRow{}, pgx.ErrNoRows
}

func (f *fakeDB) LinkPodcastEpisodeItem(_ context.Context, a gen.LinkPodcastEpisodeItemParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.episodes[a.ID].MediaItemID = a.MediaItemID
	return nil
}

func (f *fakeDB) episode(t *testing.T, guid string) gen.PodcastFeedEpisode {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.episodes {
		if e.Guid == guid {
			return *e
		}
	}
	t.Fatalf("no episode %q", guid)
	return gen.PodcastFeedEpisode{}
}

type fakeLibs map[uuid.UUID]*library.Library

func (l fakeLibs) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	if lib, ok := l[id]; ok {
		return lib, nil
	}
	return nil, library.ErrNotFound
}

type fakeScans struct {
	mu   sync.Mutex
	dirs []string
}

func (s *fakeScans) TriggerDirectoryScan(_ context.Context, _ uuid.UUID, dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs = append(s.dirs, dir)
	return nil
}

func (s *fakeScans) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.dirs)
}

// fakeFiles is the library's file records: path -> file.
type fakeFiles struct {
	mu      sync.Mutex
	byPath  map[string]*media.File
	deleted []uuid.UUID
	hidden  []uuid.UUID
	cleaned []uuid.UUID // libraries CleanupEmptyItems ran on
}

func (f *fakeFiles) GetFileByPath(_ context.Context, path string) (*media.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if file, ok := f.byPath[path]; ok {
		return file, nil
	}
	return nil, media.ErrNotFound
}

func (f *fakeFiles) DeleteFile(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeFiles) SoftDeleteItemIfEmpty(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hidden = append(f.hidden, id)
	return nil
}

func (f *fakeFiles) CleanupEmptyItems(_ context.Context, libraryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleaned = append(f.cleaned, libraryID)
	return nil
}

type fakePoster struct {
	mu       sync.Mutex
	fetched  []string
	replaced []string
}

func (p *fakePoster) DownloadPoster(_ context.Context, _ uuid.UUID, url, dir string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetched = append(p.fetched, url)
	return filepath.Join(dir, "poster.jpg"), nil
}

func (p *fakePoster) ReplaceShowPoster(_ context.Context, _ uuid.UUID, url, dir string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replaced = append(p.replaced, url)
	return filepath.Join(dir, "poster.jpg"), nil
}

// feedServer serves a feed at /feed.xml and episodes at /audio/<name>.
type feedServer struct {
	*httptest.Server
	mu       sync.Mutex
	items    []string // <item> XML, newest first
	etag     string
	feedHits int
	fail     map[string]int // audio path → status to answer with
}

func newFeedServer(t *testing.T) *feedServer {
	fs := &feedServer{fail: map[string]int{}}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		switch {
		case r.URL.Path == "/feed.xml":
			fs.feedHits++
			if fs.etag != "" && r.Header.Get("If-None-Match") == fs.etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			if fs.etag != "" {
				w.Header().Set("ETag", fs.etag)
			}
			fmt.Fprintf(w, `<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
<title>Test: Show</title><description>About the show</description>
<itunes:image href="%s/art.jpg"/>%s</channel></rss>`, fs.URL, strings.Join(fs.items, "\n"))
		case strings.HasPrefix(r.URL.Path, "/audio/"):
			if code := fs.fail[r.URL.Path]; code != 0 {
				w.WriteHeader(code)
				return
			}
			io.WriteString(w, "audio:"+strings.TrimPrefix(r.URL.Path, "/audio/"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fs.Close)
	return fs
}

// addItem prepends an episode published day days into October 2026.
func (fs *feedServer) addItem(guid, title string, day int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	pub := time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC).Format(time.RFC1123Z)
	item := fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><pubDate>%s</pubDate>
<enclosure url="%s/audio/%s.mp3" type="audio/mpeg"/></item>`, title, guid, pub, fs.URL, guid)
	fs.items = append([]string{item}, fs.items...)
}

type harness struct {
	svc    *Service
	db     *fakeDB
	scans  *fakeScans
	poster *fakePoster
	files  *fakeFiles
	fs     *feedServer
	lib    *library.Library
	root   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	lib := &library.Library{ID: uuid.New(), Type: "podcast", Paths: []string{root}}
	h := &harness{
		db:     newFakeDB(),
		scans:  &fakeScans{},
		poster: &fakePoster{},
		files:  &fakeFiles{byPath: map[string]*media.File{}},
		fs:     newFeedServer(t),
		lib:    lib,
		root:   root,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.svc = New(t.Context(), h.db, fakeLibs{lib.ID: lib}, mediastore.Local{}, h.scans, h.poster, logger).
		WithHTTPClient(h.fs.Client()).
		WithLibraryFiles(h.files)
	return h
}

func (h *harness) subscribe(t *testing.T, p SubscribeParams) gen.PodcastFeed {
	t.Helper()
	if p.LibraryID == uuid.Nil {
		p.LibraryID = h.lib.ID
	}
	if p.FeedURL == "" {
		p.FeedURL = h.fs.URL + "/feed.xml"
	}
	feed, err := h.svc.Subscribe(context.Background(), p)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return feed
}

// waitStatus polls until the episode reaches status (downloads run in the
// background).
func (h *harness) waitStatus(t *testing.T, guid, status string) gen.PodcastFeedEpisode {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ep := h.db.episode(t, guid)
		if ep.Status == status {
			return ep
		}
		if time.Now().After(deadline) {
			t.Fatalf("episode %s status = %q (error %v), want %q", guid, ep.Status, deref(ep.Error), status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────

func TestSubscribe_DownloadsNewestIntoFeedFolder(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "First: Hello", 1)
	h.fs.addItem("ep2", "Second", 8)

	feed := h.subscribe(t, SubscribeParams{AutoDownload: true, DownloadLatest: 1})

	wantFolder := filepath.Join(h.root, "Test - Show")
	if feed.Folder != wantFolder || feed.Title != "Test: Show" {
		t.Errorf("folder = %q, title = %q; want %q, Test: Show", feed.Folder, feed.Title, wantFolder)
	}
	ep := h.waitStatus(t, "ep2", "downloaded")
	wantPath := filepath.Join(wantFolder, "2026-10-08 Second.mp3")
	if ep.FilePath == nil || *ep.FilePath != wantPath {
		t.Fatalf("file_path = %v, want %s", ep.FilePath, wantPath)
	}
	if b, err := os.ReadFile(wantPath); err != nil || string(b) != "audio:ep2.mp3" {
		t.Errorf("file = %q, %v", b, err)
	}
	if got := h.db.episode(t, "ep1").Status; got != "available" {
		t.Errorf("older episode status = %q, want available (only the newest downloads)", got)
	}
	if len(h.poster.fetched) != 1 || h.poster.fetched[0] != h.fs.URL+"/art.jpg" {
		t.Errorf("poster fetches = %v", h.poster.fetched)
	}
	waitFor(t, func() bool { return h.scans.count() == 1 })
}

func TestSubscribe_Refusals(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	movies := &library.Library{ID: uuid.New(), Type: "movie", Paths: []string{h.root}}
	h.svc.libs = fakeLibs{h.lib.ID: h.lib, movies.ID: movies}

	for name, c := range map[string]struct {
		p    SubscribeParams
		want error
	}{
		"movie library": {SubscribeParams{LibraryID: movies.ID, FeedURL: h.fs.URL + "/feed.xml"}, ErrNotPodcastLibrary},
		"bad url":       {SubscribeParams{LibraryID: h.lib.ID, FeedURL: "ftp://x/feed"}, ErrInvalidFeedURL},
		"keep_latest":   {SubscribeParams{LibraryID: h.lib.ID, FeedURL: h.fs.URL + "/feed.xml", KeepLatest: -1}, ErrInvalidKeepLatest},
		"no library":    {SubscribeParams{LibraryID: uuid.New(), FeedURL: h.fs.URL + "/feed.xml"}, library.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.svc.Subscribe(context.Background(), c.p); err != c.want {
				t.Errorf("Subscribe = %v, want %v", err, c.want)
			}
		})
	}

	t.Run("not found upstream", func(t *testing.T) {
		_, err := h.svc.Subscribe(context.Background(), SubscribeParams{LibraryID: h.lib.ID, FeedURL: h.fs.URL + "/missing.xml"})
		fe, ok := err.(*FetchError)
		if !ok || !strings.Contains(fe.Msg, "HTTP 404") {
			t.Errorf("Subscribe = %v, want a FetchError naming HTTP 404", err)
		}
	})

	t.Run("twice", func(t *testing.T) {
		h.subscribe(t, SubscribeParams{})
		if _, err := h.svc.Subscribe(context.Background(), SubscribeParams{LibraryID: h.lib.ID, FeedURL: h.fs.URL + "/feed.xml"}); err != ErrAlreadySubscribed {
			t.Errorf("second Subscribe = %v, want ErrAlreadySubscribed", err)
		}
	})
}

func TestSubscribe_NoAudioEpisodes(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Subscribe(context.Background(), SubscribeParams{LibraryID: h.lib.ID, FeedURL: h.fs.URL + "/feed.xml"}); err != ErrNoAudioEpisodes {
		t.Errorf("Subscribe of an empty feed = %v, want ErrNoAudioEpisodes", err)
	}
}

// Two feeds with the same title get different folders.
func TestSubscribe_FolderCollision(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	a := h.subscribe(t, SubscribeParams{})
	b := h.subscribe(t, SubscribeParams{FeedURL: h.fs.URL + "/feed.xml?copy=1"})
	if a.Folder == b.Folder {
		t.Fatalf("both feeds use folder %q", a.Folder)
	}
	if !strings.HasPrefix(filepath.Base(b.Folder), "Test - Show (") {
		t.Errorf("second folder = %q", b.Folder)
	}

	// Another podcast library on the same folder can't reuse it either.
	other := &library.Library{ID: uuid.New(), Type: "podcast", Paths: []string{h.root}}
	h.svc.libs = fakeLibs{h.lib.ID: h.lib, other.ID: other}
	c := h.subscribe(t, SubscribeParams{LibraryID: other.ID})
	if c.Folder == a.Folder || c.Folder == b.Folder {
		t.Errorf("a second library's subscription reuses folder %q", c.Folder)
	}
}

func TestRefresh_AutoDownloadsNewEpisodes(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	feed := h.subscribe(t, SubscribeParams{AutoDownload: true, DownloadLatest: 0})

	h.fs.addItem("ep2", "Two", 2)
	res, err := h.svc.Refresh(context.Background(), feed.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.NewEpisodes != 1 || res.Downloading != 1 {
		t.Errorf("result = %+v, want 1 new, 1 downloading", res)
	}
	h.waitStatus(t, "ep2", "downloaded")
	if got := h.db.episode(t, "ep1").Status; got != "available" {
		t.Errorf("episode listed at subscribe = %q, want available (auto-download is for new ones)", got)
	}
}

func TestRefresh_AutoDownloadOff(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	feed := h.subscribe(t, SubscribeParams{AutoDownload: false})
	h.fs.addItem("ep2", "Two", 2)
	res, err := h.svc.Refresh(context.Background(), feed.ID)
	if err != nil || res.NewEpisodes != 1 || res.Downloading != 0 {
		t.Fatalf("Refresh = %+v, %v; want 1 new, 0 downloading", res, err)
	}
	if got := h.db.episode(t, "ep2").Status; got != "available" {
		t.Errorf("status = %q, want available", got)
	}
}

func TestRefresh_NotModified(t *testing.T) {
	h := newHarness(t)
	h.fs.etag = `"v1"`
	h.fs.addItem("ep1", "One", 1)
	feed := h.subscribe(t, SubscribeParams{})
	if feed.Etag == nil || *feed.Etag != `"v1"` {
		t.Fatalf("etag stored = %v", feed.Etag)
	}
	res, err := h.svc.Refresh(context.Background(), feed.ID)
	if err != nil || !res.NotModified {
		t.Errorf("Refresh = %+v, %v; want NotModified", res, err)
	}
}

func TestRefresh_RecordsFailure(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	feed := h.subscribe(t, SubscribeParams{})
	h.db.mu.Lock()
	h.db.feeds[feed.ID].FeedUrl = h.fs.URL + "/gone.xml"
	h.db.mu.Unlock()
	if _, err := h.svc.Refresh(context.Background(), feed.ID); err == nil {
		t.Fatal("Refresh of a 404 feed succeeded")
	}
	got, _ := h.db.GetPodcastFeed(context.Background(), feed.ID)
	if got.LastError == nil || !strings.Contains(*got.LastError, "404") {
		t.Errorf("last_error = %v", got.LastError)
	}
}

// keep_latest keeps the newest episodes' files and deletes the rest — even
// when the newest downloaded first, as a batch does.
func TestRetention_KeepsLatestDownloads(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	h.fs.addItem("ep2", "Two", 2)
	h.fs.addItem("ep3", "Three", 3)
	feed := h.subscribe(t, SubscribeParams{DownloadLatest: 3})
	for _, g := range []string{"ep1", "ep2", "ep3"} {
		h.waitStatus(t, g, "downloaded")
	}
	oldest := h.db.episode(t, "ep1")
	if _, err := h.svc.UpdateSettings(context.Background(), feed.ID, true, 2); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	gone := h.db.episode(t, "ep1")
	if gone.Status != "removed" || gone.FilePath != nil {
		t.Errorf("ep1 = %q %v, want removed with no path", gone.Status, gone.FilePath)
	}
	if _, err := os.Stat(*oldest.FilePath); !os.IsNotExist(err) {
		t.Errorf("ep1 file still on disk: %v", err)
	}
	for _, g := range []string{"ep2", "ep3"} {
		if st := h.db.episode(t, g).Status; st != "downloaded" {
			t.Errorf("%s = %q, want downloaded", g, st)
		}
	}
	if len(h.files.cleaned) != 1 {
		t.Errorf("CleanupEmptyItems ran %d times after retention, want 1", len(h.files.cleaned))
	}

	// A refresh doesn't fetch a removed episode again.
	if _, err := h.svc.Refresh(context.Background(), feed.ID); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if st := h.db.episode(t, "ep1").Status; st != "removed" {
		t.Errorf("after refresh ep1 = %q, want removed", st)
	}
}

func TestDownload_FailureIsRecorded(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	h.fs.fail["/audio/ep1.mp3"] = http.StatusInternalServerError
	h.subscribe(t, SubscribeParams{DownloadLatest: 1})
	ep := h.waitStatus(t, "ep1", "failed")
	if ep.Error == nil || !strings.Contains(*ep.Error, "HTTP 500") || ep.FilePath != nil {
		t.Errorf("failed episode = error %v, path %v", ep.Error, ep.FilePath)
	}
	entries, _ := os.ReadDir(filepath.Join(h.root, "Test - Show"))
	for _, e := range entries {
		if e.Name() != "poster.jpg" {
			t.Errorf("leftover file after a failed download: %s", e.Name())
		}
	}

	// A retry succeeds once the server does.
	delete(h.fs.fail, "/audio/ep1.mp3")
	if _, err := h.svc.Download(context.Background(), ep.ID); err != nil {
		t.Fatalf("retry Download: %v", err)
	}
	h.waitStatus(t, "ep1", "downloaded")
}

func TestDownload_RefusesTwice(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	h.subscribe(t, SubscribeParams{DownloadLatest: 1})
	ep := h.waitStatus(t, "ep1", "downloaded")
	if _, err := h.svc.Download(context.Background(), ep.ID); err != ErrNotDownloadable {
		t.Errorf("Download of a downloaded episode = %v, want ErrNotDownloadable", err)
	}
	if _, err := h.svc.Download(context.Background(), uuid.New()); err != ErrEpisodeNotFound {
		t.Errorf("Download of an unknown episode = %v, want ErrEpisodeNotFound", err)
	}
}

// Two episodes with the same title and date get different files.
func TestDownload_NameCollision(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("a", "Trailer", 1)
	h.fs.addItem("b", "Trailer", 1)
	h.subscribe(t, SubscribeParams{DownloadLatest: 2})
	a := h.waitStatus(t, "a", "downloaded")
	b := h.waitStatus(t, "b", "downloaded")
	if *a.FilePath == *b.FilePath {
		t.Fatalf("both episodes saved to %s", *a.FilePath)
	}
}

func TestDeleteDownload(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One", 1)
	h.subscribe(t, SubscribeParams{DownloadLatest: 1})
	ep := h.waitStatus(t, "ep1", "downloaded")
	// The scanner has made a library record of the file.
	file := &media.File{ID: uuid.New(), MediaItemID: uuid.New(), FilePath: *ep.FilePath}
	h.files.byPath[*ep.FilePath] = file
	if err := h.svc.DeleteDownload(context.Background(), ep.ID); err != nil {
		t.Fatalf("DeleteDownload: %v", err)
	}
	if _, err := os.Stat(*ep.FilePath); !os.IsNotExist(err) {
		t.Errorf("file still there: %v", err)
	}
	if st := h.db.episode(t, "ep1").Status; st != "removed" {
		t.Errorf("status = %q, want removed", st)
	}
	// It leaves the library at once, not at the next full scan.
	if len(h.files.deleted) != 1 || h.files.deleted[0] != file.ID || len(h.files.hidden) != 1 || h.files.hidden[0] != file.MediaItemID {
		t.Errorf("library record: deleted %v, hidden %v", h.files.deleted, h.files.hidden)
	}
	// An emptied show is hidden by the service: the folder rescan finds no
	// media and stops before the scanner's own cleanup.
	if len(h.files.cleaned) != 1 || h.files.cleaned[0] != h.lib.ID {
		t.Errorf("CleanupEmptyItems ran on %v, want once on the library", h.files.cleaned)
	}
	if err := h.svc.DeleteDownload(context.Background(), ep.ID); err != ErrNotDownloaded {
		t.Errorf("second DeleteDownload = %v, want ErrNotDownloaded", err)
	}
}

func TestUnsubscribe(t *testing.T) {
	for _, deleteFiles := range []bool{false, true} {
		t.Run(fmt.Sprint("delete_files=", deleteFiles), func(t *testing.T) {
			h := newHarness(t)
			h.fs.addItem("ep1", "One", 1)
			feed := h.subscribe(t, SubscribeParams{DownloadLatest: 1})
			ep := h.waitStatus(t, "ep1", "downloaded")
			if err := h.svc.Unsubscribe(context.Background(), feed.ID, deleteFiles); err != nil {
				t.Fatalf("Unsubscribe: %v", err)
			}
			if _, err := h.svc.Feed(context.Background(), feed.ID); err != ErrFeedNotFound {
				t.Errorf("feed after unsubscribe: %v", err)
			}
			_, statErr := os.Stat(*ep.FilePath)
			if deleteFiles != os.IsNotExist(statErr) {
				t.Errorf("file exists = %v after unsubscribe with delete_files=%v", statErr == nil, deleteFiles)
			}
			if cleaned := len(h.files.cleaned) > 0; cleaned != deleteFiles {
				t.Errorf("CleanupEmptyItems ran = %v with delete_files=%v", cleaned, deleteFiles)
			}
		})
	}
}

func TestMatchFile(t *testing.T) {
	h := newHarness(t)
	h.fs.addItem("ep1", "One: Start", 1)
	feed := h.subscribe(t, SubscribeParams{DownloadLatest: 1})
	ep := h.waitStatus(t, "ep1", "downloaded")
	ctx := context.Background()

	m, err := h.svc.MatchFile(ctx, *ep.FilePath)
	if err != nil || m == nil || m.Episode == nil {
		t.Fatalf("MatchFile(download) = %+v, %v", m, err)
	}
	if m.ShowTitle != "Test: Show" || m.Episode.Title != "One: Start" || m.Episode.PublishedAt == nil {
		t.Errorf("match = %+v / %+v", m, m.Episode)
	}

	dropped := filepath.Join(feed.Folder, "my own file.mp3")
	m, err = h.svc.MatchFile(ctx, dropped)
	if err != nil || m == nil || m.Episode != nil || m.FeedID != feed.ID {
		t.Errorf("MatchFile(dropped in) = %+v, %v; want the feed with no episode", m, err)
	}

	if m, err := h.svc.MatchFile(ctx, filepath.Join(h.root, "Other Show", "x.mp3")); m != nil || err != nil {
		t.Errorf("MatchFile(unrelated) = %+v, %v; want nil", m, err)
	}

	show, item := uuid.New(), uuid.New()
	full, _ := h.svc.MatchFile(ctx, *ep.FilePath)
	if err := h.svc.LinkItems(ctx, full, show, item); err != nil {
		t.Fatalf("LinkItems: %v", err)
	}
	full, _ = h.svc.MatchFile(ctx, *ep.FilePath)
	if full.ShowItemID == nil || *full.ShowItemID != show || full.Episode.ItemID == nil || *full.Episode.ItemID != item {
		t.Errorf("after LinkItems: show %v, episode item %v", full.ShowItemID, full.Episode.ItemID)
	}
}

func TestCapReader(t *testing.T) {
	c := &capReader{r: strings.NewReader("123456"), left: 4}
	_, err := io.ReadAll(c)
	if err != errTooLarge {
		t.Errorf("err = %v, want errTooLarge", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
