package streaming

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/valkey"
)

// newValkeyTracker spins up a miniredis-backed Tracker. Mirrors the
// in-memory tests but exercises the `if t.v != nil` branches in
// SetItemState / GetItemState / Touch / List that the existing
// in-memory tests skip — those branches were ~45% of the package.
func newValkeyTracker(t *testing.T) *Tracker {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// valkey.Client field is unexported; we go through the public
	// constructor which expects a URL. Use the miniredis address.
	c, err := valkey.New(context.Background(), fmt.Sprintf("redis://%s", mr.Addr()))
	if err != nil {
		t.Fatalf("valkey.New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return NewValkeyTracker(c)
}

func TestTracker_Valkey_SetAndGetItemState(t *testing.T) {
	tr := newValkeyTracker(t)
	id := uuid.New()

	pos, dur := tr.GetItemState(id)
	if pos != 0 || dur != 0 {
		t.Errorf("initial: got pos=%d dur=%d, want 0,0", pos, dur)
	}

	tr.SetItemState(id, 12345, 678910)
	pos, dur = tr.GetItemState(id)
	if pos != 12345 {
		t.Errorf("position: got %d, want 12345", pos)
	}
	if dur != 678910 {
		t.Errorf("duration: got %d, want 678910", dur)
	}
}

func TestTracker_Valkey_SetItemState_Overwrites(t *testing.T) {
	tr := newValkeyTracker(t)
	id := uuid.New()

	tr.SetItemState(id, 1000, 50000)
	tr.SetItemState(id, 9000, 50000)

	pos, _ := tr.GetItemState(id)
	if pos != 9000 {
		t.Errorf("position: got %d, want 9000 (overwrite)", pos)
	}
}

func TestTracker_Valkey_TouchAndList(t *testing.T) {
	tr := newValkeyTracker(t)
	tr.Touch("10.0.0.1", "/media/movie.mkv", "Chrome")
	tr.Touch("10.0.0.2", "/media/show.mkv", "Firefox")

	entries := tr.List()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	seen := map[string]string{}
	for _, e := range entries {
		seen[e.ClientIP] = e.ClientName
	}
	if seen["10.0.0.1"] != "Chrome" {
		t.Errorf("10.0.0.1 client = %q, want Chrome", seen["10.0.0.1"])
	}
	if seen["10.0.0.2"] != "Firefox" {
		t.Errorf("10.0.0.2 client = %q, want Firefox", seen["10.0.0.2"])
	}
}

func TestTracker_Valkey_Touch_DedupesSameKey(t *testing.T) {
	// Same (clientIP, filePath) → one entry. Second Touch updates the
	// existing row's ClientName + LastSeen.
	tr := newValkeyTracker(t)
	tr.Touch("10.0.0.1", "/media/movie.mkv", "Chrome")
	tr.Touch("10.0.0.1", "/media/movie.mkv", "Firefox")

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (same key should dedupe)", len(entries))
	}
	if entries[0].ClientName != "Firefox" {
		t.Errorf("ClientName = %q, want Firefox (latest Touch wins)", entries[0].ClientName)
	}
}

func TestTracker_Valkey_List_EmptyReturnsNil(t *testing.T) {
	tr := newValkeyTracker(t)
	entries := tr.List()
	if entries != nil {
		t.Errorf("got %v, want nil for empty Valkey", entries)
	}
}

func TestTracker_Valkey_GetItemState_MissingReturnsZero(t *testing.T) {
	// Exercises the `err != nil` branch in the Valkey GetItemState
	// path — must NOT panic, must return zeros.
	tr := newValkeyTracker(t)
	pos, dur := tr.GetItemState(uuid.New())
	if pos != 0 || dur != 0 {
		t.Errorf("missing item: got pos=%d dur=%d, want 0,0", pos, dur)
	}
}
