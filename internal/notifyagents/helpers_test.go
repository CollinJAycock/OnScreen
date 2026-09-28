package notifyagents

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeDB is an in-memory DB.
type fakeDB struct {
	mu        sync.Mutex
	agents    map[uuid.UUID]gen.NotificationAgent
	users     map[uuid.UUID]string
	newRows   map[uuid.UUID][]gen.ListNewContentForNotificationAgentsRow
	newCalls  []gen.ListNewContentForNotificationAgentsParams
	successes map[uuid.UUID]int
	failures  map[uuid.UUID][]string
	seq       int
	// Item → library privacy (itemLibraryDB): an item is in a public
	// library unless listed in privateItems; goneItems don't exist.
	privateItems map[uuid.UUID]bool
	goneItems    map[uuid.UUID]bool
}

var (
	fakePublicLib  = uuid.MustParse("00000000-0000-0000-0000-00000000a001")
	fakePrivateLib = uuid.MustParse("00000000-0000-0000-0000-00000000a002")
)

func newFakeDB() *fakeDB {
	return &fakeDB{
		agents:    map[uuid.UUID]gen.NotificationAgent{},
		users:     map[uuid.UUID]string{},
		newRows:   map[uuid.UUID][]gen.ListNewContentForNotificationAgentsRow{},
		successes: map[uuid.UUID]int{},
		failures:  map[uuid.UUID][]string{},

		privateItems: map[uuid.UUID]bool{},
		goneItems:    map[uuid.UUID]bool{},
	}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (f *fakeDB) ListNotificationAgents(context.Context) ([]gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gen.NotificationAgent, 0, len(f.agents))
	for _, a := range f.agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Time.Before(out[j].CreatedAt.Time) })
	return out, nil
}

func (f *fakeDB) GetNotificationAgent(_ context.Context, id uuid.UUID) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[id]
	if !ok {
		return gen.NotificationAgent{}, pgx.ErrNoRows
	}
	return a, nil
}

func (f *fakeDB) ListEnabledNotificationAgentsForEvent(_ context.Context, event string) ([]gen.NotificationAgent, error) {
	all, _ := f.ListNotificationAgents(context.Background())
	var out []gen.NotificationAgent
	for _, a := range all {
		if a.Enabled && slices.Contains(a.Events, event) {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeDB) CreateNotificationAgent(_ context.Context, arg gen.CreateNotificationAgentParams) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	now := time.Now().Add(time.Duration(f.seq) * time.Millisecond)
	a := gen.NotificationAgent{
		ID: arg.ID, Kind: arg.Kind, Name: arg.Name, Enabled: arg.Enabled, Events: arg.Events,
		Config: arg.Config, Secret: arg.Secret, AllowPrivateNetwork: arg.AllowPrivateNetwork,
		CreatedAt: ts(now), UpdatedAt: ts(now),
	}
	f.agents[arg.ID] = a
	return a, nil
}

func (f *fakeDB) UpdateNotificationAgent(_ context.Context, arg gen.UpdateNotificationAgentParams) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[arg.ID]
	if !ok {
		return gen.NotificationAgent{}, pgx.ErrNoRows
	}
	a.Name, a.Enabled, a.Events, a.Config, a.Secret, a.AllowPrivateNetwork =
		arg.Name, arg.Enabled, arg.Events, arg.Config, arg.Secret, arg.AllowPrivateNetwork
	a.UpdatedAt = ts(time.Now())
	f.agents[arg.ID] = a
	return a, nil
}

func (f *fakeDB) DeleteNotificationAgent(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[id]; !ok {
		return 0, nil
	}
	delete(f.agents, id)
	return 1, nil
}

func (f *fakeDB) RecordNotificationAgentSuccess(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.successes[id]++
	if a, ok := f.agents[id]; ok {
		a.LastSuccessAt = ts(time.Now())
		f.agents[id] = a
	}
	return nil
}

func (f *fakeDB) RecordNotificationAgentFailure(_ context.Context, arg gen.RecordNotificationAgentFailureParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[arg.ID] = append(f.failures[arg.ID], arg.LastError)
	if a, ok := f.agents[arg.ID]; ok {
		msg := arg.LastError
		a.LastError, a.LastErrorAt = &msg, ts(time.Now())
		f.agents[arg.ID] = a
	}
	return nil
}

