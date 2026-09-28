package notifyagents

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/notification"
)

// TestDispatch_PrivateLibraryItemsNeverNamed: a forwarded event about an item
// in a private library still reaches the channel, but neither the title nor
// the body names the title, and the link doesn't point at the item. A public
// item is announced as before; an item the lookup can't find fails closed.
func TestDispatch_PrivateLibraryItemsNeverNamed(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"house"}`)
	alice := uuid.New()
	db.users[alice] = "alice"
	privIssue, privAvail, pubIssue, gone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	db.privateItems[privIssue], db.privateItems[privAvail] = true, true
	db.goneItems[gone] = true

	issue := func(item uuid.UUID, title string) {
		s.DispatchAgentEvent(context.Background(), notification.AgentEvent{
			Type: notification.TypeIssueReported, Title: "Problem reported",
			Body:   "alice reported a problem with " + title + ": subtitles",
			ItemID: &item, Scope: notification.AgentScopeAdmins,
		})
	}
	issue(privIssue, "Secret Show — S01E02 · Pilot: The Beginning")
	issue(pubIssue, "Public Movie")
	issue(gone, "Vanished Show")
	s.DispatchAgentEvent(context.Background(), notification.AgentEvent{
		Type: notification.TypeRequestAvailable, Title: "Now available: Secret Movie",
		Body: `"Secret Movie" is ready to watch.`, ItemID: &privAvail, UserID: &alice,
		Scope: notification.AgentScopeUser,
	})

	waitUntil(t, "four deliveries", func() bool { return len(rs.got()) == 4 })
	time.Sleep(100 * time.Millisecond)
	byBody := map[string]capturedRequest{}
	for _, r := range rs.got() {
		for _, leak := range []string{"Secret", "Vanished", privAvail.String(), "/watch/"} {
			if strings.Contains(r.body, leak) || strings.Contains(r.header.Get("Title"), leak) || strings.Contains(r.header.Get("Click"), leak) {
				t.Errorf("delivery leaks %q: title=%q body=%q click=%q", leak, r.header.Get("Title"), r.body, r.header.Get("Click"))
			}
		}
		byBody[r.body] = r
	}
	// The two private reports render the same text; both still go out.
	wantPrivIssue := "alice reported a problem with a title in a private library: subtitles"
	if len(rs.got()) != 4 {
		t.Fatalf("deliveries = %d, want 4", len(rs.got()))
	}
	n := 0
	for _, r := range rs.got() {
		if r.body == wantPrivIssue {
			n++
		}
	}
	if n != 2 {
		t.Errorf("private/unknown-item reports delivered as %q: %d, want 2 (bodies %v)", wantPrivIssue, n, keys(byBody))
	}
	if _, ok := byBody["alice reported a problem with Public Movie: subtitles"]; !ok {
		t.Errorf("public report lost its title: %v", keys(byBody))
	}
	avail, ok := byBody["A requested title in a private library is ready to watch.\nRequested by alice."]
	if !ok {
		t.Fatalf("private availability body missing: %v", keys(byBody))
	}
	if avail.header.Get("Title") != "Request available" || avail.header.Get("Click") != "https://media.example.com/requests" {
		t.Errorf("private availability title/link = %q / %q", avail.header.Get("Title"), avail.header.Get("Click"))
	}
}

func keys(m map[string]capturedRequest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDispatch_RequestFailedReasonRedacted: the channel takes the admin copy
// of request_failed (it names the requester) but never the *arr reason —
// which can name download-client hosts and paths — only the wording the API
// gives non-admins.
func TestDispatch_RequestFailedReasonRedacted(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"house"}`)
	s.DispatchAgentEvent(context.Background(), notification.AgentEvent{
		Type: notification.TypeRequestFailed, Title: "Request download failed",
		Body:  `alice's request for "Dune: Part \"Two\" (2024)" failed in Radarr 4K: Import failed: /mnt/nas-01/downloads/dune not found on sabnzbd.lan:8080`,
		Scope: notification.AgentScopeAdmins,
	})
	waitUntil(t, "delivery", func() bool { return len(rs.got()) == 1 })
	r := rs.got()[0]
	want := `"Dune: Part \"Two\" (2024)", requested by alice.` + "\nThe download failed — an admin has the details."
	if r.body != want {
		t.Errorf("body = %q\nwant %q", r.body, want)
	}
	for _, leak := range []string{"/mnt", "sabnzbd", "Import failed", "Radarr 4K"} {
		if strings.Contains(r.body, leak) || strings.Contains(r.header.Get("Title"), leak) {
			t.Errorf("request_failed leaks %q: %q", leak, r.body)
		}
	}
}

