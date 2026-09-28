package scrobble

import (
	"context"
	"crypto/md5" //nolint:gosec // Last.fm's signature scheme
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// memStore is an in-memory SettingsStore. gets counts loads, so tests can
// check that heartbeats don't reach the database.
type memStore struct {
	mu   sync.Mutex
	st   Settings
	err  error
	gets int
}

func (m *memStore) Get(context.Context, uuid.UUID) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	return m.st, m.err
}

func (m *memStore) SetListenBrainz(_ context.Context, _ uuid.UUID, token string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.ListenBrainzToken, m.st.ListenBrainzEnabled = token, enabled && token != ""
	return nil
}

func (m *memStore) SetLastFM(_ context.Context, _ uuid.UUID, key, username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.LastFMSessionKey, m.st.LastFMUsername = key, username
	return nil
}

func (m *memStore) SetTrakt(_ context.Context, _ uuid.UUID, link TraktLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Trakt = link
	return nil
}

func (m *memStore) snapshot() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

type fakeMedia struct {
	track   Track
	trackOK bool
	video   Video
	videoOK bool
	err     error
}

func (f fakeMedia) Track(context.Context, uuid.UUID) (Track, bool, error) {
	return f.track, f.trackOK, f.err
}

func (f fakeMedia) Video(context.Context, uuid.UUID) (Video, bool, error) {
	return f.video, f.videoOK, f.err
}

type fakeApps struct {
	lastfm LastFMApp
	trakt  TraktApp
}

func (f fakeApps) LastFM(context.Context) LastFMApp { return f.lastfm }
func (f fakeApps) Trakt(context.Context) TraktApp   { return f.trakt }

var (
	testLastFM = LastFMApp{APIKey: "lfm-key", SharedSecret: "lfm-secret"}
	testTrakt  = TraktApp{ClientID: "trakt-id", ClientSecret: "trakt-secret"}
	bothApps   = fakeApps{lastfm: testLastFM, trakt: testTrakt}
)

// req is one request the fake services received.
type req struct {
	path   string
	header http.Header
	form   url.Values     // Last.fm
	body   map[string]any // ListenBrainz / Trakt JSON
}

// services is one httptest server standing in for ListenBrainz (/lb),
// Last.fm (/lastfm/, routed by its method parameter) and Trakt (/trakt/...).
// Every request is recorded; routes answer with defaults unless a test sets
// a handler in on.
type services struct {
	mu   sync.Mutex
	reqs []req
	on   map[string]func(w http.ResponseWriter, r req)
}

func (s *services) handle(key string, fn func(w http.ResponseWriter, r req)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on[key] = fn
}

// calls returns the recorded requests to a route key ("lb", a Last.fm
// method, or a Trakt path).
func (s *services) calls(key string) []req {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []req
	for _, r := range s.reqs {
		if routeKey(r) == key {
			out = append(out, r)
		}
	}
	return out
}

func routeKey(r req) string {
	switch {
	case r.path == "/lb":
		return "lb"
	case r.path == "/lastfm/":
		return r.form.Get("method")
	default:
		return strings.TrimPrefix(r.path, "/trakt")
	}
}

