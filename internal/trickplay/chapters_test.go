package trickplay

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestChapterThumbTimeSec(t *testing.T) {
	cases := []struct {
		c    Chapter
		want float64
	}{
		{Chapter{StartMS: 0, EndMS: 600_000}, 5},           // long chapter: +5 s
		{Chapter{StartMS: 60_000, EndMS: 80_000}, 62},      // 20 s chapter: +10% = 2 s
		{Chapter{StartMS: 120_000, EndMS: 120_000}, 120},   // unknown length: the start
		{Chapter{StartMS: 300_000, EndMS: 1_000_000}, 305}, // capped at 5 s
		{Chapter{StartMS: 10_000, EndMS: 10_500}, 10.05},   // half-second chapter
	}
	for _, c := range cases {
		if got := ChapterThumbTimeSec(c.c); got != c.want {
			t.Errorf("ChapterThumbTimeSec(%+v) = %v, want %v", c.c, got, c.want)
		}
	}
}

func TestParseChapters(t *testing.T) {
	got := ParseChapters([]byte(`[{"title":"One","start_ms":0,"end_ms":1000.5},{"title":"Two","start_ms":1000.5,"end_ms":2000}]`))
	if len(got) != 2 || got[1].StartMS != 1000 || got[0].EndMS != 1000 {
		t.Errorf("got %+v", got)
	}
	if ParseChapters(nil) != nil || ParseChapters([]byte(`{`)) != nil {
		t.Error("empty / bad JSON should give nil")
	}
}

func TestChapterThumbName(t *testing.T) {
	if n := ChapterThumbName(7); n != "chapter_007.jpg" {
		t.Errorf("got %s", n)
	}
}

// chapterStore is fakeStore plus the chapter-thumbnail count.
type chapterStore struct {
	fakeStore
	thumbs int
	set    bool
}

func (c *chapterStore) SetChapterThumbs(_ context.Context, _ uuid.UUID, n int) error {
	c.thumbs, c.set = n, true
	return nil
}

type chapterLookup struct {
	fakeLookup
	chapters []Chapter
	err      error
}

func (c chapterLookup) FileChapters(context.Context, uuid.UUID) (string, []Chapter, error) {
	return c.path, c.chapters, c.err
}

func makeClip(t *testing.T, seconds string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	p := filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=10:duration="+seconds,
		"-c:v", "libx264", "-pix_fmt", "yuv420p", p).CombinedOutput()
	if err != nil {
		t.Fatalf("make clip: %v: %s", err, out)
	}
	return p
}

// TestGenerateChapterThumbs writes one 320-wide JPEG per chapter and records
// the count; a chapter list running past the end of the file falls back to
// the chapter's start rather than failing the set.
func TestGenerateChapterThumbs(t *testing.T) {
	clip := makeClip(t, "12")
	store := &chapterStore{}
	root := t.TempDir()
	lookup := chapterLookup{
		fakeLookup: fakeLookup{path: clip},
		chapters: []Chapter{
			{StartMS: 0, EndMS: 4000},
			{StartMS: 4000, EndMS: 8000},
			{StartMS: 8000, EndMS: 400_000}, // claims to run far past the end
		},
	}
	g := New(root, store, lookup, silentLogger())
	item := uuid.New()
	n, err := g.GenerateChapterThumbs(context.Background(), item, uuid.New())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if n != 3 || !store.set || store.thumbs != 3 {
		t.Fatalf("n=%d recorded=%v/%d, want 3", n, store.set, store.thumbs)
	}
	for i := 0; i < 3; i++ {
		p := filepath.Join(g.ItemDir(item), ChapterThumbName(i))
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=width,height", "-of", "csv=p=0",
		filepath.Join(g.ItemDir(item), ChapterThumbName(0))).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "320,180\n" && got != "320,180\r\n" {
		t.Errorf("thumb size = %q, want 320,180", got)
	}

	// Regenerating with fewer chapters leaves no stale thumbnails behind.
	lookup.chapters = lookup.chapters[:1]
	g = New(root, store, lookup, silentLogger())
	if n, err := g.GenerateChapterThumbs(context.Background(), item, uuid.New()); err != nil || n != 1 {
		t.Fatalf("regen: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(g.ItemDir(item), ChapterThumbName(1))); !os.IsNotExist(err) {
		t.Errorf("stale chapter_001.jpg survived: %v", err)
	}
}

// TestGenerateChapterThumbs_NoChaptersAndFailures records 0 for a file with
// no chapters and for a failed run, so no thumb_url points at a missing file.
func TestGenerateChapterThumbs_NoChaptersAndFailures(t *testing.T) {
	store := &chapterStore{}
	g := New(t.TempDir(), store, chapterLookup{fakeLookup: fakeLookup{path: "/x.mkv"}}, silentLogger())
	if n, err := g.GenerateChapterThumbs(context.Background(), uuid.New(), uuid.New()); err != nil || n != 0 || !store.set {
		t.Fatalf("no chapters: n=%d err=%v set=%v", n, err, store.set)
	}

	store = &chapterStore{thumbs: 9}
	g = New(t.TempDir(), store, chapterLookup{err: errors.New("boom")}, silentLogger())
	if _, err := g.GenerateChapterThumbs(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("lookup error should surface")
	}

	if testing.Short() {
		return
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return
	}
	store = &chapterStore{thumbs: 9}
	missing := filepath.Join(t.TempDir(), "gone.mkv")
	g = New(t.TempDir(), store, chapterLookup{fakeLookup: fakeLookup{path: missing},
		chapters: []Chapter{{StartMS: 0, EndMS: 1000}}}, silentLogger())
	if _, err := g.GenerateChapterThumbs(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("unreadable source should fail")
	}
	if !store.set || store.thumbs != 0 {
		t.Errorf("a failed run should record 0, got set=%v %d", store.set, store.thumbs)
	}
}

// A lookup without chapter support (or no file) is a quiet no-op.
func TestGenerateChapterThumbs_NoChapterLookup(t *testing.T) {
	store := &chapterStore{}
	g := New(t.TempDir(), store, fakeLookup{path: "/x.mkv"}, silentLogger())
	if n, err := g.GenerateChapterThumbs(context.Background(), uuid.New(), uuid.New()); n != 0 || err != nil || store.set {
		t.Errorf("n=%d err=%v set=%v", n, err, store.set)
	}
}
