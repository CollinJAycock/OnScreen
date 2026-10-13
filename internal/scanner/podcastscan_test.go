package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/podcast"
)

// TestParsePodcastPath_ShowFolder is the canonical layout —
// <root>/<Show>/<episode>.mp3.
func TestParsePodcastPath_ShowFolder(t *testing.T) {
	show, ep := parsePodcastPath(
		"/media/Podcasts/The Daily/2026-04-25 - Episode Title.mp3",
		[]string{"/media/Podcasts"},
	)
	if show != "The Daily" {
		t.Errorf("show = %q, want The Daily", show)
	}
	if ep != "2026-04-25 - Episode Title" {
		t.Errorf("episode = %q", ep)
	}
}

// TestParsePodcastPath_LooseAtRoot covers a file at the library root
// — show falls back to "Unknown Show" so the file isn't lost from
// the grid.
func TestParsePodcastPath_LooseAtRoot(t *testing.T) {
	show, ep := parsePodcastPath(
		"/media/Podcasts/orphan.mp3",
		[]string{"/media/Podcasts"},
	)
	if show != "Unknown Show" {
		t.Errorf("show = %q, want Unknown Show", show)
	}
	if ep != "orphan" {
		t.Errorf("episode = %q, want orphan", ep)
	}
}

// TestFileTypeForLibrary_Podcast confirms podcast libraries default
// items to podcast_episode (not "movie" or "track").
func TestFileTypeForLibrary_Podcast(t *testing.T) {
	if got := fileTypeForLibrary("podcast"); got != "podcast_episode" {
		t.Errorf("fileTypeForLibrary(podcast) = %q, want podcast_episode", got)
	}
}

// fakePodcastFeeds answers MatchFile from a fixed table and records links.
type fakePodcastFeeds struct {
	byPath   map[string]*podcast.FileMatch
	byFolder map[string]*podcast.FileMatch
	links    [][2]uuid.UUID
}

func (f *fakePodcastFeeds) MatchFile(_ context.Context, path string) (*podcast.FileMatch, error) {
	if m, ok := f.byPath[path]; ok {
		return m, nil
	}
	if m, ok := f.byFolder[filepath.Dir(path)]; ok {
		return m, nil
	}
	return nil, nil
}

func (f *fakePodcastFeeds) LinkItems(_ context.Context, m *podcast.FileMatch, showID, episodeItemID uuid.UUID) error {
	f.links = append(f.links, [2]uuid.UUID{showID, episodeItemID})
	m.ShowItemID = &showID
	if m.Episode != nil {
		m.Episode.ItemID = &episodeItemID
	}
	return nil
}

// A downloaded episode is named from its feed: the show takes the feed's
// title and description, the episode its title, notes, date and duration,
// and the show points at the feed artwork saved in its folder.
func TestProcessPodcast_FromFeed(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Test - Show")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "poster.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "2026-10-12 Ep 1 - Hi.mp3")
	pub := time.Date(2026, 10, 12, 7, 0, 0, 0, time.UTC)
	dur := int64(3723000)
	feeds := &fakePodcastFeeds{byPath: map[string]*podcast.FileMatch{path: {
		FeedID: uuid.New(), Folder: folder, ShowTitle: "Test: Show", ShowSummary: "About the show",
		Episode: &podcast.MatchedEpisode{ID: uuid.New(), Title: "Ep 1: Hi", Summary: "Notes", PublishedAt: &pub, DurationMS: &dur},
	}}}
	svc := newMockMediaService()
	s := newTestScanner(svc).WithPodcastFeeds(feeds)

	ep, err := s.processPodcast(context.Background(), uuid.New(), path, []string{root})
	if err != nil {
		t.Fatalf("processPodcast: %v", err)
	}
	if ep.Title != "Ep 1: Hi" || ep.Summary == nil || *ep.Summary != "Notes" {
		t.Errorf("episode title/summary = %q / %v", ep.Title, ep.Summary)
	}
	if ep.OriginallyAvailableAt == nil || !ep.OriginallyAvailableAt.Equal(pub) || ep.Year == nil || *ep.Year != 2026 {
		t.Errorf("episode date/year = %v / %v", ep.OriginallyAvailableAt, ep.Year)
	}
	if ep.DurationMS == nil || *ep.DurationMS != dur {
		t.Errorf("episode duration = %v", ep.DurationMS)
	}
	show := svc.items[*ep.ParentID]
	if show.Title != "Test: Show" || show.Summary == nil || *show.Summary != "About the show" {
		t.Errorf("show title/summary = %q / %v", show.Title, show.Summary)
	}
	if show.PosterPath == nil || *show.PosterPath != "Test - Show/poster.jpg" {
		t.Errorf("show poster = %v", show.PosterPath)
	}
	if len(feeds.links) != 1 || feeds.links[0] != [2]uuid.UUID{show.ID, ep.ID} {
		t.Errorf("links = %v", feeds.links)
	}

	// A rescan reuses the linked items instead of matching by title.
	calls := len(svc.hierarchyCalls)
	again, err := s.processPodcast(context.Background(), ep.LibraryID, path, []string{root})
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if again.ID != ep.ID || len(svc.hierarchyCalls) != calls {
		t.Errorf("rescan made item %v (was %v) with %d new find-or-create calls", again.ID, ep.ID, len(svc.hierarchyCalls)-calls)
	}
}

// A file dropped into a feed's folder joins the feed's show, named by its
// file name.
func TestProcessPodcast_DroppedIntoFeedFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Test - Show")
	feeds := &fakePodcastFeeds{byFolder: map[string]*podcast.FileMatch{folder: {
		FeedID: uuid.New(), Folder: folder, ShowTitle: "Test: Show",
	}}}
	svc := newMockMediaService()
	s := newTestScanner(svc).WithPodcastFeeds(feeds)

	ep, err := s.processPodcast(context.Background(), uuid.New(), filepath.Join(folder, "my recording.mp3"), []string{root})
	if err != nil {
		t.Fatalf("processPodcast: %v", err)
	}
	if ep.Title != "my recording" {
		t.Errorf("episode title = %q, want the file name", ep.Title)
	}
	if show := svc.items[*ep.ParentID]; show.Title != "Test: Show" {
		t.Errorf("show = %q, want the feed's title", show.Title)
	}
}

// Without a subscription a podcast file is named from its folder and file.
func TestProcessPodcast_NoFeed(t *testing.T) {
	svc := newMockMediaService()
	s := newTestScanner(svc).WithPodcastFeeds(&fakePodcastFeeds{})
	ep, err := s.processPodcast(context.Background(), uuid.New(), "/media/Podcasts/The Daily/ep.mp3", []string{"/media/Podcasts"})
	if err != nil {
		t.Fatalf("processPodcast: %v", err)
	}
	if show := svc.items[*ep.ParentID]; show.Title != "The Daily" || ep.Title != "ep" {
		t.Errorf("show/episode = %q / %q", show.Title, ep.Title)
	}
}