func (s *services) ServeHTTP(w http.ResponseWriter, hr *http.Request) {
	raw, _ := io.ReadAll(hr.Body)
	r := req{path: hr.URL.Path, header: hr.Header.Clone()}
	if strings.HasPrefix(hr.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		r.form, _ = url.ParseQuery(string(raw))
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r.body)
	}
	key := routeKey(r)
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	fn := s.on[key]
	s.mu.Unlock()
	if fn != nil {
		fn(w, r)
		return
	}
	switch key {
	case "lb", "/oauth/revoke":
		w.WriteHeader(http.StatusOK)
	case "auth.getToken":
		_, _ = io.WriteString(w, `{"token":"req-token"}`)
	case "auth.getSession":
		_, _ = io.WriteString(w, `{"session":{"name":"lfm-user","key":"sk-1","subscriber":0}}`)
	case "track.updateNowPlaying":
		_, _ = io.WriteString(w, `{"nowplaying":{}}`)
	case "track.scrobble":
		_, _ = io.WriteString(w, `{"scrobbles":{"@attr":{"accepted":1,"ignored":0}}}`)
	case "/oauth/device/code":
		_, _ = io.WriteString(w, `{"device_code":"dev-code","user_code":"ABCD1234","verification_url":"https://trakt.tv/activate","expires_in":600,"interval":5}`)
	case "/oauth/device/token", "/oauth/token":
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":86400,"created_at":1700000000}`)
	case "/users/settings":
		_, _ = io.WriteString(w, `{"user":{"username":"trakt-user"}}`)
	default:
		if strings.HasPrefix(key, "/scrobble/") {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

// harness is a Service wired to the fake services, with a settable clock.
type harness struct {
	svc   *Service
	store *memStore
	srv   *services
	clock time.Time
	user  uuid.UUID
	item  uuid.UUID
}

func newHarness(t *testing.T, st Settings, media fakeMedia, apps fakeApps) *harness {
	t.Helper()
	enc, err := auth.NewEncryptor(auth.DeriveKey32(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	h := &harness{
		store: &memStore{st: st},
		srv:   &services{on: map[string]func(http.ResponseWriter, req){}},
		clock: time.Unix(1_700_000_000, 0),
		user:  uuid.New(),
		item:  uuid.New(),
	}
	ts := httptest.NewServer(h.srv)
	t.Cleanup(ts.Close)
	h.svc = NewService(h.store, media, apps, enc, ts.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.svc.submitURL = ts.URL + "/lb"
	h.svc.lastFMURL = ts.URL + "/lastfm/"
	h.svc.traktURL = ts.URL + "/trakt"
	h.svc.now = func() time.Time { return h.clock }
	return h
}

func (h *harness) play(event string, positionMS int64, durationMS *int64) {
	h.svc.OnPlayback(context.Background(), event, h.user, h.item, positionMS, durationMS, h.clock)
}

func dur(ms int64) *int64 { return &ms }

var zeppelin = Track{
	TrackName: "Black Dog", ArtistName: "Led Zeppelin", ReleaseName: "IV", TrackNumber: 1,
	DurationMS:    295_000,
	RecordingMBID: "11111111-1111-1111-1111-111111111111",
}

// ── ListenBrainz ────────────────────────────────────────────────────────────

var lbOn = Settings{ListenBrainzToken: "tok123", ListenBrainzEnabled: true}

func TestStop_SubmitsListenBrainzListen(t *testing.T) {
	h := newHarness(t, lbOn, fakeMedia{trackOK: true, track: zeppelin}, fakeApps{})

	// Played 200s of a 300s track — well past the 50% threshold.
	h.play("stop", 200_000, dur(300_000))

	calls := h.srv.calls("lb")
	if len(calls) != 1 {
		t.Fatalf("expected 1 submit, got %d", len(calls))
	}
	if got := calls[0].header.Get("Authorization"); got != "Token tok123" {
		t.Errorf("auth header: got %q, want %q", got, "Token tok123")
	}
	body := calls[0].body
	if body["listen_type"] != "single" {
		t.Errorf("listen_type: got %v", body["listen_type"])
	}
	payload, _ := body["payload"].([]any)
	if len(payload) != 1 {
		t.Fatalf("payload length: got %d, want 1", len(payload))
	}
	listen, _ := payload[0].(map[string]any)
	if listen["listened_at"].(float64) != float64(h.clock.Unix()) {
		t.Errorf("listened_at: got %v, want %d", listen["listened_at"], h.clock.Unix())
	}
	meta, _ := listen["track_metadata"].(map[string]any)
	if meta["artist_name"] != "Led Zeppelin" || meta["track_name"] != "Black Dog" || meta["release_name"] != "IV" {
		t.Errorf("track_metadata wrong: %+v", meta)
	}
}

// A skip / brief preview (well under both 50% and 4 minutes) emits a 'stop'
// but must NOT scrobble.
func TestStop_SkipsBelowThreshold(t *testing.T) {
	h := newHarness(t, lbOn, fakeMedia{trackOK: true, track: zeppelin}, fakeApps{})
	h.play("stop", 10_000, dur(300_000))
	if n := len(h.srv.calls("lb")); n != 0 {
		t.Errorf("below-threshold play must not submit, got %d", n)
	}
}

func TestStop_SkipsWhenDisabled(t *testing.T) {
	h := newHarness(t, Settings{ListenBrainzToken: "tok"}, fakeMedia{trackOK: true, track: zeppelin}, fakeApps{})
	h.play("stop", 200_000, dur(300_000))
	if n := len(h.srv.calls("lb")); n != 0 {
		t.Errorf("disabled user must not submit, got %d", n)
	}
}

func TestStop_SkipsNonMusic(t *testing.T) {
	h := newHarness(t, lbOn, fakeMedia{}, fakeApps{})
	h.play("stop", 200_000, dur(300_000))
	if n := len(h.srv.calls("lb")); n != 0 {
		t.Errorf("non-music play must not submit, got %d", n)
	}
}

func TestStop_SkipsMissingRequiredFields(t *testing.T) {
	// A music track with no resolvable artist — every service requires one.
	h := newHarness(t, lbOn, fakeMedia{trackOK: true, track: Track{TrackName: "Untitled"}}, fakeApps{})
	h.play("stop", 200_000, dur(300_000))
	if n := len(h.srv.calls("lb")); n != 0 {
		t.Errorf("missing artist must not submit, got %d", n)
	}
}

// listenedEnough is the listen-threshold gate: ≥50% of a known duration, or
// ≥4 minutes regardless. An unknown/zero duration falls back to the 4-minute
// rule alone.
func TestListenedEnough(t *testing.T) {
	tests := []struct {
		name       string
		positionMS int64
		durationMS *int64
		want       bool
	}{
		{"exactly half of a known track", 150_000, dur(300_000), true},
		{"just past half", 150_001, dur(300_000), true},
		{"just under half, under 4min", 149_999, dur(300_000), false},
		{"early skip", 10_000, dur(300_000), false},
		{"four-minute rule fires below 50%", 240_000, dur(600_000), true},
		{"one ms under four minutes, below 50%", 239_999, dur(600_000), false},
		{"unknown duration past 4min", 250_000, nil, true},
		{"unknown duration under 4min", 60_000, nil, false},
		{"zero duration under 4min", 60_000, dur(0), false},
		{"zero duration past 4min", 300_000, dur(0), true},
		{"negative duration ignored, under 4min", 60_000, dur(-5), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenedEnough(tt.positionMS, tt.durationMS); got != tt.want {
				t.Errorf("listenedEnough(%d, %v) = %v, want %v", tt.positionMS, tt.durationMS, got, tt.want)
			}
		})
	}
}

// ── now playing ─────────────────────────────────────────────────────────────

// Clients report "playing" on every heartbeat. Only the first in a window is
// announced, and a pause lets the next play announce straight away.
func TestPlay_AnnouncesNowPlayingOncePerWindow(t *testing.T) {
	st := lbOn
	st.LastFMSessionKey = "sk-1"
	h := newHarness(t, st, fakeMedia{trackOK: true, track: zeppelin}, bothApps)

	for i := 0; i < 5; i++ {
		h.play("play", int64(i)*10_000, dur(295_000))
	}
	lb := h.srv.calls("lb")
	if len(lb) != 1 || len(h.srv.calls("track.updateNowPlaying")) != 1 {
		t.Fatalf("heartbeats must announce once: listenbrainz %d, last.fm %d",
			len(lb), len(h.srv.calls("track.updateNowPlaying")))
	}
	if lb[0].body["listen_type"] != "playing_now" {
		t.Errorf("listen_type: got %v, want playing_now", lb[0].body["listen_type"])
	}
	if listen := lb[0].body["payload"].([]any)[0].(map[string]any); listen["listened_at"] != nil {
		t.Errorf("playing_now must not carry listened_at: %v", listen)
	}
	if h.store.gets != 1 {
		t.Errorf("throttled heartbeats must not load settings: %d loads", h.store.gets)
	}

	h.play("pause", 50_000, dur(295_000))
	h.play("play", 50_000, dur(295_000))
	if n := len(h.srv.calls("track.updateNowPlaying")); n != 2 {
		t.Errorf("a play after a pause must announce again: %d announcements", n)
	}

	h.clock = h.clock.Add(nowPlayingWindow)
	h.play("play", 60_000, dur(295_000))
	if n := len(h.srv.calls("track.updateNowPlaying")); n != 3 {
		t.Errorf("a still-playing item must re-announce each window: %d announcements", n)
	}
}

func TestNowPlayingGate(t *testing.T) {
	g := newNowPlayingGate(time.Minute)
	user, a, b := uuid.New(), uuid.New(), uuid.New()
	t0 := time.Unix(0, 0)

	if !g.claim(user, a, t0) {
		t.Fatal("first claim must pass")
	}
	if g.claim(user, a, t0.Add(59*time.Second)) {
		t.Error("a claim inside the window must be refused")
	}
	if !g.claim(user, b, t0.Add(time.Second)) {
		t.Error("another item has its own window")
	}
	g.release(user, a)
	if !g.claim(user, a, t0.Add(2*time.Second)) {
		t.Error("a released item must announce again")
	}
	// Past the window the next claim sweeps the stale entries.
	if !g.claim(user, uuid.New(), t0.Add(3*time.Minute)) || len(g.last) != 1 {
		t.Errorf("stale entries must be swept: %d left", len(g.last))
	}
}

// ── Last.fm ─────────────────────────────────────────────────────────────────

func TestLastFMSign(t *testing.T) {
	// md5("api_keyxxxmethodauth.getSessiontokenyyy" + "secret"). format and
	// callback are never signed.
	p := url.Values{"method": {"auth.getSession"}, "api_key": {"xxx"}, "token": {"yyy"}, "format": {"json"}, "callback": {"cb"}}
	sum := md5.Sum([]byte("api_keyxxxmethodauth.getSessiontokenyyysecret"))
	if got, want := lastFMSign(p, "secret"), hex.EncodeToString(sum[:]); got != want {
		t.Errorf("api_sig: got %s, want %s", got, want)
	}
}

func TestStop_ScrobblesToLastFM(t *testing.T) {
	h := newHarness(t, Settings{LastFMSessionKey: "sk-1"}, fakeMedia{trackOK: true, track: zeppelin}, bothApps)

	h.play("stop", 200_000, dur(295_000))

	calls := h.srv.calls("track.scrobble")
	if len(calls) != 1 {
		t.Fatalf("expected 1 scrobble, got %d", len(calls))
	}
	f := calls[0].form
	want := map[string]string{
		"sk": "sk-1", "api_key": "lfm-key", "format": "json",
		"artist": "Led Zeppelin", "track": "Black Dog", "album": "IV", "albumArtist": "Led Zeppelin",
		"trackNumber": "1", "duration": "295", "mbid": zeppelin.RecordingMBID,
		// The start of the play: the stop time less the position.
		"timestamp": "1699999800",
	}
	for k, v := range want {
		if f.Get(k) != v {
			t.Errorf("%s: got %q, want %q", k, f.Get(k), v)
		}
	}
	signed := url.Values{}
	for k, v := range f {
		if k != "api_sig" {
			signed[k] = v
		}
	}
	if f.Get("api_sig") != lastFMSign(signed, "lfm-secret") {
		t.Error("api_sig doesn't verify with the shared secret")
	}
	if n := len(h.srv.calls("lb")); n != 0 {
		t.Errorf("an unlinked ListenBrainz must get nothing, got %d", n)
	}
}

func TestStop_LastFMSkipsShortTracks(t *testing.T) {
	short := zeppelin
	short.DurationMS = 25_000
	h := newHarness(t, Settings{LastFMSessionKey: "sk-1"}, fakeMedia{trackOK: true, track: short}, bothApps)
	h.play("stop", 25_000, dur(25_000))
	if n := len(h.srv.calls("track.scrobble")); n != 0 {
		t.Errorf("a track of 30s or less must not scrobble to Last.fm, got %d", n)
	}
}

func TestLastFMScrobble_IgnoredIsAnError(t *testing.T) {
	h := newHarness(t, Settings{}, fakeMedia{}, bothApps)
	h.srv.handle("track.scrobble", func(w http.ResponseWriter, _ req) {
		_, _ = io.WriteString(w, `{"scrobbles":{"@attr":{"accepted":"0","ignored":"1"}}}`)
	})
	if err := h.svc.lastFMScrobble(context.Background(), testLastFM, "sk", zeppelin, h.clock); err == nil {
		t.Error("an ignored scrobble must report an error")
	}
}

func TestLastFMLink(t *testing.T) {
	h := newHarness(t, Settings{}, fakeMedia{}, bothApps)
	ctx := context.Background()

	start, err := h.svc.StartLastFMLink(ctx, h.user)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	u, _ := url.Parse(start.AuthURL)
	if u.Host != "www.last.fm" || u.Query().Get("api_key") != "lfm-key" || u.Query().Get("token") != "req-token" {
		t.Errorf("auth url: %s", start.AuthURL)
	}
	if strings.Contains(start.Pending, "req-token") {
		t.Error("the pending handle must be sealed")
	}

	// Not approved yet: Last.fm answers error 14.
	h.srv.handle("auth.getSession", func(w http.ResponseWriter, _ req) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":14,"message":"Unauthorized Token - This token has not been authorized"}`)
	})
	if res, err := h.svc.CompleteLastFMLink(ctx, h.user, start.Pending); err != nil || res.Status != LinkPending {
		t.Fatalf("before approval: %+v, %v", res, err)
	}

	h.srv.handle("auth.getSession", nil)
	res, err := h.svc.CompleteLastFMLink(ctx, h.user, start.Pending)
	if err != nil || res.Status != LinkLinked || res.Username != "lfm-user" {
		t.Fatalf("after approval: %+v, %v", res, err)
	}
	if got := h.srv.calls("auth.getSession"); got[len(got)-1].form.Get("token") != "req-token" {
		t.Error("the session must be requested for the sealed token")
	}
	if st := h.store.snapshot(); st.LastFMSessionKey != "sk-1" || st.LastFMUsername != "lfm-user" {
		t.Errorf("stored: %+v", st)
	}

	status, _ := h.svc.Status(ctx, h.user)
	if !status.LastFMAvailable || !status.LastFMLinked || status.LastFMUsername != "lfm-user" {
		t.Errorf("status: %+v", status)
	}

	if err := h.svc.UnlinkLastFM(ctx, h.user); err != nil || h.store.snapshot().LastFMSessionKey != "" {
		t.Errorf("unlink: %v, %+v", err, h.store.snapshot())
	}
}

func TestLastFMLink_ExpiredAndForeignHandles(t *testing.T) {
	h := newHarness(t, Settings{}, fakeMedia{}, bothApps)
	ctx := context.Background()
	start, err := h.svc.StartLastFMLink(ctx, h.user)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := h.svc.CompleteLastFMLink(ctx, uuid.New(), start.Pending); !errors.Is(err, ErrInvalidPending) {
		t.Errorf("another user's handle: got %v, want ErrInvalidPending", err)
	}
	if _, err := h.svc.CompleteTraktLink(ctx, h.user, start.Pending); !errors.Is(err, ErrInvalidPending) {
		t.Errorf("a Last.fm handle used for Trakt: got %v, want ErrInvalidPending", err)
	}

	h.srv.handle("auth.getSession", func(w http.ResponseWriter, _ req) {
		_, _ = io.WriteString(w, `{"error":15,"message":"This token has expired"}`)
	})
	if res, _ := h.svc.CompleteLastFMLink(ctx, h.user, start.Pending); res.Status != LinkExpired {
		t.Errorf("error 15: got %q, want expired", res.Status)
	}

	h.clock = h.clock.Add(lastFMTokenTTL + time.Second)
	if res, err := h.svc.CompleteLastFMLink(ctx, h.user, start.Pending); err != nil || res.Status != LinkExpired {
		t.Errorf("stale handle: %+v, %v", res, err)
	}
}

func TestLink_NotConfigured(t *testing.T) {
	h := newHarness(t, Settings{}, fakeMedia{}, fakeApps{})
	ctx := context.Background()
	if _, err := h.svc.StartLastFMLink(ctx, h.user); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("last.fm: got %v", err)
	}
	if _, err := h.svc.StartTraktLink(ctx, h.user); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("trakt: got %v", err)
	}
	if st, _ := h.svc.Status(ctx, h.user); st.LastFMAvailable || st.TraktAvailable {
		t.Errorf("nothing configured must report unavailable: %+v", st)
	}
}

// A wrong key or secret is the operator's to fix, not a network fault: it
// comes back as ErrAppRejected. Other service errors don't.
func TestLink_RejectedAppCredentials(t *testing.T) {
	ctx := context.Background()
	lastFMError := func(code int) func(http.ResponseWriter, req) {
		return func(w http.ResponseWriter, _ req) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":`+strconv.Itoa(code)+`,"message":"nope"}`)
		}
	}
	h := newHarness(t, Settings{}, fakeMedia{}, bothApps)

	for _, code := range []int{10, 13, 26} {
		h.srv.handle("auth.getToken", lastFMError(code))
		if _, err := h.svc.StartLastFMLink(ctx, h.user); !errors.Is(err, ErrAppRejected) {
			t.Errorf("last.fm error %d: got %v, want ErrAppRejected", code, err)
		}
	}
	h.srv.handle("auth.getToken", lastFMError(11)) // service offline
	if _, err := h.svc.StartLastFMLink(ctx, h.user); err == nil || errors.Is(err, ErrAppRejected) {
		t.Errorf("last.fm error 11: got %v, want a plain upstream error", err)
	}

	h.srv.handle("auth.getToken", nil)
	start, _ := h.svc.StartLastFMLink(ctx, h.user)
	h.srv.handle("auth.getSession", lastFMError(13))
	if _, err := h.svc.CompleteLastFMLink(ctx, h.user, start.Pending); !errors.Is(err, ErrAppRejected) {
		t.Errorf("bad signature on complete: got %v", err)
	}

	h.srv.handle("/oauth/device/code", func(w http.ResponseWriter, _ req) { w.WriteHeader(http.StatusUnauthorized) })
	if _, err := h.svc.StartTraktLink(ctx, h.user); !errors.Is(err, ErrAppRejected) {
		t.Errorf("trakt 401: got %v", err)
	}
	h.srv.handle("/oauth/device/code", nil)
	tstart, _ := h.svc.StartTraktLink(ctx, h.user)
	h.srv.handle("/oauth/device/token", func(w http.ResponseWriter, _ req) { w.WriteHeader(http.StatusForbidden) })
	if _, err := h.svc.CompleteTraktLink(ctx, h.user, tstart.Pending); !errors.Is(err, ErrAppRejected) {
		t.Errorf("trakt 403 on complete: got %v", err)
	}
}

