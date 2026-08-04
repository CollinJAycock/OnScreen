package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/valkey"
)

// ── SessionDirName ───────────────────────────────────────────────────────────

// Incarnation 0 MUST be the bare session ID: every non-ABR session, every
// first start, and every pre-upgrade session on disk uses that layout. A
// suffix on incarnation 0 would orphan every live session's directory at
// deploy time.
func TestSessionDirName_ZeroIsBareID(t *testing.T) {
	if got := SessionDirName("abc", 0); got != "abc" {
		t.Errorf("incarnation 0: got %q, want bare id", got)
	}
	if got := SessionDirName("abc", -1); got != "abc" {
		t.Errorf("negative incarnation: got %q, want bare id", got)
	}
	if got := SessionDirName("abc", 2); got != "abc-i2" {
		t.Errorf("incarnation 2: got %q, want abc-i2", got)
	}
	// The Session accessors must agree with the free functions — the API
	// builds URLs from one and the worker builds paths from the other.
	s := &Session{ID: "abc", Incarnation: 3}
	if s.DirName() != SessionDirName("abc", 3) {
		t.Error("Session.DirName disagrees with SessionDirName")
	}
	if s.Dir() != SessionDirFor("abc", 3) {
		t.Error("Session.Dir disagrees with SessionDirFor")
	}
}

// Two incarnations of one session must never share a directory — that shared
// directory is exactly how a superseded ABR rung's cleanup destroyed its
// successor's live segments.
func TestSessionDirName_IncarnationsAreDisjoint(t *testing.T) {
	seen := map[string]int{}
	for inc := 0; inc < 4; inc++ {
		name := SessionDirName("sess", inc)
		if prev, dup := seen[name]; dup {
			t.Fatalf("incarnations %d and %d share directory %q", prev, inc, name)
		}
		seen[name] = inc
	}
}

// ── watchdogVerdict ──────────────────────────────────────────────────────────

