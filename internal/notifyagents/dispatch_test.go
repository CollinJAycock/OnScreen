package notifyagents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/notification"
)

// recordingServer captures every delivery and answers with the scripted
// status codes (then 200).
type recordingServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
	statuses []int
	headers  []http.Header
}

type capturedRequest struct {
	path   string
	header http.Header
	body   string
	at     time.Time
}

func newRecordingServer(t *testing.T, statuses ...int) *recordingServer {
	rs := &recordingServer{statuses: statuses}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		n := len(rs.requests)
		rs.requests = append(rs.requests, capturedRequest{r.URL.Path, r.Header.Clone(), string(b), time.Now()})
		status := http.StatusOK
		if n < len(rs.statuses) {
			status = rs.statuses[n]
		}
		var extra http.Header
		if n < len(rs.headers) {
			extra = rs.headers[n]
		}
		rs.mu.Unlock()
		for k, v := range extra {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: chat not found"}`)
		}
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) got() []capturedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]capturedRequest(nil), rs.requests...)
}

func addAgent(t *testing.T, s *Service, db *fakeDB, kind Kind, secret, cfg string, events ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sealed, err := s.seal(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	row := genAgent(id, kind, sealed, cfg, false)
	if len(events) > 0 {
		row.Events = events
	}
	db.mu.Lock()
	db.agents[id] = row
	db.mu.Unlock()
	return id
}

// TestDispatch_FulfilmentCoalescedWithRequesters: MarkFulfilled notifies each
// requester of a title in turn; the channel gets ONE message naming both,
// with an item link.
func TestDispatch_FulfilmentCoalescedWithRequesters(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	addAgent(t, s, db, KindDiscord, discordURL, `{}`)
	alice, bob := uuid.New(), uuid.New()
	db.users[alice], db.users[bob] = "alice", "bob"
	item := uuid.New()

	for _, u := range []uuid.UUID{alice, bob, alice} {
		u := u
		s.DispatchAgentEvent(context.Background(), notification.AgentEvent{
			Type: notification.TypeRequestAvailable, Title: "Now available: Dune",
			Body: `"Dune" is ready to watch.`, ItemID: &item, UserID: &u, Scope: notification.AgentScopeUser,
		})
	}
	waitUntil(t, "delivery", func() bool { return len(rs.got()) == 1 })
	time.Sleep(100 * time.Millisecond)
	reqs := rs.got()
	if len(reqs) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(reqs))
	}
	if reqs[0].path != "/api/webhooks/123456/sEcReT-webhook-token" {
		t.Errorf("path = %q", reqs[0].path)
	}
	var p discordPayload
	if err := json.Unmarshal([]byte(reqs[0].body), &p); err != nil {
		t.Fatal(err)
	}
	e := p.Embeds[0]
	if e.Description != "\"Dune\" is ready to watch.\nRequested by alice and bob." {
		t.Errorf("description = %q", e.Description)
	}
	if e.URL != "https://media.example.com/watch/"+item.String() || e.Color != discordColors[SeveritySuccess] {
		t.Errorf("embed = %+v", e)
	}
}

