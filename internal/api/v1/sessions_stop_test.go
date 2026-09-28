package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type mapSessionUsers map[uuid.UUID]gen.User

func (m mapSessionUsers) GetUser(_ context.Context, id uuid.UUID) (gen.User, error) {
	if u, ok := m[id]; ok {
		return u, nil
	}
	return gen.User{}, fmt.Errorf("no user %s", id)
}

type mapSessionFiles map[uuid.UUID]media.File

func (m mapSessionFiles) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	if f, ok := m[id]; ok {
		return &f, nil
	}
	return nil, media.ErrNotFound
}

func (m mapSessionFiles) GetFiles(_ context.Context, itemID uuid.UUID) ([]media.File, error) {
	var out []media.File
	for _, f := range m {
		if f.MediaItemID == itemID {
			out = append(out, f)
		}
	}
	return out, nil
}

type recordingTerminator struct{ stopped []string }

func (r *recordingTerminator) TerminateSession(_ context.Context, sess *transcode.Session) {
	r.stopped = append(r.stopped, sess.ID)
}

// ── harness ──────────────────────────────────────────────────────────────────

type stopHarness struct {
	h          *NativeSessionsHandler
	store      *transcode.SessionStore
	tracker    *streaming.Tracker
	guard      *PlaybackStopGuard
	terminator *recordingTerminator
	broker     *notification.Broker
	audits     chan gen.InsertAuditLogParams
	items      *mapSessionItems
	users      mapSessionUsers
	files      mapSessionFiles
}

func newStopHarness(t *testing.T) *stopHarness {
	t.Helper()
	v := testvalkey.New(t)
	hs := &stopHarness{
		store:      transcode.NewSessionStore(v),
		tracker:    streaming.NewTracker(),
		guard:      NewPlaybackStopGuard(nil, slog.Default()),
		terminator: &recordingTerminator{},
		broker:     notification.NewBroker(),
		audits:     make(chan gen.InsertAuditLogParams, 4),
		items:      &mapSessionItems{byID: map[uuid.UUID]gen.SessionMediaItem{}},
		users:      mapSessionUsers{},
		files:      mapSessionFiles{},
	}
	hs.h = NewNativeSessionsHandler(hs.store, hs.tracker, hs.items, slog.Default()).
		WithUsers(hs.users).
		WithFiles(hs.files).
		WithPlaybackStops(hs.guard).
		WithTerminator(hs.terminator).
		WithSyncBroker(hs.broker).
		WithAudit(audit.New(&auditCapture{ch: hs.audits}, slog.Default()))
	return hs
}

func (hs *stopHarness) addItem(title string) uuid.UUID {
	id := uuid.New()
	hs.items.byID[id] = gen.SessionMediaItem{ID: id, Title: title, Type: "movie"}
	return id
}