// The watchdog's kill decision, branch by branch. The store-error branch is
// the fleet-wide one: killing on ANY error meant a single Valkey restart shot
// every in-flight stream on the deployment at the same heartbeat tick.
func TestWatchdogVerdict(t *testing.T) {
	now := time.Now()
	live := func() *Session {
		return &Session{ID: "s", CreatedAt: now.Add(-5 * time.Second), LastActivityAt: now.Add(-2 * time.Second)}
	}

	cases := []struct {
		name           string
		sess           *Session
		err            error
		jobIncarnation int
		want           watchdogDecision
	}{
		{"healthy session keeps encoding", live(), nil, 0, watchdogKeep},
		{"not-found kills (client stopped)", nil, valkey.ErrNotFound, 0, watchdogGone},
		{"WRAPPED not-found still kills — store.Get wraps its errors",
			nil, fmt.Errorf("get session: %w", valkey.ErrNotFound), 0, watchdogGone},
		{"transient store error keeps encoding — Valkey down proves nothing",
			nil, fmt.Errorf("get session: %w", context.DeadlineExceeded), 0, watchdogStoreError},
		{"newer incarnation kills the old run",
			&Session{ID: "s", Incarnation: 2, CreatedAt: now, LastActivityAt: now}, nil, 1, watchdogSuperseded},
		{"matching nonzero incarnation keeps",
			&Session{ID: "s", Incarnation: 2, CreatedAt: now, LastActivityAt: now}, nil, 2, watchdogKeep},
		{"idle past threshold kills",
			&Session{ID: "s", CreatedAt: now.Add(-10 * time.Minute), LastActivityAt: now.Add(-2 * time.Minute)},
			nil, 0, watchdogIdle},
		{"no activity yet anchors on CreatedAt (still buffering seg 0)",
			&Session{ID: "s", CreatedAt: now.Add(-10 * time.Second)}, nil, 0, watchdogKeep},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := watchdogVerdict(tc.sess, tc.err, tc.jobIncarnation, now); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ── wipeSessionDir ───────────────────────────────────────────────────────────

func newWipeTestWorker(t *testing.T) (*Worker, *SessionStore) {
	t.Helper()
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	return &Worker{store: store, logger: slog.Default()}, store
}

// shortWipeTimings shrinks the wipe loop's clocks for tests and restores them.
func shortWipeTimings(t *testing.T, maxWait, poll time.Duration) {
	t.Helper()
	oldMax, oldPoll := sessionDirWipeMaxWait, sessionDirWipePoll
	sessionDirWipeMaxWait, sessionDirWipePoll = maxWait, poll
	t.Cleanup(func() { sessionDirWipeMaxWait, sessionDirWipePoll = oldMax, oldPoll })
}

func mkSessionDir(t *testing.T, sessionID string, incarnation int) string {
	t.Helper()
	dir := SessionDirFor(sessionID, incarnation)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("x"), 0644); err != nil {
		t.Fatalf("write seg: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// A LIVE session's directory must survive ffmpeg exiting. The old flat-delay
// wipe deleted it 120 s after natural completion, so pausing near the end of
// a film for a few minutes 404'd the ending out from under the player.
func TestWipeSessionDir_LiveSessionDirSurvives(t *testing.T) {
	w, store := newWipeTestWorker(t)
	shortWipeTimings(t, 2*time.Second, 20*time.Millisecond)

	id := "wipe-live-" + uuid.NewString()
	dir := mkSessionDir(t, id, 0)
	if err := store.Create(context.Background(), Session{
		ID: id, UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(),
		CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	done := make(chan struct{})
	go func() { w.wipeSessionDir(id, 0, 0); close(done) }()

	// Well past the old behavior's delete point (delay 0 + several polls): the
	// dir must still be there because the session is still live.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("live session's directory was wiped while the session still existed — " +
			"a paused viewer would 404 on the remaining segments")
	}

	// The moment the session is gone, the wipe should proceed.
	_ = store.Delete(context.Background(), id)
	waitFor(t, 2*time.Second, func() bool {
		_, err := os.Stat(dir)
		return os.IsNotExist(err)
	}, "directory not wiped after the session was deleted")
	<-done
}

// A superseded incarnation must delete ITS OWN directory and leave the
// successor's untouched — the exact critical from the audit: the old run's
// deferred cleanup used to delete the directory the NEW run was writing into.
//
// Uses incarnations 1→2 deliberately: with 0→1 the bare directory and the old
// run's directory coincide, so a wipe that ignores its incarnation argument
// (the pre-fix behavior of always targeting SessionDir(id)) happens to pass.
// A second restart is where that regression would bite — the wipe would
// remove nothing of its own and, with a shared-dir layout, everything of its
// successor's.
func TestWipeSessionDir_SupersededWipesOwnDirOnly(t *testing.T) {
	w, store := newWipeTestWorker(t)
	shortWipeTimings(t, 2*time.Second, 20*time.Millisecond)

	id := "wipe-super-" + uuid.NewString()
	oldDir := mkSessionDir(t, id, 1)
	newDir := mkSessionDir(t, id, 2)
	// Store now holds incarnation 2 — the twice-restarted rung.
	if err := store.Create(context.Background(), Session{
		ID: id, UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(),
		Incarnation: 2, CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The OLD run (incarnation 1) exits and schedules its wipe.
	w.wipeSessionDir(id, 1, 0)

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("superseded incarnation's directory should be wiped — it targeted some other path instead")
	}
	if _, err := os.Stat(filepath.Join(newDir, "seg00000.ts")); err != nil {
		t.Fatal("successor incarnation's directory was touched by the superseded run's cleanup — " +
			"this is the critical the incarnation split exists to prevent")
	}
}

// No session at all → wipe immediately (the normal post-stop cleanup).
func TestWipeSessionDir_GoneSessionWipes(t *testing.T) {
	w, _ := newWipeTestWorker(t)
	shortWipeTimings(t, 2*time.Second, 20*time.Millisecond)

	id := "wipe-gone-" + uuid.NewString()
	dir := mkSessionDir(t, id, 0)
	w.wipeSessionDir(id, 0, 0)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("directory of a deleted session should be wiped")
	}
}

// The max-wait backstop: a session that never goes away (e.g. a client that
// parks on pause for hours refreshing activity elsewhere) still gets its disk
// reclaimed eventually rather than never.
func TestWipeSessionDir_MaxWaitReclaims(t *testing.T) {
	w, store := newWipeTestWorker(t)
	shortWipeTimings(t, 150*time.Millisecond, 20*time.Millisecond)

	id := "wipe-max-" + uuid.NewString()
	dir := mkSessionDir(t, id, 0)
	if err := store.Create(context.Background(), Session{
		ID: id, UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(),
		CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	w.wipeSessionDir(id, 0, 0)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("directory should be reclaimed once the max wait elapses")
	}
}
