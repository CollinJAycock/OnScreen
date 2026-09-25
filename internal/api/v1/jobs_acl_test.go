package v1

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// /jobs used to hand every signed-in user the name of every library being
// scanned. With an access checker wired, a non-admin sees only their own
// libraries' scans; admins still see all.
func TestJobsHandler_ScopesScansToCallerLibraries(t *testing.T) {
	public, private := uuid.New(), uuid.New()
	scans := &stubScanLister{ids: []uuid.UUID{public, private}}
	namer := &stubLibNamer{names: map[uuid.UUID]string{public: "Kids", private: "Private"}}
	access := fakePeopleAccess{allowed: map[uuid.UUID]struct{}{public: {}}}
	h := NewJobsHandler(scans, &stubJobsCounters{}, namer, slog.Default()).WithLibraryAccess(access)

	get := func(c *auth.Claims) []any {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
		if c != nil {
			req = req.WithContext(middleware.WithClaims(req.Context(), c))
		}
		rec := httptest.NewRecorder()
		h.Get(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		out, _ := decodeData(t, rec).Data["scans"].([]any)
		return out
	}

	user := get(&auth.Claims{UserID: uuid.New()})
	if len(user) != 1 || user[0].(map[string]any)["library_id"] != public.String() {
		t.Errorf("non-admin scans = %v, want only the accessible library", user)
	}
	if admin := get(&auth.Claims{UserID: uuid.New(), IsAdmin: true}); len(admin) != 2 {
		t.Errorf("admin scans = %v, want both", admin)
	}
	if anon := get(nil); len(anon) != 0 {
		t.Errorf("no claims must fail closed, got %v", anon)
	}
}