// TestDispatch_ScopeMapping: each notice is taken from one fan-out scope
// only — request_failed from the admin copy (not the requester's), request
// approvals from the requester's copy — and types the agents don't announce
// are dropped.
func TestDispatch_ScopeMapping(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"house"}`)
	u := uuid.New()
	send := func(typ string, scope notification.AgentScope, title string) {
		ev := notification.AgentEvent{Type: typ, Title: title, Body: title + " body", Scope: scope}
		if scope == notification.AgentScopeUser {
			ev.UserID = &u
		}
		s.DispatchAgentEvent(context.Background(), ev)
	}
	send(notification.TypeRequestFailed, notification.AgentScopeUser, "user copy")      // dropped
	send(notification.TypeRequestFailed, notification.AgentScopeAdmins, "admin copy")   // kept
	send(notification.TypeRequestApproved, notification.AgentScopeUser, "approved")     // kept
	send(notification.TypeRequestDeclined, notification.AgentScopeUser, "declined")     // not an agent event
	send(notification.TypeNewContent, notification.AgentScopeAll, "per-item new")       // batched path only
	send(notification.TypeIssueReported, notification.AgentScopeAdmins, "issue")        // kept
	send(notification.TypeRequestPending, notification.AgentScopeUser, "pending wrong") // dropped

	waitUntil(t, "three deliveries", func() bool { return len(rs.got()) >= 3 })
	time.Sleep(100 * time.Millisecond)
	var titles []string
	for _, r := range rs.got() {
		titles = append(titles, r.header.Get("Title"))
	}
	if len(titles) != 3 {
		t.Fatalf("delivered %v, want exactly [admin copy approved issue] in some order", titles)
	}
	for _, want := range []string{"admin copy", "approved", "issue"} {
		found := false
		for _, got := range titles {
			found = found || got == want
		}
		if !found {
			t.Errorf("missing %q in %v", want, titles)
		}
	}
}

// TestDispatch_OnlySubscribedEnabledAgents: fan-out honours subscriptions
// and the enabled flag, and a duplicate within the dedupe window is dropped.
func TestDispatch_OnlySubscribedEnabledAgents(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	sub := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"a"}`, EventTaskFailed)
	addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"b"}`, EventBackupFailed)
	off := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"c"}`, EventTaskFailed)
	db.mu.Lock()
	row := db.agents[off]
	row.Enabled = false
	db.agents[off] = row
	db.mu.Unlock()

	m := Message{Event: EventTaskFailed, Title: "Scheduled task failed: x", Body: "boom"}
	s.Emit(m)
	s.Emit(m) // duplicate
	waitUntil(t, "delivery", func() bool { return db.successCount(sub) == 1 })
	time.Sleep(100 * time.Millisecond)
	reqs := rs.got()
	if len(reqs) != 1 || reqs[0].path != "/a" {
		t.Fatalf("deliveries = %+v, want one to topic a", reqs)
	}
}

// TestDelivery_RetriesThenSucceeds: 5xx is retried (three attempts total) and
// success is recorded.
func TestDelivery_RetriesThenSucceeds(t *testing.T) {
	rs := newRecordingServer(t, 500, 502)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	id := addAgent(t, s, db, KindGotify, "AppTok3n", `{"server_url":"https://gotify.example.com"}`)
	s.Emit(Message{Event: EventIssueReported, Title: "Problem reported", Body: "x"})
	waitUntil(t, "success", func() bool { return db.successCount(id) == 1 })
	if n := len(rs.got()); n != 3 {
		t.Errorf("attempts = %d, want 3", n)
	}
	if len(db.failureList(id)) != 0 {
		t.Errorf("failure recorded for an eventually-delivered message: %v", db.failureList(id))
	}
	if got := rs.got()[2].header.Get("X-Gotify-Key"); got != "AppTok3n" {
		t.Errorf("X-Gotify-Key = %q", got)
	}
}

// TestDelivery_PermanentErrorNotRetried_Sanitized: a 4xx stops at one attempt
// and last_error carries the service's explanation but never the Telegram
// bot token (which is in the request path).
func TestDelivery_PermanentErrorNotRetried_Sanitized(t *testing.T) {
	rs := newRecordingServer(t, 400, 400, 400)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	id := addAgent(t, s, db, KindTelegram, testTelegramToken, `{"chat_id":"-1"}`)
	s.Emit(Message{Event: EventRequestPending, Title: "New request", Body: "alice requested"})
	waitUntil(t, "failure recorded", func() bool { return len(db.failureList(id)) == 1 })
	if n := len(rs.got()); n != 1 {
		t.Errorf("attempts = %d, want 1 (4xx is permanent)", n)
	}
	if !strings.HasPrefix(rs.got()[0].path, "/bot"+testTelegramToken+"/sendMessage") {
		t.Errorf("path = %q", rs.got()[0].path)
	}
	msg := db.failureList(id)[0]
	if !strings.Contains(msg, "HTTP 400") || !strings.Contains(msg, "chat not found") {
		t.Errorf("last_error = %q", msg)
	}
	if strings.Contains(msg, testTelegramToken) || strings.Contains(msg, "AAFsecret") {
		t.Errorf("last_error leaks the token: %q", msg)
	}
}

