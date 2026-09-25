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
