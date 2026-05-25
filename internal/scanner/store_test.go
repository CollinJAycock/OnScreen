package scanner

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dhowden/tag"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// stubConc satisfies ConcurrencyProvider for full-scan tests.
type stubConc struct{}

func (stubConc) ScanFileConcurrency() int    { return 2 }
func (stubConc) ScanLibraryConcurrency() int { return 1 }

// fakeRemoteStore is a non-local Store+Lister backed by an in-memory key→bytes
// map, so a scan exercises the remote (store.Walk) discovery branch.
type fakeRemoteStore struct{ files map[string][]byte }

func (f *fakeRemoteStore) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	b, ok := f.files[key]
	if !ok {
		return nil, mediastore.ErrNotFound
	}
	return nopSeekCloser{bytes.NewReader(b)}, nil
}
func (f *fakeRemoteStore) Stat(_ context.Context, key string) (mediastore.FileInfo, error) {
	b, ok := f.files[key]
	if !ok {
		return mediastore.FileInfo{}, mediastore.ErrNotFound
	}
	return mediastore.FileInfo{Size: int64(len(b)), ModTime: time.Now()}, nil
}
func (f *fakeRemoteStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (f *fakeRemoteStore) Walk(_ context.Context, prefix string, fn func(mediastore.ObjectInfo) error) error {
	for k, b := range f.files {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if err := fn(mediastore.ObjectInfo{Key: k, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
			return err
		}
	}
	return nil
}

func TestScanLibrary_RemoteStoreDiscoversViaWalk(t *testing.T) {
	// A non-local store routes discovery through store.Walk. The .mkv is found and
	// a file row created; the .artwork poster is pruned (skipped dir + image in a
	// non-photo library), proving the remote walk applies the same exclusions.
	svc := newMockMediaService()
	store := &fakeRemoteStore{files: map[string][]byte{
		"/srv/media/Movies/Dune (2021)/Dune.mkv": []byte("fake-mkv-bytes"),
		"/srv/media/Movies/.artwork/poster.jpg":  []byte("img"),
	}}
	s := New(svc, nil, stubConc{}, slog.Default()).WithMediaStore(store)

	res, err := s.ScanLibrary(context.Background(), uuid.New(), "movie", []string{"/srv/media/Movies"})
	if err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}
	if len(svc.files) != 1 {
		t.Errorf("created %d file rows, want 1 (.mkv only; .artwork excluded)", len(svc.files))
	}
	if res.Found != 1 {
		t.Errorf("Found = %d, want 1", res.Found)
	}
}

func TestPathHasSkippedDir(t *testing.T) {
	cases := []struct {
		key, root string
		want      bool
	}{
		{"/srv/media/Movies/Dune.mkv", "/srv/media", false},
		{"/srv/media/.artwork/poster.jpg", "/srv/media", true},
		{"/srv/media/TV/@eaDir/thumb.jpg", "/srv/media", true},
		{"/srv/media/.Trash-1000/old.mkv", "/srv/media", true},
		{"/srv/media/A/B/c.mkv", "/srv/media", false},
		// The filename itself matching a skip name must NOT trip it (only dirs).
		{"/srv/media/@eaDir", "/srv/media", false},
	}
	for _, tc := range cases {
		if got := pathHasSkippedDir(tc.key, tc.root); got != tc.want {
			t.Errorf("pathHasSkippedDir(%q,%q) = %v, want %v", tc.key, tc.root, got, tc.want)
		}
	}
}

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }

// recordingStore serves a fixed blob from memory and records how it was read, so
// a test can prove the scanner reaches the source through the store rather than
// the local filesystem.
type recordingStore struct {
	data        []byte
	modTime     time.Time
	opens, stat int
}

func (s *recordingStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	s.opens++
	return nopSeekCloser{bytes.NewReader(s.data)}, nil
}
func (s *recordingStore) Stat(context.Context, string) (mediastore.FileInfo, error) {
	s.stat++
	return mediastore.FileInfo{Size: int64(len(s.data)), ModTime: s.modTime}, nil
}
func (s *recordingStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func TestHashFileStore_ReadsThroughStore(t *testing.T) {
	store := &recordingStore{data: bytes.Repeat([]byte("x"), 1024), modTime: time.Now()}
	h, err := HashFileStore(context.Background(), store, "/objstore/unique-hashtest.mkv", int64(len(store.data)), store.modTime)
	if err != nil {
		t.Fatalf("HashFileStore: %v", err)
	}
	if h == nil || *h == "" {
		t.Fatal("expected a hash")
	}
	if store.opens == 0 {
		t.Error("hash did not read bytes through the store")
	}
}

func TestReadMusicTagsStore_ReadsThroughStore(t *testing.T) {
	orig := readTagFrom
	readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
		_, _ = io.ReadAll(r) // prove the store-backed reader is real
		return &stubTagMetadata{artist: "Boards of Canada", album: "MHTRTC", title: "Roygbiv", trackN: 4}, nil
	}
	defer func() { readTagFrom = orig }()

	store := &recordingStore{data: []byte("fake-audio-bytes"), modTime: time.Now()}
	tags, err := ReadMusicTagsStore(context.Background(), store, "/objstore/BoC/MHTRTC/04.flac")
	if err != nil {
		t.Fatalf("ReadMusicTagsStore: %v", err)
	}
	if store.opens == 0 {
		t.Error("tags were not read through the store")
	}
	if tags.Artist != "Boards of Canada" || tags.Title != "Roygbiv" {
		t.Errorf("tags not parsed: %+v", tags)
	}
}