// TestDelivery_TransportErrorSanitized: a connection failure's *url.Error
// quotes the full URL — for Discord that is the secret. It must not reach
// last_error.
func TestDelivery_TransportErrorSanitized(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	s.clientFor = func(netPolicy) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})}
	}
	id := addAgent(t, s, db, KindDiscord, discordURL, `{}`)
	s.Emit(Message{Event: EventWorkerFleetDown, Title: "No transcode worker available"})
	waitUntil(t, "failure recorded", func() bool { return len(db.failureList(id)) == 1 })
	msg := db.failureList(id)[0]
	if strings.Contains(msg, "sEcReT") || strings.Contains(msg, "/api/webhooks") {
		t.Errorf("last_error leaks the webhook URL: %q", msg)
	}
	if !strings.Contains(msg, "discord.com") || !strings.Contains(msg, "connection refused") {
		t.Errorf("last_error = %q", msg)
	}
}

// TestDelivery_RateLimited: past the per-agent budget, messages wait for the
// window instead of being sent (or dropped).
func TestDelivery_RateLimited(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	s.rateWindow = 300 * time.Millisecond
	id := addAgent(t, s, db, KindEmail, "", `{"recipients":["a@example.com"]}`)
	_ = id
	// Email's budget is 10 per window; send 12 distinct messages.
	fe := s.email.(*fakeEmail)
	start := time.Now()
	for i := 0; i < 12; i++ {
		s.Emit(Message{Event: EventRequestPending, Title: "New request", Body: strings.Repeat("x", i+1)})
	}
	waitUntil(t, "12 emails", func() bool {
		fe.mu.Lock()
		defer fe.mu.Unlock()
		return len(fe.sent) == 12
	})
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("12 sends finished in %s; the 11th and 12th should wait for the window", elapsed)
	}
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.sent[0].subject != "[OnScreen] New request" || fe.sent[0].to[0] != "a@example.com" {
		t.Errorf("email = %+v", fe.sent[0])
	}
}

func TestRateLimiter_Reserve(t *testing.T) {
	var l rateLimiter
	base := time.Unix(1000, 0)
	w := time.Minute
	for i := 0; i < 3; i++ {
		if d := l.reserve(base.Add(time.Duration(i)*time.Second), 3, w); d != 0 {
			t.Fatalf("send %d waited %s", i, d)
		}
	}
	// 4th at t+3s must wait until the 1st leaves the window (t+60s).
	if d := l.reserve(base.Add(3*time.Second), 3, w); d != 57*time.Second {
		t.Errorf("4th wait = %s, want 57s", d)
	}
	// 5th, also at t+3s, queues behind the 2nd (t+1s+60s).
	if d := l.reserve(base.Add(3*time.Second), 3, w); d != 58*time.Second {
		t.Errorf("5th wait = %s, want 58s", d)
	}
	// Long after, the window is clear again.
	if d := l.reserve(base.Add(10*time.Minute), 3, w); d != 0 {
		t.Errorf("later wait = %s, want 0", d)
	}
}

// TestDelivery_RetryAfterHonoured: a 429 waits at least Retry-After before
// the next attempt.
func TestDelivery_RetryAfterHonoured(t *testing.T) {
	rs := newRecordingServer(t, 429)
	rs.headers = []http.Header{{"Retry-After": []string{"1"}}}
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	id := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"t"}`)
	s.Emit(Message{Event: EventBackupFailed, Title: "Backup failed"})
	waitUntil(t, "success", func() bool { return db.successCount(id) == 1 })
	reqs := rs.got()
	if len(reqs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(reqs))
	}
	if gap := reqs[1].at.Sub(reqs[0].at); gap < 900*time.Millisecond {
		t.Errorf("retried after %s, want >= Retry-After 1s", gap)
	}
}

// TestDelivery_DisabledOrDeletedWhileQueuedIsDropped: the worker re-reads the
// agent before sending.
func TestDelivery_DisabledOrDeletedWhileQueuedIsDropped(t *testing.T) {
	rs := newRecordingServer(t, 500)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	s.retryDelays = []time.Duration{0, 200 * time.Millisecond}
	id := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"t"}`)
	s.Emit(Message{Event: EventTaskFailed, Title: "fail"})
	waitUntil(t, "first attempt", func() bool { return len(rs.got()) == 1 })
	if err := s.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if n := len(rs.got()); n != 1 {
		t.Errorf("attempts after delete = %d, want 1", n)
	}
}

