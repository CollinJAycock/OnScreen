package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// /auth/pin-switch mints the weakest credential the system issues: derived from
// a 4-digit PIN, no refresh token. It must not reach anything that destroys the
// account or seizes control of how it authenticates. SetPIN and ClearPIN
// already demand the account password; account deletion and 2FA enrolment are
// strictly more destructive and were reachable with nothing but the PIN.
func TestBlockSwitchedSession(t *testing.T) {
	cases := []struct {
		name     string
		claims   *auth.Claims
		wantStop bool
	}{
		{"pin-switched session is blocked",
			&auth.Claims{UserID: uuid.New(), Switched: true}, true},
		{"full login passes",
			&auth.Claims{UserID: uuid.New()}, false},
		{"admin pin-switch is still blocked",
			&auth.Claims{UserID: uuid.New(), Switched: true, IsAdmin: true}, true},
		{"no claims passes (handler's own auth check owns this)",
			nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("DELETE", "/", nil)
			if c.claims != nil {
				req = req.WithContext(middleware.WithClaims(req.Context(), c.claims))
			}
			rec := httptest.NewRecorder()
			got := blockSwitchedSession(rec, req, "do the thing")

			if got != c.wantStop {
				t.Fatalf("blocked = %v, want %v", got, c.wantStop)
			}
			if c.wantStop && rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if !c.wantStop && rec.Code != http.StatusOK {
				t.Errorf("wrote a response when it should have passed through: %d", rec.Code)
			}
		})
	}
}
