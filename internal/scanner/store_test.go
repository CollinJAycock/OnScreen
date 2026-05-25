package scanner

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/mediastore"
)

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
