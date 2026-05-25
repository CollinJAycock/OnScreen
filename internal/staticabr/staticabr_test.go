package staticabr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
)

// mapStore is a minimal read store backed by key→bytes, for StoreChecker tests.
type mapStore struct{ files map[string][]byte }

func (m mapStore) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	b, ok := m.files[key]
	if !ok {
		return nil, mediastore.ErrNotFound
	}
	return nopSeekCloser{bytes.NewReader(b)}, nil
}
func (m mapStore) Stat(_ context.Context, key string) (mediastore.FileInfo, error) {
	b, ok := m.files[key]
	if !ok {
		return mediastore.FileInfo{}, mediastore.ErrNotFound
	}
	return mediastore.FileInfo{Size: int64(len(b))}, nil
}
func (m mapStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }

func TestStoreChecker_IsEncoded(t *testing.T) {
	id := uuid.New()
	ctx := context.Background()

	// No ladder at all → not encoded.
	if ok, _ := (StoreChecker{Store: mapStore{files: map[string][]byte{}}}).IsEncoded(ctx, id, "h1"); ok {
		t.Error("missing ladder should not be encoded")
	}

	// Master present, hash matches → encoded.
	matched := mapStore{files: map[string][]byte{
		MasterKey(id): []byte("#EXTM3U"),
		HashKey(id):   []byte("h1\n"),
	}}
	if ok, err := (StoreChecker{Store: matched}).IsEncoded(ctx, id, "h1"); err != nil || !ok {
		t.Errorf("matching hash: ok=%v err=%v, want true", ok, err)
	}
	// Hash differs (source replaced) → stale.
	if ok, _ := (StoreChecker{Store: matched}).IsEncoded(ctx, id, "h2"); ok {
		t.Error("differing hash should be stale (not encoded)")
	}

	// Master present but no hash sidecar → treat as stale.
	noHash := mapStore{files: map[string][]byte{MasterKey(id): []byte("#EXTM3U")}}
	if ok, _ := (StoreChecker{Store: noHash}).IsEncoded(ctx, id, "h1"); ok {
		t.Error("master without a hash sidecar should be stale")
	}
}

func TestKeyScheme(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	p := Prefix(id)
	if p != "static-abr/11111111-1111-1111-1111-111111111111" {
		t.Errorf("Prefix = %q", p)
	}
	if got := MasterKey(id); got != p+"/master.m3u8" {
		t.Errorf("MasterKey = %q", got)
	}
	if got := RungPlaylistKey(id, "1080p"); got != p+"/1080p/index.m3u8" {
		t.Errorf("RungPlaylistKey = %q", got)
	}
	if got := SegmentKey(id, "720p", "seg00007.m4s"); got != p+"/720p/seg00007.m4s" {
		t.Errorf("SegmentKey = %q", got)
	}
	if got := HashKey(id); got != p+"/source.hash" {
		t.Errorf("HashKey = %q", got)
	}
	// Every key lives under the file's prefix, so a single Walk(Prefix) covers it.
	for _, k := range []string{MasterKey(id), RungPlaylistKey(id, "480p"), SegmentKey(id, "480p", "s.m4s"), HashKey(id)} {
		if len(k) <= len(p) || k[:len(p)] != p {
			t.Errorf("%q is not under the prefix %q", k, p)
		}
	}
}

// fakeChecker reports encoded=true only for (fileID, hash) pairs it was told.
type fakeChecker struct {
	encoded map[uuid.UUID]string // fileID → the hash it's encoded for
	err     error
}

func (f fakeChecker) IsEncoded(_ context.Context, fileID uuid.UUID, hash string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.encoded[fileID] == hash, nil
}

func TestPlan_SelectsMissingAndStale(t *testing.T) {
	hot := uuid.New()    // encoded, current hash → skip
	stale := uuid.New()  // encoded against an old hash → re-encode
	fresh := uuid.New()  // never encoded → encode
	cold := uuid.New()   // below minPlays → skip
	noHash := uuid.New() // no source hash → skip

	candidates := []Title{
		{FileID: hot, SourceHash: "h1", PlayCount: 100},
		{FileID: stale, SourceHash: "new", PlayCount: 80},
		{FileID: fresh, SourceHash: "h3", PlayCount: 50},
		{FileID: cold, SourceHash: "h4", PlayCount: 2},
		{FileID: noHash, SourceHash: "", PlayCount: 99},
	}
	checker := fakeChecker{encoded: map[uuid.UUID]string{
		hot:   "h1",  // matches → encoded
		stale: "old", // differs → stale
	}}

	got, err := Plan(context.Background(), candidates, checker, 10 /*minPlays*/, 0 /*no limit*/)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := map[uuid.UUID]bool{stale: true, fresh: true}
	if len(got) != len(want) {
		t.Fatalf("planned %d titles, want %d: %+v", len(got), len(want), got)
	}
	for _, ti := range got {
		if !want[ti.FileID] {
			t.Errorf("unexpected title in plan: %s", ti.FileID)
		}
	}
}

func TestPlan_RespectsLimit(t *testing.T) {
	var candidates []Title
	for i := 0; i < 5; i++ {
		candidates = append(candidates, Title{FileID: uuid.New(), SourceHash: "h", PlayCount: 100})
	}
	got, err := Plan(context.Background(), candidates, fakeChecker{}, 1, 2 /*limit*/)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("planned %d, want 2 (limit)", len(got))
	}
}

func TestPlan_PropagatesCheckerError(t *testing.T) {
	boom := errors.New("boom")
	_, err := Plan(context.Background(),
		[]Title{{FileID: uuid.New(), SourceHash: "h", PlayCount: 100}},
		fakeChecker{err: boom}, 1, 0)
	if !errors.Is(err, boom) {
		t.Errorf("got %v, want the checker's error", err)
	}
}
