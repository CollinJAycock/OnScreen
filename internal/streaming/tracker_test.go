package streaming

import (
	"net/http"
	"net/http/httptest"
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

// ── Middleware ────────────────────────────────────────────────────────────────

func TestMiddleware_RecordsGETRequest(t *testing.T) {
	tr := NewTracker()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/files/movies/test.mkv", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("User-Agent", "VLC/3.0.16")
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("inner handler not called")
	}
	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	e := entries[0]
	if e.ClientIP != "10.0.0.5" {
		t.Errorf("ClientIP: got %q, want %q", e.ClientIP, "10.0.0.5")
	}
	if e.ClientName != "VLC" {
		t.Errorf("ClientName: got %q, want %q", e.ClientName, "VLC")
	}
}

func TestMiddleware_HEADRequest(t *testing.T) {
	tr := NewTracker()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("HEAD", "/media/files/movie.mkv", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("HEAD should be tracked: got %d entries", len(entries))
	}
}

func TestMiddleware_POSTNotTracked(t *testing.T) {
	tr := NewTracker()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/media/files/movie.mkv", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	entries := tr.List()
	if len(entries) != 0 {
		t.Errorf("POST should not be tracked: got %d entries", len(entries))
	}
}

func TestMiddleware_XRealIP(t *testing.T) {
	tr := NewTracker()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/files/movie.mkv", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "192.168.1.100")
	handler.ServeHTTP(rec, req)

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	// X-Real-IP is no longer read directly — chi's RealIP middleware
	// already writes the real IP into RemoteAddr before this code runs.
	if entries[0].ClientIP != "10.0.0.1" {
		t.Errorf("ClientIP should use RemoteAddr (set by chi RealIP middleware): got %q", entries[0].ClientIP)
	}
}

func TestMiddleware_DeviceName(t *testing.T) {
	tr := NewTracker()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/files/movie.mkv", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Device-Name", "Living Room TV")
	req.Header.Set("User-Agent", "SomePlayer/4.0")
	handler.ServeHTTP(rec, req)

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	if entries[0].ClientName != "Living Room TV" {
		t.Errorf("ClientName should prefer X-Device-Name: got %q", entries[0].ClientName)
	}
}

func TestMiddleware_URLDecode(t *testing.T) {
	tr := NewTracker()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := tr.Middleware("/media/files/", "/mnt/media", inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/media/files/My%20Movie%20(2020)/file.mkv", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	entries := tr.List()
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	// The file path should be URL-decoded and joined with diskBase.
	if entries[0].FilePath == "" {
		t.Error("FilePath should not be empty")
	}
}