func TestProbeImageReader_Dimensions(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	pr := ProbeImageReader(&buf, "photo.jpg")
	if pr.ResolutionW == nil || *pr.ResolutionW != 12 || pr.ResolutionH == nil || *pr.ResolutionH != 8 {
		t.Errorf("dims = %v x %v, want 12x8", pr.ResolutionW, pr.ResolutionH)
	}
	if pr.Container == nil || *pr.Container != "jpg" {
		t.Errorf("container = %v, want jpg", pr.Container)
	}
}

func TestProbeImageReader_BadDataReturnsEmpty(t *testing.T) {
	pr := ProbeImageReader(bytes.NewReader([]byte("not an image")), "x.jpg")
	if pr.ResolutionW != nil {
		t.Error("expected empty result for undecodable data")
	}
}

func TestExtractEXIFReader_NoEXIFReturnsNilNil(t *testing.T) {
	// A plain JPEG has no EXIF block → (nil, nil) soft miss, not an error.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	ex, err := ExtractEXIFReader(&buf)
	if err != nil {
		t.Fatalf("err = %v, want nil (soft miss)", err)
	}
	if ex != nil {
		t.Errorf("ex = %+v, want nil (no EXIF block)", ex)
	}
}

func TestHashFileStore_CacheHitSkipsSecondOpen(t *testing.T) {
	store := &recordingStore{data: bytes.Repeat([]byte("z"), 2048), modTime: time.Now()}
	const key = "/objstore/unique-cachehit.mkv"

	h1, err := HashFileStore(context.Background(), store, key, int64(len(store.data)), store.modTime)
	if err != nil {
		t.Fatal(err)
	}
	opensAfter := store.opens
	h2, err := HashFileStore(context.Background(), store, key, int64(len(store.data)), store.modTime)
	if err != nil {
		t.Fatal(err)
	}
	if *h1 != *h2 {
		t.Error("hash changed across identical calls")
	}
	if store.opens != opensAfter {
		t.Errorf("cache hit re-opened the source: %d → %d", opensAfter, store.opens)
	}
}

func TestProcessFile_StatsThroughStore(t *testing.T) {
	// The key does NOT exist on the local filesystem — only the store can stat it.
	// A successful mtime fast-skip therefore proves processFile stats via the
	// store (os.Stat would have errored "no such file").
	const key = "/objstore/Music/track.flac"
	store := &recordingStore{data: bytes.Repeat([]byte("a"), 4096), modTime: time.Now().Add(-time.Hour)}

	itemID := uuid.New()
	hash := "deadbeefcafefood"
	poster := "Music/x/poster.jpg"
	durationMS := int64(180_000)
	svc := newMockMediaService()
	svc.items[itemID] = &media.Item{ID: itemID, Type: "track", Title: "T", PosterPath: &poster}
	svc.fileByPath[key] = &media.File{
		ID:          uuid.New(),
		MediaItemID: itemID,
		FilePath:    key,
		FileSize:    int64(len(store.data)), // matches store.Stat → fast-skip eligible
		FileHash:    &hash,
		DurationMS:  &durationMS,
		Status:      "active",
		ScannedAt:   time.Now(), // strictly after store.modTime
	}

	s := newTestScanner(svc).WithMediaStore(store)
	item, file, isNew, err := s.processFile(context.Background(), uuid.New(), "music", key, []string{"/objstore"})
	if err != nil {
		t.Fatalf("processFile: %v (os.Stat leaked instead of store.Stat?)", err)
	}
	if item != nil || file != nil || isNew {
		t.Errorf("expected clean fast-skip (nil,nil,false); got item=%v file=%v isNew=%v", item, file, isNew)
	}
	if store.stat == 0 {
		t.Error("processFile did not stat through the store")
	}
}
