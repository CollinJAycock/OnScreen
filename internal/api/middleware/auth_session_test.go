package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// SessionStillValid is the re-check long-lived responses (SSE) run after
// admission; it must apply exactly the admission rules.
func TestSessionStillValid(t *testing.T) {
	tm := testTokenMaker(t)
	claims := &auth.Claims{UserID: uuid.New(), SessionEpoch: 3}
	ctx := context.Background()

	cases := []struct {
		name   string
		reader SessionEpochReader
		want   bool
	}{
		{"epoch unchanged", stubEpochReader{epoch: 3}, true},
		{"epoch bumped (logout/demote/reset)", stubEpochReader{epoch: 4}, false},
		{"user deleted", stubEpochReader{err: ErrUserNotFound}, false},
		// Accepted risk, documented on epochValid: a DB blip doesn't log everybody out.
		{"transient DB error fails open", stubEpochReader{err: errors.New("conn refused")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewAuthenticator(tm).WithEpochReader(c.reader)
			if got := a.SessionStillValid(ctx, claims); got != c.want {
				t.Errorf("SessionStillValid = %v, want %v", got, c.want)
			}
		})
	}
	if NewAuthenticator(tm).SessionStillValid(ctx, nil) {
		t.Error("nil claims must never be valid")
	}
}

// stubStateReader is a SessionEpochReader that also answers the per-session
// check. epochCalls counts fallbacks to the epoch-only lookup.
type stubStateReader struct {
	epoch      int64
	live       map[uuid.UUID]bool
	err        error
	epochCalls *int
}

func (s stubStateReader) GetSessionEpoch(_ context.Context, _ uuid.UUID) (int64, error) {
	if s.epochCalls != nil {
		*s.epochCalls++
	}
	return s.epoch, s.err
}

func (s stubStateReader) GetSessionState(_ context.Context, _, sid uuid.UUID) (int64, bool, error) {
	return s.epoch, s.live[sid], s.err
}

// A token naming a revoked sign-in dies on its next request even though the
// user's epoch never moved; a token naming a live one, and a token with no sid
// at all (PIN-switch, pre-upgrade), still pass.
func TestEpochValid_PerSessionRevocation(t *testing.T) {
	tm := testTokenMaker(t)
	live, revoked := uuid.New(), uuid.New()
	epochCalls := 0
	reader := stubStateReader{epoch: 2, live: map[uuid.UUID]bool{live: true}, epochCalls: &epochCalls}
	a := NewAuthenticator(tm).WithEpochReader(reader)
	ctx := context.Background()
	uid := uuid.New()

	if !a.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 2, SessionID: live}) {
		t.Error("live session rejected")
	}
	if a.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 2, SessionID: revoked}) {
		t.Error("revoked session accepted")
	}
	if epochCalls != 0 {
		t.Errorf("sid tokens fell back to the epoch-only lookup %d times", epochCalls)
	}
	if !a.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 2}) {
		t.Error("sid-less token rejected")
	}
	if epochCalls != 1 {
		t.Errorf("sid-less token: epoch lookups = %d, want 1", epochCalls)
	}
	// The epoch still applies to a live session.
	if a.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 1, SessionID: live}) {
		t.Error("stale epoch accepted on a live session")
	}
	// A DB error keeps the fail-open posture; user-gone keeps failing closed.
	flaky := NewAuthenticator(tm).WithEpochReader(stubStateReader{err: errors.New("conn refused")})
	if !flaky.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 2, SessionID: revoked}) {
		t.Error("transient DB error must fail open")
	}
	gone := NewAuthenticator(tm).WithEpochReader(stubStateReader{err: ErrUserNotFound})
	if gone.SessionStillValid(ctx, &auth.Claims{UserID: uid, SessionEpoch: 2, SessionID: live}) {
		t.Error("deleted user accepted")
	}
}