func TestPrivateItemText(t *testing.T) {
	cases := []struct {
		event, title, body string
		wantTitle          string
		wantBody           string
	}{
		{EventIssueReported, "Problem reported", "bob reported a problem with Star Wars: A New Hope: wrong match",
			"Problem reported", "bob reported a problem with a title in a private library: wrong match"},
		// An unknown kind is dropped with the title (it could be the title's tail).
		{EventIssueReported, "Problem reported", "bob reported a problem with Star Wars: A New Hope",
			"Problem reported", "bob reported a problem with a title in a private library."},
		{EventIssueReported, "Problem reported", "someone changed the wording of Secret",
			"Problem reported", "A problem was reported with a title in a private library."},
		{EventRequestSeasonAvailable, "Season 3 now available: Secret Show", `Season 3 of "Secret Show" is ready to watch.`,
			"Season 3 available", "Season 3 of a requested show in a private library is ready to watch."},
		{EventRequestSeasonAvailable, "Secret Show: new seasons", "x",
			"Requested seasons available", "More of a requested show in a private library is ready to watch."},
		{EventRequestAvailable, "Now available: Secret", `"Secret" is ready to watch.`,
			"Request available", "A requested title in a private library is ready to watch."},
		{EventRequestPending, "New request: Secret", "Secret", "New request", "About a title in a private library."},
	}
	for _, c := range cases {
		gotTitle, gotBody := privateItemText(c.event, c.title, c.body)
		if gotTitle != c.wantTitle || gotBody != c.wantBody {
			t.Errorf("privateItemText(%s, %q, %q) = %q, %q; want %q, %q", c.event, c.title, c.body, gotTitle, gotBody, c.wantTitle, c.wantBody)
		}
		if strings.Contains(gotTitle+gotBody, "Secret") || strings.Contains(gotTitle+gotBody, "Star Wars") {
			t.Errorf("privateItemText leaks the title: %q / %q", gotTitle, gotBody)
		}
	}
}

func TestRequestFailedText(t *testing.T) {
	cases := map[string]string{
		`alice's request for "Dune (2021)" failed in Radarr`:                      "\"Dune (2021)\", requested by alice.\nThe download failed — an admin has the details.",
		`A user's request for "Dune (2021)" failed in Radarr: disk full on nas01`: "\"Dune (2021)\", requested by a user.\nThe download failed — an admin has the details.",
		// Unparseable: only the wording, never the original text.
		`Download of Dune failed: /mnt/secret/path`: "The download failed — an admin has the details.",
		``: "The download failed — an admin has the details.",
	}
	for in, want := range cases {
		if got := requestFailedText(in); got != want {
			t.Errorf("requestFailedText(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestItemIsPrivate_FailsClosedWithoutLookup: a DB that can't look items up
// (the item lookup is optional on DB) treats every item as private.
func TestItemIsPrivate_FailsClosedWithoutLookup(t *testing.T) {
	db := newFakeDB()
	s := New(Options{DB: struct{ DB }{db}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(s.Close)
	if s.items != nil {
		t.Fatal("a DB without the item lookup was used for it")
	}
	if !s.itemIsPrivate(context.Background(), uuid.New()) {
		t.Error("an unverifiable item counted as public")
	}
	s2 := testService(t, db, "")
	pub, priv := uuid.New(), uuid.New()
	db.privateItems[priv] = true
	if s2.itemIsPrivate(context.Background(), pub) || !s2.itemIsPrivate(context.Background(), priv) {
		t.Error("item privacy lookup wrong")
	}
}
