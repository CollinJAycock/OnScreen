package notifyagents

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// captureEmits swaps the fan-out for a recorder: every Emit lands in the
// returned slice (after dedupe), nothing is delivered.
func captureEmits(t *testing.T, s *Service) func() []Message {
	t.Helper()
	var mu sync.Mutex
	var got []Message
	s.cancel() // stop the real fan-out loop
	s.wg.Wait()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case m := <-s.events:
				mu.Lock()
				got = append(got, m)
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() { close(stop) })
	return func() []Message {
		mu.Lock()
		defer mu.Unlock()
		return append([]Message(nil), got...)
	}
}

// ── new_content batching ─────────────────────────────────────────────────────

func row(typ, title string, year int32, at time.Time, show *uuid.UUID, showTitle string) gen.ListNewContentForNotificationAgentsRow {
	r := gen.ListNewContentForNotificationAgentsRow{ID: uuid.New(), Type: typ, Title: title, CreatedAt: ts(at)}
	if year > 0 {
		r.Year = &year
	}
	if show != nil {
		r.ShowID = pgtype.UUID{Bytes: *show, Valid: true}
		r.ShowTitle = &showTitle
	}
	return r
}

// TestNewContent_OneMessagePerBatch: three scans of two libraries inside the
// window produce ONE message listing every title, episodes grouped under
// their show; the watermark then moves so the next batch only has new rows.
func TestNewContent_OneMessagePerBatch(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	got := captureEmits(t, s)
	s.newContent.window = 50 * time.Millisecond
	now := time.Now().Add(time.Minute) // after the batcher's start watermark
	movies, tv := uuid.New(), uuid.New()
	show := uuid.New()
	db.newRows[movies] = []gen.ListNewContentForNotificationAgentsRow{
		row("movie", "Dune: Part Two", 2024, now.Add(3*time.Second), nil, ""),
		row("movie", "Arrival", 2016, now.Add(2*time.Second), nil, ""),
	}
	db.newRows[tv] = []gen.ListNewContentForNotificationAgentsRow{
		row("episode", "Ep 3", 0, now.Add(5*time.Second), &show, "Severance"),
		row("episode", "Ep 2", 0, now.Add(4*time.Second), &show, "Severance"),
		row("episode", "orphan", 0, now.Add(4*time.Second), nil, ""),
	}

	s.LibraryHasNewContent(movies)
	s.LibraryHasNewContent(tv)
	s.LibraryHasNewContent(movies)
	waitUntil(t, "batch", func() bool { return len(got()) == 1 })
	time.Sleep(100 * time.Millisecond)
	msgs := got()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Event != EventNewContent || m.Title != "New on OnScreen: 3 titles" {
		t.Errorf("message = %+v", m)
	}
	for _, want := range []string{"• Dune: Part Two (2024)", "• Arrival (2016)", "• Severance — 2 new episodes"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
	if strings.Contains(m.Body, "orphan") || m.URL != "https://media.example.com/" {
		t.Errorf("body/url = %q / %q", m.Body, m.URL)
	}

	// Nothing new since: a later scan report sends nothing.
	s.LibraryHasNewContent(movies)
	time.Sleep(150 * time.Millisecond)
	if len(got()) != 1 {
		t.Errorf("an empty batch sent a message")
	}
	db.mu.Lock()
	last := db.newCalls[len(db.newCalls)-1]
	db.mu.Unlock()
	if !last.Since.Time.Equal(now.Add(3*time.Second)) || last.MaxRows != newContentMaxRows {
		t.Errorf("watermark = %v (max %d), want the newest created_at seen", last.Since.Time, last.MaxRows)
	}
}

func TestNewContentMessage_SingleTitleAndOverflow(t *testing.T) {
	now := time.Now()
	one := row("movie", "Heat", 1995, now, nil, "")
	res, ok := newContentMessage([]gen.ListNewContentForNotificationAgentsRow{one}, false)
	if !ok || res.msg.Title != "New on OnScreen: Heat" || res.itemID != one.ID || res.msg.Body != "• Heat (1995)" {
		t.Errorf("single = %+v", res)
	}

	var rows []gen.ListNewContentForNotificationAgentsRow
	for i := 0; i < 14; i++ {
		rows = append(rows, row("movie", "M"+string(rune('a'+i)), 0, now, nil, ""))
	}
	res, _ = newContentMessage(rows, false)
	if lines := strings.Split(res.msg.Body, "\n"); len(lines) != newContentMaxTitles+1 || lines[len(lines)-1] != "…and 4 more" {
		t.Errorf("overflow body = %q", res.msg.Body)
	}
	res, _ = newContentMessage(rows, true)
	if !strings.HasSuffix(res.msg.Body, "…and 4+ more") || !strings.HasSuffix(res.msg.Title, "14 titles+") {
		t.Errorf("truncated = %q / %q", res.msg.Title, res.msg.Body)
	}
	if _, ok := newContentMessage(nil, false); ok {
		t.Error("empty rows produced a message")
	}
}

// ── scheduled task failures ──────────────────────────────────────────────────

// TestTaskResult_AlertOncePerFailureStreak: a task failing every run alerts
// once; success re-arms it; the backup task maps to backup_failed.
func TestTaskResult_AlertOncePerFailureStreak(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	got := captureEmits(t, s)
	ctx := context.Background()
	syncTask, backup := uuid.New(), uuid.New()
	boom := errors.New("sonarr unreachable")

	s.TaskResult(ctx, syncTask, "Request sync", "arr_request_sync", boom)
	s.TaskResult(ctx, syncTask, "Request sync", "arr_request_sync", boom)
	s.TaskResult(ctx, syncTask, "Request sync", "arr_request_sync", boom)
	waitUntil(t, "first alert", func() bool { return len(got()) == 1 })
	s.TaskResult(ctx, syncTask, "Request sync", "arr_request_sync", nil) // recovers
	s.TaskResult(ctx, syncTask, "Request sync", "arr_request_sync", errors.New("sonarr 503"))
	s.TaskResult(ctx, backup, "Nightly backup", "backup_database", errors.New("pg_dump failed: exit status 1"))
	waitUntil(t, "three alerts", func() bool { return len(got()) == 3 })
	time.Sleep(50 * time.Millisecond)

	msgs := got()
	if len(msgs) != 3 {
		t.Fatalf("alerts = %d, want 3", len(msgs))
	}
	if msgs[0].Event != EventTaskFailed || msgs[0].Title != "Scheduled task failed: Request sync" ||
		!strings.Contains(msgs[0].Body, "sonarr unreachable") || msgs[0].URL != "https://media.example.com/settings/tasks" {
		t.Errorf("task alert = %+v", msgs[0])
	}
	if msgs[2].Event != EventBackupFailed || msgs[2].Title != "Backup failed" || msgs[2].Severity != SeverityError {
		t.Errorf("backup alert = %+v", msgs[2])
	}

	// The suppression window expires on its own too.
	s.taskRealert = 0
	s.TaskResult(ctx, backup, "Nightly backup", "backup_database", errors.New("pg_dump failed: disk full"))
	waitUntil(t, "re-alert", func() bool { return len(got()) == 4 })
}

// ── worker fleet ─────────────────────────────────────────────────────────────

// TestFleetMonitor_TransitionsOnly: no alert during the startup grace or on a
// single empty reading; one "down" after the threshold however long it stays
// down; errors hold state; one "back" on recovery.
func TestFleetMonitor_TransitionsOnly(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	got := captureEmits(t, s)
	var n int
	var countErr error
	clock := time.Now()
	m := NewFleetMonitor(s, func(context.Context) (int, error) { return n, countErr }, nil)
	m.now = func() time.Time { return clock }
	m.start = clock
	ctx := context.Background()

	m.poll(ctx) // empty, inside grace
	m.poll(ctx)
	if len(got()) != 0 {
		t.Fatal("alerted during the startup grace")
	}
	clock = clock.Add(5 * time.Minute)
	n = 1
	m.poll(ctx) // healthy resets the streak
	n = 0
	m.poll(ctx) // one empty reading: not yet
	time.Sleep(20 * time.Millisecond)
	if len(got()) != 0 {
		t.Fatal("alerted on a single empty reading")
	}
	m.poll(ctx) // second: down
	for i := 0; i < 5; i++ {
		m.poll(ctx) // still down: no repeats
	}
	countErr = errors.New("valkey down")
	m.poll(ctx)
	countErr = nil
	waitUntil(t, "down alert", func() bool { return len(got()) == 1 })
	n = 2
	m.poll(ctx)
	m.poll(ctx)
	waitUntil(t, "recovery", func() bool { return len(got()) == 2 })
	time.Sleep(20 * time.Millisecond)

	msgs := got()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if msgs[0].Event != EventWorkerFleetDown || msgs[0].Severity != SeverityError || msgs[0].Title != "No transcode worker available" {
		t.Errorf("down = %+v", msgs[0])
	}
	if msgs[1].Severity != SeveritySuccess || !strings.Contains(msgs[1].Body, "2 transcode workers are available") ||
		msgs[1].URL != "https://media.example.com/settings/transcode" {
		t.Errorf("up = %+v", msgs[1])
	}
}

// TestFleetMonitor_ClaimDedupesAcrossNodes: with a claim function, only the
// node that wins the claim announces the transition.
func TestFleetMonitor_ClaimDedupesAcrossNodes(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	got := captureEmits(t, s)
	claimed := map[string]bool{}
	var mu sync.Mutex
	claim := func(_ context.Context, key string, _ time.Duration) bool {
		mu.Lock()
		defer mu.Unlock()
		if claimed[key] {
			return false
		}
		claimed[key] = true
		return true
	}
	for i := 0; i < 2; i++ { // two nodes
		m := NewFleetMonitor(s, func(context.Context) (int, error) { return 0, nil }, nil).WithClaim(claim)
		m.start = time.Now().Add(-time.Hour)
		m.poll(context.Background())
		m.poll(context.Background())
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(got()); n != 1 {
		t.Errorf("down alerts across two nodes = %d, want 1", n)
	}
}

// sharedClaim is a SET NX shared by simulated nodes.
func sharedClaim() func(context.Context, string, time.Duration) bool {
	var mu sync.Mutex
	claimed := map[string]bool{}
	return func(_ context.Context, key string, _ time.Duration) bool {
		mu.Lock()
		defer mu.Unlock()
		if claimed[key] {
			return false
		}
		claimed[key] = true
		return true
	}
}

// fleetNode is one simulated server node: it polls once a minute at offset
// past the minute, from start on (a zero start = long running, past the
// grace).
type fleetNode struct {
	offset time.Duration
	start  time.Duration
}

// runFleet drives the nodes through script — the live worker count per
// minute — on a fake clock aligned to a minute boundary.
func runFleet(t *testing.T, s *Service, claim func(context.Context, string, time.Duration) bool, nodes []fleetNode, script []int) {
	t.Helper()
	base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	var n int
	var clock time.Time
	mons := make([]*FleetMonitor, len(nodes))
	for i, nd := range nodes {
		m := NewFleetMonitor(s, func(context.Context) (int, error) { return n, nil }, quiet)
		if claim != nil {
			m.WithClaim(claim)
		}
		m.now = func() time.Time { return clock }
		m.start = base.Add(-time.Hour)
		if nd.start > 0 {
			m.start = base.Add(nd.start)
		}
		mons[i] = m
	}
	for k, count := range script {
		n = count
		for i, m := range mons {
			clock = base.Add(time.Duration(k)*time.Minute + nodes[i].offset)
			if clock.Before(m.start) {
				continue
			}
			m.poll(context.Background())
		}
	}
}

// settledTitles waits (briefly) for at least n captured messages, then lets
// any extra ones land, and returns every title — without failing, so a
// mismatch reports what was announced.
func settledTitles(got func() []Message, n int) []string {
	deadline := time.Now().Add(2 * time.Second)
	for len(got()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	return titles(got())
}

func titles(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Title
	}
	return out
}

// TestFleetMonitor_FlapAnnouncedEveryTime: down → up → down → up inside the
// old ten-minute claim window is four announcements — each transition once,
// across nodes that poll in the same tick or a tick apart, and on a single
// node without a claim (the dedupe window must not eat the second outage).
func TestFleetMonitor_FlapAnnouncedEveryTime(t *testing.T) {
	script := []int{1, 0, 0, 1, 0, 0, 1} // six minutes
	want := []string{"No transcode worker available", "Transcode workers are back",
		"No transcode worker available", "Transcode workers are back"}
	cases := []struct {
		name  string
		claim bool
		nodes []fleetNode
	}{
		{"one node, no claim", false, []fleetNode{{offset: 10 * time.Second}}},
		{"two nodes, same tick", true, []fleetNode{{offset: 10 * time.Second}, {offset: 40 * time.Second}}},
		// The second node polls 40s after the first, in the next minute:
		// it detects each transition one tick later.
		{"two nodes, adjacent ticks", true, []fleetNode{{offset: 50 * time.Second}, {offset: 90 * time.Second}}},
		{"three nodes", true, []fleetNode{{offset: 5 * time.Second}, {offset: 35 * time.Second}, {offset: 65 * time.Second}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := testService(t, newFakeDB(), "")
			got := captureEmits(t, s)
			var claim func(context.Context, string, time.Duration) bool
			if c.claim {
				claim = sharedClaim()
			}
			runFleet(t, s, claim, c.nodes, script)
			if g := settledTitles(got, len(want)); !slices.Equal(g, want) {
				t.Errorf("announcements = %q, want %q", g, want)
			}
		})
	}
}

// TestFleetMonitor_LateNodeDoesNotReannounce: a node that starts during an
// outage another node already announced stays quiet when its own startup
// grace ends (it adopts the down state), and the recovery is announced once.
// A node that boots into an outage nobody announced still announces it.
func TestFleetMonitor_LateNodeDoesNotReannounce(t *testing.T) {
	s := testService(t, newFakeDB(), "")
	got := captureEmits(t, s)
	claim := sharedClaim()
	runFleet(t, s, claim, []fleetNode{
		{offset: 10 * time.Second},
		{offset: 40 * time.Second, start: 3*time.Minute + 40*time.Second}, // boots mid-outage
	}, []int{1, 0, 0, 0, 0, 0, 0, 0, 0, 2})
	if g, want := settledTitles(got, 2), []string{"No transcode worker available", "Transcode workers are back"}; !slices.Equal(g, want) {
		t.Errorf("announcements = %q, want %q", g, want)
	}

	s2 := testService(t, newFakeDB(), "")
	got2 := captureEmits(t, s2)
	runFleet(t, s2, sharedClaim(), []fleetNode{
		{offset: 10 * time.Second, start: 10 * time.Second},
		{offset: 40 * time.Second, start: 40 * time.Second},
	}, []int{0, 0, 0, 0, 0, 0})
	if g := settledTitles(got2, 1); len(g) != 1 || g[0] != "No transcode worker available" {
		t.Errorf("boot-time announcements = %q, want one down", g)
	}
}
