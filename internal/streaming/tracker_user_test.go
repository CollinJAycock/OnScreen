package streaming

import (
	"testing"

	"github.com/google/uuid"
)

// Both tracker modes must attribute entries identically; each test runs
// against the in-memory map and the Valkey (miniredis) backend.
func eachTracker(t *testing.T, fn func(t *testing.T, tr *Tracker)) {
	t.Run("memory", func(t *testing.T) { fn(t, NewTracker()) })
	t.Run("valkey", func(t *testing.T) { fn(t, newValkeyTracker(t)) })
}

func TestTracker_HeartbeatUser_AttributesViewer(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		user, item := uuid.New(), uuid.New()
		tr.HeartbeatUser(user, "10.0.0.5", item, "Web — Chrome on Windows", "directPlay")

		entries := tr.List()
		if len(entries) != 1 {
			t.Fatalf("entries: got %d, want 1", len(entries))
		}
		e := entries[0]
		if e.UserID != user || e.MediaItemID != item {
			t.Errorf("attribution: got user=%v item=%v, want %v/%v", e.UserID, e.MediaItemID, user, item)
		}
		if !e.FromHeartbeat || e.ClientName != "Web — Chrome on Windows" || e.Decision != "directPlay" {
			t.Errorf("heartbeat fields: got %+v", e)
		}
	})
}

// An empty decision on a later beat must not wipe the one an earlier beat
// reported (older builds of the same client omit it on pause beacons).
func TestTracker_HeartbeatUser_KeepsDecision(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		user, item := uuid.New(), uuid.New()
		tr.HeartbeatUser(user, "10.0.0.5", item, "TV", "directStream")
		tr.HeartbeatUser(user, "10.0.0.5", item, "TV", "")
		entries := tr.List()
		if len(entries) != 1 || entries[0].Decision != "directStream" {
			t.Fatalf("got %+v, want one entry keeping directStream", entries)
		}
	})
}

// Two users behind one address watching the same title are two streams, not
// one: the key carries the user.
func TestTracker_HeartbeatUser_SameIPDifferentUsers(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		item := uuid.New()
		tr.HeartbeatUser(uuid.New(), "203.0.113.7", item, "Phone", "")
		tr.HeartbeatUser(uuid.New(), "203.0.113.7", item, "Tablet", "")
		if got := len(tr.List()); got != 2 {
			t.Fatalf("entries: got %d, want 2", got)
		}
	})
}

// A zero user keys exactly like the legacy Heartbeat, so RemoveHeartbeat still
// finds it.
func TestTracker_HeartbeatUser_NilUserIsLegacyKey(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		item := uuid.New()
		tr.HeartbeatUser(uuid.Nil, "10.0.0.5", item, "Chrome", "")
		tr.RemoveHeartbeat("10.0.0.5", item)
		if got := len(tr.List()); got != 0 {
			t.Fatalf("legacy RemoveHeartbeat left %d entries", got)
		}
	})
}

func TestTracker_TouchFile_OneEntryPerStream(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		user, item, file := uuid.New(), uuid.New(), uuid.New()
		// Many range requests for the same stream collapse into one entry.
		for i := 0; i < 3; i++ {
			tr.TouchFile(user, "10.0.0.5", item, file, "/media/movie.mkv", "Mozilla")
		}
		entries := tr.List()
		if len(entries) != 1 {
			t.Fatalf("entries: got %d, want 1", len(entries))
		}
		e := entries[0]
		if e.UserID != user || e.MediaItemID != item || e.FileID != file || e.FilePath != "/media/movie.mkv" {
			t.Errorf("file entry: got %+v", e)
		}
		if e.FromHeartbeat {
			t.Error("a byte-traffic entry must not claim to be a heartbeat")
		}
	})
}

// RemoveStream drops both halves of one viewer's stream and nothing else —
// the same user's other device and another user on the same item survive.
func TestTracker_RemoveStream_ScopedToUserClientItem(t *testing.T) {
	eachTracker(t, func(t *testing.T, tr *Tracker) {
		user, other, item := uuid.New(), uuid.New(), uuid.New()
		tr.HeartbeatUser(user, "10.0.0.5", item, "Chrome", "")
		tr.TouchFile(user, "10.0.0.5", item, uuid.New(), "/m.mkv", "Mozilla")
		tr.HeartbeatUser(user, "10.0.0.6", item, "TV", "")     // same user, other device
		tr.HeartbeatUser(other, "10.0.0.5", item, "Other", "") // other user, same IP

		tr.RemoveStream(user, "10.0.0.5", item)

		entries := tr.List()
		if len(entries) != 2 {
			t.Fatalf("entries after RemoveStream: got %d, want 2 (%+v)", len(entries), entries)
		}
		for _, e := range entries {
			if e.UserID == user && e.ClientIP == "10.0.0.5" {
				t.Errorf("stopped stream survived: %+v", e)
			}
		}
	})
}