// TestSendTest: synchronous, works on a disabled agent, records the outcome,
// reports failures as a sanitized DeliveryError.
func TestSendTest(t *testing.T) {
	rs := newRecordingServer(t, 200, 404)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	id := addAgent(t, s, db, KindDiscord, discordURL, `{}`, EventRequestPending)
	db.mu.Lock()
	row := db.agents[id]
	row.Enabled = false
	db.agents[id] = row
	db.mu.Unlock()

	if err := s.SendTest(context.Background(), id); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if db.successCount(id) != 1 || len(rs.got()) != 1 {
		t.Fatalf("success=%d deliveries=%d", db.successCount(id), len(rs.got()))
	}
	var p discordPayload
	_ = json.Unmarshal([]byte(rs.got()[0].body), &p)
	if p.Embeds[0].Title != "OnScreen test notification" || p.Embeds[0].URL != "https://media.example.com/" {
		t.Errorf("test embed = %+v", p.Embeds[0])
	}

	err := s.SendTest(context.Background(), id)
	var derr *DeliveryError
	if !errors.As(err, &derr) || !strings.Contains(derr.Msg, "HTTP 404") || strings.Contains(derr.Msg, "sEcReT") {
		t.Fatalf("second SendTest = %v", err)
	}
	if len(db.failureList(id)) != 1 {
		t.Error("failed test not recorded")
	}
	if err := s.SendTest(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("SendTest unknown = %v", err)
	}
}

func TestSendTest_EmailNotConfigured(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	s.email = &fakeEmail{err: email.ErrNotConfigured}
	id := addAgent(t, s, db, KindEmail, "", `{"recipients":["a@example.com"]}`)
	err := s.SendTest(context.Background(), id)
	var derr *DeliveryError
	if !errors.As(err, &derr) || !strings.Contains(derr.Msg, "SMTP is not configured") {
		t.Fatalf("err = %v", err)
	}
}

// TestNoBaseURL_NoLinks: without a configured public URL, messages carry no
// link at all (rather than a useless localhost one).
func TestNoBaseURL_NoLinks(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	s.baseURL = func(context.Context) string { return "" }
	id := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"t"}`)
	if err := s.SendTest(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if c := rs.got()[0].header.Get("Click"); c != "" {
		t.Errorf("Click = %q, want none", c)
	}
	s.baseURL = func(context.Context) string { return "javascript:alert(1)" }
	if got := s.link(context.Background(), "/requests"); got != "" {
		t.Errorf("link with a non-http base = %q", got)
	}
}

// TestClose_StopsWorkers: Close returns promptly even with a message in a
// long backoff.
func TestClose_StopsWorkers(t *testing.T) {
	rs := newRecordingServer(t, 500, 500, 500)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	s.retryDelays = []time.Duration{0, time.Hour}
	addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"t"}`)
	s.Emit(Message{Event: EventTaskFailed, Title: "fail"})
	waitUntil(t, "first attempt", func() bool { return len(rs.got()) == 1 })
	done := make(chan struct{})
	var closed atomic.Bool
	go func() { s.Close(); closed.Store(true); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked on a retry backoff")
	}
}

// TestWorker_IdleExitAndRestart: an idle agent worker exits and a later
// message starts a fresh one.
func TestWorker_IdleExitAndRestart(t *testing.T) {
	rs := newRecordingServer(t)
	db := newFakeDB()
	s := testService(t, db, rs.URL)
	s.idleTimeout = 30 * time.Millisecond
	id := addAgent(t, s, db, KindNtfy, "", `{"server_url":"https://ntfy.example.com","topic":"t"}`)
	s.Emit(Message{Event: EventTaskFailed, Title: "one"})
	waitUntil(t, "first delivery", func() bool { return db.successCount(id) == 1 })
	waitUntil(t, "worker exit", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.workers) == 0
	})
	s.Emit(Message{Event: EventTaskFailed, Title: "two"})
	waitUntil(t, "second delivery", func() bool { return db.successCount(id) == 2 })
}
