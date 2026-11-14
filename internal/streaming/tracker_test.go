package streaming

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTracker_SetAndGetItemState(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()

	pos, dur := tr.GetItemState(id)
	if pos != 0 || dur != 0 {
		t.Errorf("initial state: got pos=%d dur=%d, want 0,0", pos, dur)
	}

	tr.SetItemState(id, 5000, 120000)
	pos, dur = tr.GetItemState(id)
	if pos != 5000 {
		t.Errorf("position: got %d, want 5000", pos)
	}
	if dur != 120000 {
		t.Errorf("duration: got %d, want 120000", dur)
	}
}

func TestTracker_SetItemState_Overwrite(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()

	tr.SetItemState(id, 1000, 50000)
	tr.SetItemState(id, 2000, 50000)

	pos, _ := tr.GetItemState(id)
	if pos != 2000 {
		t.Errorf("position: got %d, want 2000", pos)
	}
}

func TestTracker_Touch_NewEntry(t *testing.T) {
	tr := NewTracker()
	tr.Touch("192.168.1.1", "/media/movie.mkv", "Chrome")

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	e := entries[0]
	if e.ClientIP != "192.168.1.1" {
		t.Errorf("ClientIP: got %q", e.ClientIP)
	}
	if e.FilePath != "/media/movie.mkv" {
		t.Errorf("FilePath: got %q", e.FilePath)
	}
	if e.ClientName != "Chrome" {
		t.Errorf("ClientName: got %q", e.ClientName)
	}
}

func TestTracker_Touch_UpdateExisting(t *testing.T) {
	tr := NewTracker()
	tr.Touch("192.168.1.1", "/media/movie.mkv", "Chrome")
	tr.Touch("192.168.1.1", "/media/movie.mkv", "Firefox")

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1 (should deduplicate)", len(entries))
	}
	if entries[0].ClientName != "Firefox" {
		t.Errorf("ClientName not updated: got %q", entries[0].ClientName)
	}
}

func TestTracker_Touch_DifferentStreams(t *testing.T) {
	tr := NewTracker()
	tr.Touch("192.168.1.1", "/media/movie1.mkv", "Client1")
	tr.Touch("192.168.1.2", "/media/movie2.mkv", "Client2")

	entries := tr.List()
	if len(entries) != 2 {
		t.Fatalf("entries: got %d, want 2", len(entries))
	}
}

func TestTracker_List_PrunesExpired(t *testing.T) {
	tr := NewTracker()

	// Directly inject an expired entry.
	tr.mu.Lock()
	tr.entries["old|/old.mkv"] = &Entry{
		FilePath:   "/old.mkv",
		ClientIP:   "old",
		ClientName: "OldClient",
		FirstSeen:  time.Now().Add(-2 * time.Minute),
		LastSeen:   time.Now().Add(-2 * time.Minute),
	}
	tr.mu.Unlock()

	// Touch a fresh entry.
	tr.Touch("new", "/new.mkv", "NewClient")

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1 (expired should be pruned)", len(entries))
	}
	if entries[0].ClientIP != "new" {
		t.Errorf("remaining entry: got client %q, want %q", entries[0].ClientIP, "new")
	}
}

func TestTracker_List_Empty(t *testing.T) {
	tr := NewTracker()
	entries := tr.List()
	if entries != nil && len(entries) != 0 {
		t.Errorf("entries: got %d, want 0", len(entries))
	}
}

// ── Heartbeat (progress-driven entries) ────────────────────────────────────────

func TestTracker_Heartbeat_NewEntry(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()
	tr.HeartbeatUser(uuid.Nil, "10.0.0.5", id, "Chrome", "")

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	e := entries[0]
	if e.MediaItemID != id {
		t.Errorf("MediaItemID: got %v, want %v", e.MediaItemID, id)
	}
	if e.FilePath != "" {
		t.Errorf("FilePath should be empty for a heartbeat entry: got %q", e.FilePath)
	}
	if e.ClientIP != "10.0.0.5" || e.ClientName != "Chrome" {
		t.Errorf("client: got ip=%q name=%q", e.ClientIP, e.ClientName)
	}
}

func TestTracker_Heartbeat_RefreshesNotDuplicates(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()
	tr.HeartbeatUser(uuid.Nil, "10.0.0.5", id, "Chrome", "")
	tr.HeartbeatUser(uuid.Nil, "10.0.0.5", id, "Chrome", "") // same client+item ~5s later

	if entries := tr.List(); len(entries) != 1 {
		t.Fatalf("repeated heartbeat should refresh one entry, got %d", len(entries))
	}
}

func TestTracker_Heartbeat_DistinctFromFileTraffic(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()
	// A file-traffic entry and a heartbeat entry for the same client are
	// distinct rows here (keyed differently); the sessions API collapses them.
	tr.Touch("10.0.0.5", "/media/movie.mkv", "Chrome")
	tr.HeartbeatUser(uuid.Nil, "10.0.0.5", id, "Chrome", "")

	if entries := tr.List(); len(entries) != 2 {
		t.Fatalf("entries: got %d, want 2 (path + item keyed)", len(entries))
	}
}

func TestTracker_RemoveHeartbeat(t *testing.T) {
	tr := NewTracker()
	id := uuid.New()
	tr.HeartbeatUser(uuid.Nil, "10.0.0.5", id, "Chrome", "")
	tr.RemoveHeartbeatUser(uuid.Nil, "10.0.0.5", id)

	if entries := tr.List(); len(entries) != 0 {
		t.Fatalf("entry should be gone after RemoveHeartbeat, got %d", len(entries))
	}
}
