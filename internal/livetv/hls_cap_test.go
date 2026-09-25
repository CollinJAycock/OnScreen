package livetv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// blockingOpener stands in for the tuner layer: every OpenChannelStream call is
// counted and then parked until release is closed — modelling the 100 ms–10 s a
// real tuner takes to lock, which is exactly the window the MaxSessions check
// used to be made across. Once released, every open fails.
type blockingOpener struct {
	opens   atomic.Int32
	release chan struct{}
}

var errUpstreamDown = errors.New("upstream unavailable")

func (b *blockingOpener) OpenChannelStream(_ context.Context, _ uuid.UUID) (Stream, error) {
	b.opens.Add(1)
	<-b.release
	return nil, errUpstreamDown
}

func newCapTestProxy(t *testing.T, svc hlsStreamSource, maxSessions int) *HLSProxy {
	t.Helper()
	return &HLSProxy{
		cfg:      HLSConfig{Dir: t.TempDir(), FFmpegBin: "unused", MaxSessions: maxSessions},
		svc:      svc,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
}

func (p *HLSProxy) pendingCreates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.creating)
}

// The cap used to be check-then-act: len(sessions) was read under p.mu, the
// lock dropped, and the session inserted only after the tune + ffmpeg start.
// 30 parallel first-viewers of 30 channels all read the same under-cap map and
// all tuned. With the slot reserved under the lock, at most MaxSessions tunes
// may ever be in flight, and every other viewer is refused without one.
func TestHLSProxy_MaxSessionsHoldsUnderConcurrentFirstViewers(t *testing.T) {
	const maxSessions, viewers = 3, 30
	opener := &blockingOpener{release: make(chan struct{})}
	p := newCapTestProxy(t, opener, maxSessions)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make(chan error, viewers)
	for i := 0; i < viewers; i++ {
		go func() {
			_, err := p.Acquire(ctx, uuid.New())
			results <- err
		}()
	}

	// Everyone beyond the cap is refused while the admitted tunes are still
	// in flight — without ever reaching the tuner.
	for i := 0; i < viewers-maxSessions; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrAllTunersBusy) {
				t.Fatalf("over-cap viewer got %v, want ErrAllTunersBusy", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d over-cap viewers were refused; opens so far = %d (cap %d)",
				i, viewers-maxSessions, opener.opens.Load(), maxSessions)
		}
	}
	dvrWaitFor(t, 2*time.Second, func() bool { return opener.opens.Load() >= maxSessions },
		"admitted viewers never reached the tuner")
	time.Sleep(50 * time.Millisecond) // room for any excess tune to show up
	if got := opener.opens.Load(); got != maxSessions {
		t.Fatalf("%d tunes started concurrently, cap is %d", got, maxSessions)
	}

	// Fail the admitted tunes. Cancel first so their post-failure join poll
	// returns at once instead of waiting out its window.
	cancel()
	close(opener.release)
	for i := 0; i < maxSessions; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, errUpstreamDown) {
				t.Errorf("admitted viewer got %v, want the upstream error", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("admitted viewer never returned after its tune failed")
		}
	}

	// Every error path must give its slot back.
	if n := p.pendingCreates(); n != 0 {
		t.Fatalf("%d reservations leaked on the tune-failure path", n)
	}
	if _, err := p.Acquire(ctx, uuid.New()); !errors.Is(err, errUpstreamDown) {
		t.Errorf("after failures freed the slots, a new channel got %v, want a fresh tune attempt", err)
	}
	if got := opener.opens.Load(); got != maxSessions+1 {
		t.Errorf("tune attempts = %d, want %d", got, maxSessions+1)
	}
}

// A second first-viewer of the SAME channel must wait for the in-flight create
// rather than tune a duplicate — and must not be refused as "busy" at the cap
// edge for a channel that is about to be live.
func TestHLSProxy_SameChannelFirstViewersShareOneTune(t *testing.T) {
	const viewers = 5
	opener := &blockingOpener{release: make(chan struct{})}
	p := newCapTestProxy(t, opener, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := uuid.New()

	results := make(chan error, viewers)
	for i := 0; i < viewers; i++ {
		go func() {
			_, err := p.Acquire(ctx, ch)
			results <- err
		}()
	}
	dvrWaitFor(t, 2*time.Second, func() bool { return opener.opens.Load() >= 1 },
		"no viewer reached the tuner")
	time.Sleep(100 * time.Millisecond)
	if got := opener.opens.Load(); got != 1 {
		t.Fatalf("%d tunes for one channel; same-channel viewers must share the in-flight create", got)
	}
	select {
	case err := <-results:
		t.Fatalf("a same-channel viewer returned early with %v; it must wait for the in-flight create", err)
	default:
	}

	// Waiters honour their own request context.
	cancel()
	for i := 0; i < viewers-1; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("waiter got %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not return on context cancel")
		}
	}
	close(opener.release)
	select {
	case err := <-results:
		if !errors.Is(err, errUpstreamDown) {
			t.Errorf("creator got %v, want the upstream error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("creator never returned")
	}
	if got := opener.opens.Load(); got != 1 {
		t.Errorf("tune attempts = %d, want 1", got)
	}
	if n := p.pendingCreates(); n != 0 {
		t.Errorf("%d reservations leaked", n)
	}
}

// On success the reservation must be HANDED to the session, not kept beside
// it: a leaked reservation would count every running channel twice and halve
// the effective cap.
func TestHLSProxy_ReservationHandedToSessionOnSuccess(t *testing.T) {
	src := &dvrFakeSource{}
	p := newCapTestProxy(t, src, 2)
	p.cfg.FFmpegBin = os.Args[0] // re-invoke the test binary; see TestMain
	t.Setenv("DVR_FAKE_FFMPEG", "1")
	t.Cleanup(p.Shutdown)
	ctx := context.Background()

	a, err := p.Acquire(ctx, uuid.New())
	if err != nil {
		t.Fatalf("first channel: %v", err)
	}
	if n := p.pendingCreates(); n != 0 {
		t.Fatalf("%d reservations still held after a successful create", n)
	}
	b, err := p.Acquire(ctx, uuid.New())
	if err != nil {
		t.Fatalf("second channel under a cap of 2: %v (a leaked reservation double-counts the first)", err)
	}
	if _, err := p.Acquire(ctx, uuid.New()); !errors.Is(err, ErrAllTunersBusy) {
		t.Fatalf("third channel over a cap of 2: got %v, want ErrAllTunersBusy", err)
	}
	src.mu.Lock()
	opened := len(src.streams)
	src.mu.Unlock()
	if opened != 2 {
		t.Errorf("upstream opens = %d, want 2 (the refused channel must not tune)", opened)
	}

	// Joining a running channel is never capped.
	a2, err := p.Acquire(ctx, a.channelID)
	if err != nil || a2 != a {
		t.Fatalf("joining a running channel at the cap: got (%p, %v), want the live session", a2, err)
	}
	p.Release(a)
	p.Release(a2)
	p.Release(b)
}