func (hs *stopHarness) list(t *testing.T, claims *auth.Claims) []activeSession {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	hs.h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []activeSession `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return body.Data
}

func (hs *stopHarness) stop(t *testing.T, claims *auth.Claims, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/x/stop", strings.NewReader(body))
	req = withChiParam(req, "id", id)
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	hs.h.Stop(rec, req)
	return rec
}

func stopAdminClaims() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Username: "admin", IsAdmin: true}
}

func nextEvent(t *testing.T, ch chan notification.Event) notification.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no SSE event published")
	}
	return notification.Event{}
}

func nextAudit(t *testing.T, ch chan gen.InsertAuditLogParams) (gen.InsertAuditLogParams, map[string]any) {
	t.Helper()
	select {
	case p := <-ch:
		var detail map[string]any
		_ = json.Unmarshal(p.Detail, &detail)
		return p, detail
	case <-time.After(2 * time.Second):
		t.Fatal("stop was not audited")
	}
	return gen.InsertAuditLogParams{}, nil
}

// ── List: who / where / why ──────────────────────────────────────────────────

// A direct play surfaces as a heartbeat AND a byte-traffic entry; they merge
// into one card that names the viewer (owner · profile for a managed
// profile), the player's own device name, LAN vs remote, and the source.
func TestSessions_List_DirectPlayAttribution(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Dune")
	owner, profile := uuid.New(), uuid.New()
	hs.users[owner] = gen.User{ID: owner, Username: "collin"}
	hs.users[profile] = gen.User{ID: profile, Username: "kids", ParentUserID: pgtype.UUID{Bytes: owner, Valid: true}}
	fileID := uuid.New()
	w, hh := 3840, 2160
	br := int64(25_000_000)
	hs.files[fileID] = media.File{
		ID: fileID, MediaItemID: item, Container: strPtr("matroska"),
		VideoCodec: strPtr("hevc"), AudioCodec: strPtr("eac3"),
		ResolutionW: &w, ResolutionH: &hh, Bitrate: &br,
		AudioStreams: []byte(`[{"channels":6}]`),
	}
	hs.tracker.HeartbeatUser(profile, "192.168.1.20", item, "Web — Chrome on Windows", "")
	hs.tracker.TouchFile(profile, "192.168.1.20", item, fileID, "/media/dune.mkv", "Mozilla")

	cards := hs.list(t, stopAdminClaims())
	if len(cards) != 1 {
		t.Fatalf("want 1 merged card, got %d: %+v", len(cards), cards)
	}
	c := cards[0]
	if c.ID != "192.168.1.20|"+item.String()+"|"+profile.String() {
		t.Errorf("id: got %q", c.ID)
	}
	if c.Decision != "directPlay" || c.ClientName != "Web — Chrome on Windows" {
		t.Errorf("decision/client: got %q / %q", c.Decision, c.ClientName)
	}
	if c.UserID != profile.String() || c.Username != "collin" || c.ProfileName != "kids" {
		t.Errorf("viewer: got id=%q user=%q profile=%q", c.UserID, c.Username, c.ProfileName)
	}
	if c.ClientIP != "192.168.1.20" || c.Location != "lan" {
		t.Errorf("address: got ip=%q location=%q", c.ClientIP, c.Location)
	}
	if c.Source == nil || c.Source.VideoCodec != "hevc" || c.Source.Width != 3840 || c.Source.BitrateKbps != 25000 || c.Source.AudioChannels != 6 {
		t.Errorf("source: got %+v", c.Source)
	}
	if c.Output != nil {
		t.Errorf("a direct play has no separate output, got %+v", c.Output)
	}
	if !c.CanStop {
		t.Error("an attributed direct play must be stoppable")
	}

	// The viewer themselves sees their own card, minus the raw address.
	own := hs.list(t, &auth.Claims{UserID: profile})
	if len(own) != 1 || own[0].ClientIP != "" || own[0].Location != "lan" {
		t.Errorf("own view: got %+v", own)
	}
	// Anyone else sees nothing.
	if other := hs.list(t, &auth.Claims{UserID: uuid.New()}); len(other) != 0 {
		t.Errorf("another user saw %d cards", len(other))
	}
}

// A server session's card carries the transcode reasons, the output shape,
// and borrows the player's self-reported device name + address from its
// heartbeat over the generic "OnScreenWeb" placeholder.
func TestSessions_List_ServerSessionAttribution(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Arrival")
	user := uuid.New()
	hs.users[user] = gen.User{ID: user, Username: "sam"}
	fileID := uuid.New()
	hs.files[fileID] = media.File{ID: fileID, MediaItemID: item, VideoCodec: strPtr("hevc"), AudioCodec: strPtr("truehd")}
	now := time.Now()
	sess := transcode.Session{
		ID: transcode.NewSessionID(), UserID: user, MediaItemID: item, FileID: fileID,
		Decision: "transcode", FilePath: "/media/arrival.mkv", CreatedAt: now, LastActivityAt: now,
		ClientName: genericSessionClientName, BitrateKbps: 4000,
		TranscodeReasons:    []string{"HEVC not supported by client", "TrueHD audio not supported by client"},
		OutputWidth:         1280,
		OutputHeight:        720,
		OutputAudioCodec:    "aac",
		OutputAudioChannels: 2,
	}
	if err := hs.store.Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	hs.tracker.HeartbeatUser(user, "203.0.113.9", item, "Living Room TV", "transcode")

	cards := hs.list(t, stopAdminClaims())
	if len(cards) != 1 {
		t.Fatalf("want 1 card (heartbeat folded into the session), got %d", len(cards))
	}
	c := cards[0]
	if c.ClientName != "Living Room TV" || c.ClientIP != "203.0.113.9" || c.Location != "remote" {
		t.Errorf("client: got name=%q ip=%q loc=%q", c.ClientName, c.ClientIP, c.Location)
	}
	if len(c.TranscodeReasons) != 2 || c.TranscodeReasons[0] != "HEVC not supported by client" {
		t.Errorf("reasons: got %v", c.TranscodeReasons)
	}
	want := streamFormat{Container: "hls", VideoCodec: "h264", AudioCodec: "aac", Width: 1280, Height: 720, BitrateKbps: 4000, AudioChannels: 2}
	if c.Output == nil || *c.Output != want {
		t.Errorf("output: got %+v, want %+v", c.Output, want)
	}
	if c.Source == nil || c.Source.VideoCodec != "hevc" || c.Source.AudioCodec != "truehd" {
		t.Errorf("source: got %+v", c.Source)
	}
	if c.Username != "sam" || !c.CanStop {
		t.Errorf("user/can_stop: got %q / %v", c.Username, c.CanStop)
	}
}

// ── Stop: every session kind ─────────────────────────────────────────────────

// Transcode: today's teardown, plus the SSE message; no refusal window.
func TestSessionsStop_Transcode(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Movie")
	user := uuid.New()
	now := time.Now()
	sess := transcode.Session{
		ID: transcode.NewSessionID(), UserID: user, MediaItemID: item, FileID: uuid.New(),
		Decision: "transcode", CreatedAt: now, LastActivityAt: now, ClientIP: "192.168.1.30",
	}
	if err := hs.store.Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	events := hs.broker.Subscribe(user)
	defer hs.broker.Unsubscribe(user, events)

	admin := stopAdminClaims()
	rec := hs.stop(t, admin, sess.ID, `{"message":"Server maintenance at 9pm"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(hs.terminator.stopped) != 1 || hs.terminator.stopped[0] != sess.ID {
		t.Errorf("terminated %v, want [%s]", hs.terminator.stopped, sess.ID)
	}
	if _, blocked := hs.guard.Blocked(context.Background(), user, item, "192.168.1.30"); blocked {
		t.Error("a transcode stop keeps today's behaviour — no refusal window")
	}

	ev := nextEvent(t, events)
	if ev.Type != PlaybackStopEventType || ev.ItemID == nil || *ev.ItemID != item.String() {
		t.Errorf("event: got %+v", ev)
	}
	var p PlaybackStopPayload
	_ = json.Unmarshal(ev.Data, &p)
	if p.SessionID != sess.ID || p.Message != "Server maintenance at 9pm" || p.Decision != "transcode" {
		t.Errorf("payload: got %+v", p)
	}

	a, detail := nextAudit(t, hs.audits)
	if a.Action != audit.ActionTranscodeStop || a.Target == nil || *a.Target != sess.ID {
		t.Errorf("audit: action=%q target=%v", a.Action, a.Target)
	}
	if !a.UserID.Valid || uuid.UUID(a.UserID.Bytes) != admin.UserID {
		t.Errorf("audit actor: got %v, want the admin", a.UserID)
	}
	if detail["reason"] != "admin_terminated" || detail["owner_id"] != user.String() || detail["message"] != "Server maintenance at 9pm" {
		t.Errorf("audit detail: got %v", detail)
	}
}

// Remux: torn down AND refused for the window, scoped to (user, item, IP).
func TestSessionsStop_Remux(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Movie")
	user := uuid.New()
	now := time.Now()
	sess := transcode.Session{
		ID: transcode.NewSessionID(), UserID: user, MediaItemID: item, FileID: uuid.New(),
		Decision: "remux", CreatedAt: now, LastActivityAt: now, ClientIP: "192.168.1.31",
	}
	if err := hs.store.Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}

	if rec := hs.stop(t, stopAdminClaims(), sess.ID, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(hs.terminator.stopped) != 1 {
		t.Errorf("remux session not torn down: %v", hs.terminator.stopped)
	}
	ctx := context.Background()
	if _, blocked := hs.guard.Blocked(ctx, user, item, "192.168.1.31"); !blocked {
		t.Error("stopped remux client must be refused for the window")
	}
	if _, blocked := hs.guard.Blocked(ctx, user, item, "192.168.1.32"); blocked {
		t.Error("the same user's other device must keep working")
	}
	if _, blocked := hs.guard.Blocked(ctx, uuid.New(), item, "192.168.1.31"); blocked {
		t.Error("another user on the same address must keep working")
	}
	if _, blocked := hs.guard.Blocked(ctx, user, uuid.New(), "192.168.1.31"); blocked {
		t.Error("the same device may play another title")
	}
}

// Direct play: no server state to kill — SSE + refusal window + the card
// leaves Now Playing at once.
func TestSessionsStop_DirectPlay(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Movie")
	user := uuid.New()
	hs.tracker.HeartbeatUser(user, "10.0.0.40", item, "Web — Firefox on Linux", "directPlay")
	hs.tracker.TouchFile(user, "10.0.0.40", item, uuid.New(), "/media/m.mkv", "Mozilla")
	// Same user, second device: must survive the stop.
	hs.tracker.HeartbeatUser(user, "10.0.0.41", item, "Bedroom TV", "directPlay")
	events := hs.broker.Subscribe(user)
	defer hs.broker.Unsubscribe(user, events)

	admin := stopAdminClaims()
	cards := hs.list(t, admin)
	if len(cards) != 2 {
		t.Fatalf("want 2 cards, got %d", len(cards))
	}
	id := "10.0.0.40|" + item.String() + "|" + user.String()

	rec := hs.stop(t, admin, id, `{"message":"  bedtime\n"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(hs.terminator.stopped) != 0 {
		t.Errorf("a direct play has no server session to terminate: %v", hs.terminator.stopped)
	}
	msg, blocked := hs.guard.Blocked(context.Background(), user, item, "10.0.0.40")
	if !blocked || msg != "bedtime" {
		t.Errorf("block: got blocked=%v msg=%q", blocked, msg)
	}
	after := hs.list(t, admin)
	if len(after) != 1 || after[0].ClientName != "Bedroom TV" {
		t.Errorf("after stop: got %+v, want only the other device", after)
	}

	ev := nextEvent(t, events)
	var p PlaybackStopPayload
	_ = json.Unmarshal(ev.Data, &p)
	if p.SessionID != "" || p.ClientName != "Web — Firefox on Linux" || p.Message != "bedtime" || p.ItemID != item.String() {
		t.Errorf("payload: got %+v", p)
	}

	a, detail := nextAudit(t, hs.audits)
	if a.Target == nil || *a.Target != id {
		t.Errorf("audit target: got %v, want %s", a.Target, id)
	}
	if detail["decision"] != "directPlay" || detail["client_ip"] != "10.0.0.40" || detail["blocked_until"] == nil {
		t.Errorf("audit detail: got %v", detail)
	}
}

// An IPv6 direct-play viewer's card id is "::1|item|user". The web client
// percent-encodes the segment (encodeURIComponent → %3A%3A1%7C…); an escape Go
// wouldn't produce itself makes the URL carry a RawPath, chi routes on it
// without unescaping, and the param used to arrive encoded and 404. Driven
// through a real chi router so the RawPath routing is what's under test; the
// unencoded IPv4 form keeps working through the same route.
func TestSessionsStop_IPv6CardIDThroughRouter(t *testing.T) {
	hs := newStopHarness(t)
	admin := stopAdminClaims()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(middleware.WithClaims(r.Context(), admin)))
		})
	})
	router.Route("/api/v1", func(r chi.Router) {
		r.Post("/sessions/{id}/stop", hs.h.Stop)
	})
	post := func(rawID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+rawID+"/stop", strings.NewReader(`{"message":"bye"}`))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	item := hs.addItem("Movie")
	user := uuid.New()
	hs.tracker.HeartbeatUser(user, "::1", item, "Web — Chrome on Linux", "directPlay")
	hs.tracker.TouchFile(user, "::1", item, uuid.New(), "/media/m.mkv", "Mozilla")
	id := "::1|" + item.String() + "|" + user.String()
	if cards := hs.list(t, admin); len(cards) != 1 || cards[0].ID != id {
		t.Fatalf("cards: got %+v, want one with id %q", cards, id)
	}

	encoded := encodeURIComponent(id)
	if !strings.HasPrefix(encoded, "%3A%3A1%7C") {
		t.Fatalf("encoding: got %q", encoded)
	}
	rec := post(encoded)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("encoded IPv6 id: got %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if msg, blocked := hs.guard.Blocked(context.Background(), user, item, "::1"); !blocked || msg != "bye" {
		t.Errorf("block: got blocked=%v msg=%q", blocked, msg)
	}
	if cards := hs.list(t, admin); len(cards) != 0 {
		t.Errorf("stopped card still listed: %+v", cards)
	}

	// An IPv4 id, sent unencoded, still matches.
	item4 := hs.addItem("Other")
	hs.tracker.HeartbeatUser(user, "10.0.0.7", item4, "Bedroom TV", "directPlay")
	if rec := post("10.0.0.7|" + item4.String() + "|" + user.String()); rec.Code != http.StatusNoContent {
		t.Errorf("unencoded IPv4 id: got %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
}

// encodeURIComponent mirrors the web client's encoding of a path segment.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func TestSessionsStop_UnknownIDIs404(t *testing.T) {
	hs := newStopHarness(t)
	for _, id := range []string{"nope", transcode.NewSessionID(), "10.0.0.1|" + uuid.New().String()} {
		if rec := hs.stop(t, stopAdminClaims(), id, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("id %q: got %d, want 404", id, rec.Code)
		}
	}
}

func TestSessionsStop_AdminOnly(t *testing.T) {
	hs := newStopHarness(t)
	if rec := hs.stop(t, &auth.Claims{UserID: uuid.New()}, "x", `{}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: got %d, want 403", rec.Code)
	}
	req := withChiParam(httptest.NewRequest(http.MethodPost, "/", nil), "id", "x")
	rec := httptest.NewRecorder()
	hs.h.Stop(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", rec.Code)
	}
}

func TestSessionsStop_MessageValidation(t *testing.T) {
	hs := newStopHarness(t)
	long := strings.Repeat("é", playbackStopMessageMax+1)
	if rec := hs.stop(t, stopAdminClaims(), "x", `{"message":"`+long+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("201-char message: got %d, want 400", rec.Code)
	}
	if rec := hs.stop(t, stopAdminClaims(), "x", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: got %d, want 400", rec.Code)
	}
	// Exactly the cap (in characters, not bytes) is fine — reaches the lookup.
	exact := strings.Repeat("é", playbackStopMessageMax)
	if rec := hs.stop(t, stopAdminClaims(), "x", `{"message":"`+exact+`"}`); rec.Code != http.StatusNotFound {
		t.Errorf("200-char message: got %d, want 404 (valid body, unknown id)", rec.Code)
	}
}

// A heartbeat recorded without a user (pre-upgrade) can't be scoped to one
// viewer, so the card says can_stop=false and the endpoint refuses with 409.
func TestSessionsStop_UnattributedLegacyEntryIs409(t *testing.T) {
	hs := newStopHarness(t)
	item := hs.addItem("Legacy")
	hs.tracker.Heartbeat("10.0.0.9", item, "Chrome")
	cards := hs.list(t, stopAdminClaims())
	if len(cards) != 1 || cards[0].CanStop {
		t.Fatalf("legacy card: got %+v, want one with can_stop=false", cards)
	}
	if rec := hs.stop(t, stopAdminClaims(), cards[0].ID, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("got %d, want 409", rec.Code)
	}
}

func TestCleanStopMessage(t *testing.T) {
	got, err := cleanStopMessage("  line one\r\nline\ttwo\x1b[31m  ")
	if err != nil || got != "line one  line two [31m" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := cleanStopMessage(strings.Repeat("a", 201)); err == nil {
		t.Error("201 characters must be rejected")
	}
}

// ── Guard: scope + expiry ────────────────────────────────────────────────────

func TestPlaybackStopGuard_ScopeAndExpiry(t *testing.T) {
	now := time.Now()
	g := NewPlaybackStopGuard(nil, slog.Default())
	g.now = func() time.Time { return now }
	ctx := context.Background()
	user, item := uuid.New(), uuid.New()

	until := g.Block(ctx, user, item, "::ffff:10.0.0.5", "bye")
	if !until.Equal(now.Add(playbackStopWindow)) {
		t.Errorf("until: got %v", until)
	}
	// The IPv4-mapped form and the plain form are the same client.
	if msg, ok := g.Blocked(ctx, user, item, "10.0.0.5"); !ok || msg != "bye" {
		t.Errorf("same client: got %q %v", msg, ok)
	}
	if _, ok := g.Blocked(ctx, user, item, "10.0.0.6"); ok {
		t.Error("other device blocked")
	}
	now = now.Add(playbackStopWindow + time.Second)
	if _, ok := g.Blocked(ctx, user, item, "10.0.0.5"); ok {
		t.Error("block outlived its window")
	}

	var nilGuard *PlaybackStopGuard
	if _, ok := nilGuard.Blocked(ctx, user, item, "10.0.0.5"); ok {
		t.Error("an unwired guard must block nothing")
	}
	nilGuard.Block(ctx, user, item, "10.0.0.5", "") // must not panic
}

// Valkey-backed: a block recorded by one API instance is enforced by another,
// and expires with the key's TTL.
func TestPlaybackStopGuard_SharedThroughValkey(t *testing.T) {
	mr := miniredis.RunT(t)
	v, err := valkey.New(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	ctx := context.Background()
	user, item := uuid.New(), uuid.New()

	NewPlaybackStopGuard(v, slog.Default()).Block(ctx, user, item, "10.0.0.5", "")
	other := NewPlaybackStopGuard(v, slog.Default())
	if msg, ok := other.Blocked(ctx, user, item, "10.0.0.5"); !ok || msg != "" {
		t.Fatalf("second instance: got %q %v, want blocked with no message", msg, ok)
	}
	mr.FastForward(playbackStopWindow + time.Second)
	if _, ok := other.Blocked(ctx, user, item, "10.0.0.5"); ok {
		t.Error("block outlived its Valkey TTL")
	}
}