// ── Trakt ───────────────────────────────────────────────────────────────────

func TestTraktLink(t *testing.T) {
	h := newHarness(t, Settings{}, fakeMedia{}, bothApps)
	ctx := context.Background()

	start, err := h.svc.StartTraktLink(ctx, h.user)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if start.UserCode != "ABCD1234" || start.VerificationURL != "https://trakt.tv/activate" || start.Interval != 5 || start.ExpiresIn != 600 {
		t.Errorf("start: %+v", start)
	}
	if c := h.srv.calls("/oauth/device/code"); c[0].header.Get("trakt-api-key") != "trakt-id" || c[0].body["client_id"] != "trakt-id" {
		t.Errorf("device code request: %+v %+v", c[0].header, c[0].body)
	}

	for status, want := range map[int]LinkStatus{
		http.StatusBadRequest:      LinkPending,
		http.StatusTooManyRequests: LinkSlowDown,
		http.StatusTeapot:          LinkDenied,
		http.StatusGone:            LinkExpired,
		http.StatusConflict:        LinkExpired,
	} {
		h.srv.handle("/oauth/device/token", func(w http.ResponseWriter, _ req) { w.WriteHeader(status) })
		if res, err := h.svc.CompleteTraktLink(ctx, h.user, start.Pending); err != nil || res.Status != want {
			t.Errorf("status %d: got %+v, %v; want %s", status, res, err, want)
		}
	}
	if h.store.snapshot().Trakt.linked() {
		t.Fatal("nothing may be stored before approval")
	}

	h.srv.handle("/oauth/device/token", nil)
	res, err := h.svc.CompleteTraktLink(ctx, h.user, start.Pending)
	if err != nil || res.Status != LinkLinked || res.Username != "trakt-user" {
		t.Fatalf("approved: %+v, %v", res, err)
	}
	poll := h.srv.calls("/oauth/device/token")
	if b := poll[len(poll)-1].body; b["code"] != "dev-code" || b["client_secret"] != "trakt-secret" {
		t.Errorf("token poll body: %+v", b)
	}
	link := h.store.snapshot().Trakt
	if link.AccessToken != "new-access" || link.RefreshToken != "new-refresh" || link.Username != "trakt-user" ||
		!link.ExpiresAt.Equal(time.Unix(1_700_000_000+86_400, 0)) {
		t.Errorf("stored link: %+v", link)
	}
}