func (f *fakeDB) ListNewContentForNotificationAgents(_ context.Context, arg gen.ListNewContentForNotificationAgentsParams) ([]gen.ListNewContentForNotificationAgentsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.newCalls = append(f.newCalls, arg)
	var out []gen.ListNewContentForNotificationAgentsRow
	for _, r := range f.newRows[arg.LibraryID] {
		if r.CreatedAt.Time.After(arg.Since.Time) {
			out = append(out, r)
		}
	}
	if len(out) > int(arg.MaxRows) {
		out = out[:arg.MaxRows]
	}
	return out, nil
}

func (f *fakeDB) GetUser(_ context.Context, id uuid.UUID) (gen.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.users[id]
	if !ok {
		return gen.User{}, pgx.ErrNoRows
	}
	return gen.User{ID: id, Username: name}, nil
}

func (f *fakeDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goneItems[id] {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	lib := fakePublicLib
	if f.privateItems[id] {
		lib = fakePrivateLib
	}
	return gen.GetMediaItemRow{ID: id, LibraryID: lib, Title: "Secret Title"}, nil
}

func (f *fakeDB) GetLibrary(_ context.Context, id uuid.UUID) (gen.Library, error) {
	switch id {
	case fakePublicLib:
		return gen.Library{ID: id}, nil
	case fakePrivateLib:
		return gen.Library{ID: id, IsPrivate: true}, nil
	}
	return gen.Library{}, pgx.ErrNoRows
}

func (f *fakeDB) successCount(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.successes[id]
}

func (f *fakeDB) failureList(id uuid.UUID) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.failures[id])
}

// fakeEmail records SendWithText calls.
type fakeEmail struct {
	mu   sync.Mutex
	sent []sentEmail
	err  error
}

type sentEmail struct {
	to                  []string
	subject, text, html string
}

func (e *fakeEmail) SendWithText(_ context.Context, to []string, subject, text, html string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	e.sent = append(e.sent, sentEmail{to, subject, text, html})
	return nil
}

func testEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// rewriteTransport sends every request to target (an httptest server),
// keeping the path and query — so a Discord or Telegram URL can be exercised
// end-to-end without leaving the machine.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = rt.target.Scheme
	r2.URL.Host = rt.target.Host
	r2.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// testService builds a Service with fast timings and, when target is set, a
// client that routes every delivery to it (no SSRF guard — the guard has its
// own tests).
func testService(t *testing.T, db *fakeDB, target string) *Service {
	t.Helper()
	s := New(Options{
		DB:        db,
		Encrypter: testEncryptor(t),
		Email:     &fakeEmail{},
		BaseURL:   func(context.Context) string { return "https://media.example.com/" },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s.lookupHost = func(_ context.Context, host string) ([]string, error) { return []string{"93.184.216.34"}, nil }
	s.retryDelays = []time.Duration{0, 5 * time.Millisecond, 5 * time.Millisecond}
	s.coalesceWindow = 20 * time.Millisecond
	s.idleTimeout = 200 * time.Millisecond
	s.sendTimeout = 2 * time.Second
	if target != "" {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		s.clientFor = func(netPolicy) *http.Client {
			return &http.Client{
				Transport:     rewriteTransport{target: u},
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
		}
	}
	t.Cleanup(s.Close)
	return s
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// genAgent builds a stored agent row subscribed to every event.
func genAgent(id uuid.UUID, k Kind, secret *string, cfg string, allowPrivate bool) gen.NotificationAgent {
	events := []string{EventTest}
	for _, e := range catalog {
		events = append(events, e.Key)
	}
	return gen.NotificationAgent{
		ID: id, Kind: string(k), Name: string(k) + " agent", Enabled: true, Events: events,
		Config: []byte(cfg), Secret: secret, AllowPrivateNetwork: allowPrivate,
		CreatedAt: ts(time.Now()), UpdatedAt: ts(time.Now()),
	}
}
