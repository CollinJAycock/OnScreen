package uat

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// ── Upcoming — any signed-in user ────────────────────────────────────────────

// stubUpcomingDB satisfies v1.UpcomingDB with no arr services configured.
type stubUpcomingDB struct{}

func (stubUpcomingDB) ListArrServices(context.Context) ([]gen.ArrService, error) { return nil, nil }
func (stubUpcomingDB) ListMediaItemsByTMDBIDs(context.Context, gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}

type stubUpcomingLibs struct{}

func (stubUpcomingLibs) List(context.Context) ([]library.Library, error) { return nil, nil }

// stubUpcomingAccess grants every library.
type stubUpcomingAccess struct{}

func (stubUpcomingAccess) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return true, nil
}
func (stubUpcomingAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

type stubUpcomingPaths struct{}

func (stubUpcomingPaths) ArrPathMappings(context.Context) map[string]string { return nil }

func newUpcomingServer(t *testing.T) *testServer {
	return newExtrasServer(t, func(h *api.Handlers) {
		h.Upcoming = v1.NewUpcomingHandler(stubUpcomingDB{}, stubUpcomingLibs{},
			stubUpcomingAccess{}, stubUpcomingPaths{}, slog.Default())
	})
}

func TestUpcoming_RequiresAuth(t *testing.T) {
	ts := newUpcomingServer(t)
	resp := ts.do("GET", "/api/v1/upcoming", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestUpcoming_OpenToNonAdmin(t *testing.T) {
	ts := newUpcomingServer(t)
	resp := ts.do("GET", "/api/v1/upcoming?from=2026-10-01&to=2026-10-31", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, readBody(resp))
	}
	var body struct {
		Data struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Items    []any  `json:"items"`
			Services []any  `json:"services"`
		} `json:"data"`
	}
	mustDecode(t, resp, &body)
	if body.Data.From != "2026-10-01" || body.Data.To != "2026-10-31" {
		t.Errorf("window = %s..%s", body.Data.From, body.Data.To)
	}
	if body.Data.Items == nil || body.Data.Services == nil {
		t.Errorf("items / services must be arrays, got %+v", body.Data)
	}
}

func TestUpcoming_RangeTooWideIs400(t *testing.T) {
	ts := newUpcomingServer(t)
	resp := ts.do("GET", "/api/v1/upcoming?from=2026-10-01&to=2027-01-01", ts.userToken(), nil)
	assertStatus(t, resp, http.StatusBadRequest)
}