var traktLinked = Settings{Trakt: TraktLink{
	AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Unix(1_700_000_000, 0).Add(24 * time.Hour),
}}

func TestStop_ScrobblesMovieToTrakt(t *testing.T) {
	movie := Video{Title: "Heat", Year: 1995, TMDBID: 949, IMDBID: "tt0113277", DurationMS: 10_200_000}
	h := newHarness(t, traktLinked, fakeMedia{videoOK: true, video: movie}, bothApps)

	h.play("stop", 9_180_000, nil) // 90%, the item's own duration

	calls := h.srv.calls("/scrobble/stop")
	if len(calls) != 1 {
		t.Fatalf("expected 1 stop, got %d", len(calls))
	}
	c := calls[0]
	if c.header.Get("Authorization") != "Bearer access" || c.header.Get("trakt-api-version") != "2" ||
		c.header.Get("trakt-api-key") != "trakt-id" || c.header.Get("User-Agent") == "" {
		t.Errorf("headers: %v", c.header)
	}
	m, _ := c.body["movie"].(map[string]any)
	ids, _ := m["ids"].(map[string]any)
	if m["title"] != "Heat" || m["year"] != float64(1995) || ids["tmdb"] != float64(949) || ids["imdb"] != "tt0113277" {
		t.Errorf("movie: %+v", m)
	}
	if c.body["progress"] != float64(90) || c.body["show"] != nil || c.body["episode"] != nil {
		t.Errorf("body: %+v", c.body)
	}
}

