package v1

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// atom builds an 8-byte ISOBMFF header (size 8, no body).
func atom(typ string) []byte { return append([]byte{0, 0, 0, 8}, []byte(typ)...) }

func TestItemHandler_Faststart(t *testing.T) {
	dir := t.TempDir()
	fast := filepath.Join(dir, "fast.mp4")
	if err := os.WriteFile(fast, append(atom("ftyp"), atom("moov")...), 0o644); err != nil {
		t.Fatal(err)
	}
	slow := filepath.Join(dir, "slow.mp4")
	if err := os.WriteFile(slow, append(atom("ftyp"), atom("mdat")...), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &ItemHandler{} // nil store → defaults to mediastore.Local

	if !h.faststart(context.Background(), fast) {
		t.Error("moov-before-mdat .mp4 should be faststart")
	}
	if h.faststart(context.Background(), slow) {
		t.Error("mdat-before-moov .mp4 should not be faststart")
	}
	if !h.faststart(context.Background(), filepath.Join(dir, "x.mkv")) {
		t.Error("non-MP4 container should short-circuit to true without opening")
	}
	if !h.faststart(context.Background(), filepath.Join(dir, "missing.mp4")) {
		t.Error("unreadable file should assume faststart (true), not block playback")
	}
}

// Item detail asks for every MP4 version's faststart flag on every GET; the
// probe opens the file. fileFaststart memoizes it per (file, size,
// scanned_at): a rewrite the scanner hasn't recorded yet keeps the old answer,
// one it has recorded (new size / scanned_at) is probed again, and a file that
// couldn't be opened is retried rather than remembered.
func TestItemHandler_FileFaststartMemo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.mp4")
	write := func(atoms ...[]byte) {
		t.Helper()
		var b []byte
		for _, a := range atoms {
			b = append(b, a...)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(atom("ftyp"), atom("moov"))

	h := &ItemHandler{}
	f := media.File{ID: uuid.New(), FilePath: path, FileSize: 16, ScannedAt: time.Unix(1_700_000_000, 0)}
	ctx := context.Background()

	if !h.fileFaststart(ctx, f) {
		t.Fatal("moov-first file should be faststart")
	}
	// Rewritten on disk, row unchanged: the memo answers without reading.
	write(atom("ftyp"), atom("mdat"))
	if !h.fileFaststart(ctx, f) {
		t.Fatal("unchanged row should be served from the memo")
	}
	// The scanner records the change: probed again.
	f.ScannedAt = f.ScannedAt.Add(time.Hour)
	if h.fileFaststart(ctx, f) {
		t.Fatal("a new scanned_at must re-probe the file")
	}
	// Past the TTL the memo re-probes even for an unchanged row.
	h.faststartMemo.now = func() time.Time { return time.Now().Add(2 * faststartCacheTTL) }
	write(atom("ftyp"), atom("moov"))
	if !h.fileFaststart(ctx, f) {
		t.Fatal("an expired entry must re-probe the file")
	}
	h.faststartMemo.now = nil

	// An unreadable file says "assume faststart" and is not memoized.
	missing := media.File{ID: uuid.New(), FilePath: filepath.Join(dir, "gone.mp4"), FileSize: 16}
	if !h.fileFaststart(ctx, missing) {
		t.Fatal("unreadable file should assume faststart")
	}
	if _, ok := h.faststartMemo.get(missing); ok {
		t.Fatal("an open failure must not be memoized")
	}
	// Non-MP4 containers never touch the memo.
	mkv := media.File{ID: uuid.New(), FilePath: filepath.Join(dir, "x.mkv")}
	if !h.fileFaststart(ctx, mkv) {
		t.Fatal("non-MP4 should short-circuit to true")
	}
	if _, ok := h.faststartMemo.get(mkv); ok {
		t.Fatal("non-MP4 must not be memoized")
	}
}

// The memo is bounded: filling it past the cap evicts instead of growing.
func TestFaststartCache_Bounded(t *testing.T) {
	var c faststartCache
	for i := 0; i < faststartCacheMax+10; i++ {
		c.put(media.File{ID: uuid.New(), FileSize: int64(i)}, true)
	}
	if n := len(c.m); n != faststartCacheMax {
		t.Fatalf("cache holds %d entries, want %d", n, faststartCacheMax)
	}
}
