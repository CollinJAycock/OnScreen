package worker

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type mockWebhookDB struct {
	endpoints  []gen.WebhookEndpoint
	listErr    error
	failureErr error
	failures   []gen.CreateWebhookFailureParams
}

func (m *mockWebhookDB) ListWebhookEndpoints(_ context.Context) ([]gen.WebhookEndpoint, error) {
	return m.endpoints, m.listErr
}

func (m *mockWebhookDB) CreateWebhookFailure(_ context.Context, arg gen.CreateWebhookFailureParams) (gen.WebhookFailure, error) {
	m.failures = append(m.failures, arg)
	return gen.WebhookFailure{}, m.failureErr
}

type mockWebhookMedia struct{}

func (m *mockWebhookMedia) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	return &media.Item{Type: "movie", Title: "Test Movie"}, nil
}

func newTestDispatcher(t *testing.T, db *mockWebhookDB) *WebhookDispatcher {
	t.Helper()
	enc := testEncryptor(t)
	d := NewWebhookDispatcher(
		db,
		&mockWebhookMedia{},
		enc,
		WebhookServerInfo{Title: "Test", MachineID: "test-id"},
		slog.Default(),
	)
	// Override SafeTransport (which blocks loopback) so tests can use httptest servers.
	d.client = &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(func() { d.Close() })
	return d
}

func testEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key := auth.DeriveKey32("test-key-for-webhook-dispatcher-tests!!")
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("create encryptor: %v", err)
	}
	return enc
}

// ── Dispatch ─────────────────────────────────────────────────────────────────

func TestDispatch_DeliversToSubscribedEndpoints(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := &mockWebhookDB{
		endpoints: []gen.WebhookEndpoint{
			{ID: uuid.New(), Url: srv.URL, Events: []string{"media.play"}, Enabled: true},
		},
	}
	d := newTestDispatcher(t, db)

	d.Dispatch("play", uuid.New(), uuid.New())
	d.Close() // wait for delivery

	if hits.Load() != 1 {
		t.Errorf("expected 1 delivery, got %d", hits.Load())
	}
}

func TestDispatch_SkipsDisabledEndpoints(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := &mockWebhookDB{
		endpoints: []gen.WebhookEndpoint{
			{ID: uuid.New(), Url: srv.URL, Events: []string{"media.play"}, Enabled: false},
		},
	}
	d := newTestDispatcher(t, db)

	d.Dispatch("play", uuid.New(), uuid.Nil)
	d.Close()

	if hits.Load() != 0 {
		t.Errorf("expected 0 deliveries to disabled endpoint, got %d", hits.Load())
	}
}

func TestDispatch_SkipsUnsubscribedEvents(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := &mockWebhookDB{
		endpoints: []gen.WebhookEndpoint{
			{ID: uuid.New(), Url: srv.URL, Events: []string{"media.stop"}, Enabled: true},
		},
	}
	d := newTestDispatcher(t, db)

	d.Dispatch("play", uuid.New(), uuid.Nil) // event is "play", endpoint only subscribes to "stop"
	d.Close()

	if hits.Load() != 0 {
		t.Errorf("expected 0 deliveries for unsubscribed event, got %d", hits.Load())
	}
}

// ── Close cancels retry sleeps ───────────────────────────────────────────────

func TestClose_CancelsRetries(t *testing.T) {
	// Endpoint always fails → retries with 30s + 5m delays.
	// Close should interrupt and return quickly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := &mockWebhookDB{
		endpoints: []gen.WebhookEndpoint{
			{ID: uuid.New(), Url: srv.URL, Events: []string{"media.play"}, Enabled: true},
		},
	}
	d := newTestDispatcher(t, db)

	d.Dispatch("play", uuid.New(), uuid.Nil)

	// Give it a moment to start the first delivery attempt.
	time.Sleep(100 * time.Millisecond)

	// Close should return quickly (not wait 5+ minutes for retries).
	done := make(chan struct{})
	go func() {
		d.Close()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(3 * time.Second):
		t.Fatal("Close took too long — context cancellation did not interrupt retries")
	}
}

// ── webhookEvent mapping ─────────────────────────────────────────────────────

func TestWebhookEvent_Mapping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"play", "media.play"},
		{"pause", "media.pause"},
		{"resume", "media.resume"},
		{"stop", "media.stop"},
		{"scrobble", "media.scrobble"},
		{"library.scan.complete", "library.scan.complete"},
		{"custom", "media.custom"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := webhookEvent(tt.input)
			if got != tt.want {
				t.Errorf("webhookEvent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