func TestStop_ScrobblesEpisodeAsShowAndNumbers(t *testing.T) {
	ep := Video{Episode: true, Title: "The Wire", Year: 2002, TMDBID: 1438, TVDBID: 79126, Season: 4, Number: 13, DurationMS: 4_800_000}
	h := newHarness(t, traktLinked, fakeMedia{videoOK: true, video: ep}, bothApps)

	h.play("stop", 1_200_000, dur(4_800_000))

	body := h.srv.calls("/scrobble/stop")[0].body
	show, _ := body["show"].(map[string]any)
	ids, _ := show["ids"].(map[string]any)
	epRef, _ := body["episode"].(map[string]any)
	if show["title"] != "The Wire" || ids["tmdb"] != float64(1438) || ids["tvdb"] != float64(79126) {
		t.Errorf("show: %+v", show)
	}
	if epRef["season"] != float64(4) || epRef["number"] != float64(13) || body["progress"] != float64(25) {
		t.Errorf("episode / progress: %+v", body)
	}
	if body["movie"] != nil {
		t.Error("an episode must not be sent as a movie")
	}
}

func TestTrakt_StartPauseAndGaps(t *testing.T) {
	movie := Video{Title: "Heat", Year: 1995, TMDBID: 949, DurationMS: 10_000_000}
	h := newHarness(t, traktLinked, fakeMedia{videoOK: true, video: movie}, bothApps)

	h.play("play", 0, nil)
	h.play("play", 10_000, nil)
	h.play("pause", 1_000_000, nil)
	if len(h.srv.calls("/scrobble/start")) != 1 || len(h.srv.calls("/scrobble/pause")) != 1 {
		t.Errorf("start %d, pause %d; want 1 and 1",
			len(h.srv.calls("/scrobble/start")), len(h.srv.calls("/scrobble/pause")))
	}
	if p := h.srv.calls("/scrobble/pause")[0].body["progress"]; p != float64(10) {
		t.Errorf("pause progress: got %v, want 10", p)
	}

	// No duration anywhere: Trakt can't be given a progress, so nothing goes.
	h2 := newHarness(t, traktLinked, fakeMedia{videoOK: true, video: Video{Title: "x"}}, bothApps)
	h2.play("stop", 1_000_000, nil)
	if n := len(h2.srv.calls("/scrobble/stop")); n != 0 {
		t.Errorf("no duration must send nothing, got %d", n)
	}
	// Music doesn't go to Trakt.
	h3 := newHarness(t, traktLinked, fakeMedia{trackOK: true, track: zeppelin}, bothApps)
	h3.play("stop", 200_000, dur(295_000))
	if n := len(h3.srv.calls("/scrobble/stop")); n != 0 {
		t.Errorf("a track must not reach Trakt, got %d", n)
	}
}

