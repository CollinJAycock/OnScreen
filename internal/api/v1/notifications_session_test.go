package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/auth"
)

// flipValidator reports the session valid until revoke() is called.
type flipValidator struct {
	revoked atomic.Bool
	checks  atomic.Int64
}

func (v *flipValidator) SessionStillValid(context.Context, *auth.Claims) bool {
	v.checks.Add(1)
	return !v.revoked.Load()
}

func runStream(h *NotificationHandler, ctx context.Context) <-chan struct{} {
	req := notifAuthedRequest(httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stream", nil).WithContext(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Stream(httptest.NewRecorder(), req)
	}()
	return done
}

// An SSE stream opened before a logout/demote/delete must close once the
// session is revoked, not run for the life of the connection.
func TestNotificationsStream_ClosesWhenSessionRevoked(t *testing.T) {
	v := &flipValidator{}
	h := newNotifHandler(&mockNotifDB{}).WithSessionValidator(v)
	h.recheckEvery = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runStream(h, ctx)

	// Still open while the session is valid (and re-checks are happening).
	deadline := time.Now().Add(2 * time.Second)
	for v.checks.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("stream closed while the session was still valid")
	default:
	}

	v.revoked.Store(true)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream stayed open after the session was revoked")
	}
}

// Without a validator wired the stream behaves as before (no re-check).
func TestNotificationsStream_NoValidatorStaysOpen(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})
	h.recheckEvery = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := runStream(h, ctx)
	select {
	case <-done:
		t.Fatal("stream closed with no validator configured")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-done
}