func TestTrakt_RefreshesAnExpiringToken(t *testing.T) {
	st := traktLinked
	st.Trakt.ExpiresAt = time.Unix(1_700_000_000, 0).Add(10 * time.Minute)
	h := newHarness(t, st, fakeMedia{videoOK: true, video: Video{Title: "Heat", DurationMS: 1000}}, bothApps)

	h.play("stop", 900, nil)

	refresh := h.srv.calls("/oauth/token")
	if len(refresh) != 1 || refresh[0].body["refresh_token"] != "refresh" || refresh[0].body["grant_type"] != "refresh_token" {
		t.Fatalf("refresh: %+v", refresh)
	}
	if got := h.srv.calls("/scrobble/stop")[0].header.Get("Authorization"); got != "Bearer new-access" {
		t.Errorf("the scrobble must use the refreshed token: %q", got)
	}
	if link := h.store.snapshot().Trakt; link.AccessToken != "new-access" || link.RefreshToken != "new-refresh" {
		t.Errorf("stored: %+v", link)
	}
}

func TestTrakt_RetriesOnceAfterA401(t *testing.T) {
	h := newHarness(t, traktLinked, fakeMedia{videoOK: true, video: Video{Title: "Heat", DurationMS: 1000}}, bothApps)
	var n int
	h.srv.handle("/scrobble/stop", func(w http.ResponseWriter, _ req) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	h.play("stop", 900, nil)

	calls := h.srv.calls("/scrobble/stop")
	if len(calls) != 2 || calls[1].header.Get("Authorization") != "Bearer new-access" {
		t.Fatalf("expected a retry with the refreshed token: %d calls", len(calls))
	}
}

func TestTrakt_ARevokedGrantUnlinks(t *testing.T) {
	st := traktLinked
	st.Trakt.ExpiresAt = time.Unix(1_700_000_000, 0) // already expired
	h := newHarness(t, st, fakeMedia{videoOK: true, video: Video{Title: "Heat", DurationMS: 1000}}, bothApps)
	h.srv.handle("/oauth/token", func(w http.ResponseWriter, _ req) { w.WriteHeader(http.StatusUnauthorized) })

	h.play("stop", 900, nil)

	if h.store.snapshot().Trakt.linked() {
		t.Error("a rejected refresh token must unlink Trakt")
	}
	if n := len(h.srv.calls("/scrobble/stop")); n != 0 {
		t.Errorf("nothing can be scrobbled without a token, got %d", n)
	}
}

func TestUnlinkTrakt_RevokesAndForgets(t *testing.T) {
	h := newHarness(t, traktLinked, fakeMedia{}, bothApps)
	if err := h.svc.UnlinkTrakt(context.Background(), h.user); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	revoke := h.srv.calls("/oauth/revoke")
	if len(revoke) != 1 || revoke[0].body["token"] != "access" || revoke[0].body["client_secret"] != "trakt-secret" {
		t.Errorf("revoke: %+v", revoke)
	}
	if h.store.snapshot().Trakt.linked() {
		t.Error("unlink must forget the grant")
	}
}
