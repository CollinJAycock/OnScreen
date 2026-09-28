// uat_trickplay_test.go — router-level gates for the per-library trickplay
// (seek-bar thumbnail) endpoints.
package uat

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// stubTrickplayPlanner satisfies v1.TrickplayPlanner.
type stubTrickplayPlanner struct{}

func (stubTrickplayPlanner) QueueLibrary(context.Context, uuid.UUID, bool) (int, error) {
	return 3, nil
}

func (stubTrickplayPlanner) LibraryCounts(context.Context, uuid.UUID) (trickplay.LibraryCounts, error) {
	return trickplay.NewLibraryCounts(5, 2, 1), nil
}

func (stubTrickplayPlanner) QueueItem(uuid.UUID) {}

// stubTrickplayLibs satisfies v1.TrickplayLibraryLookup.
type stubTrickplayLibs struct{}

func (stubTrickplayLibs) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	return &library.Library{ID: id, Name: "Movies", Type: "movie", TrickplayEnabled: true}, nil
}

func newTrickplayLibraryServer(t *testing.T) *testServer {
	return newExtrasServer(t, func(h *api.Handlers) {
		h.Trickplay = v1.NewTrickplayHandler(&stubTrickplayService{}, &stubTrickplayMedia{}, slog.Default()).
			WithPlanner(stubTrickplayPlanner{}, stubTrickplayLibs{})
	})
}

// TestTrickplayLibrary_RouteAdminOnly proves both per-library trickplay
// routes sit behind AdminRequired: anonymous → 401, a regular user → 403,
// an admin → 200/202 with the documented shapes.
func TestTrickplayLibrary_RouteAdminOnly(t *testing.T) {
	ts := newTrickplayLibraryServer(t)
	base := "/api/v1/libraries/" + uuid.New().String() + "/trickplay"
	routes := []struct {
		method, path string
		adminWant    int
	}{
		{"GET", base + "/status", http.StatusOK},
		{"POST", base + "/generate", http.StatusAccepted},
	}
	for _, rt := range routes {
		resp := ts.do(rt.method, rt.path, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status = %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.userToken(), nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.adminToken(), nil)
		assertStatus(t, resp, rt.adminWant)
		resp.Body.Close()
	}
}

func TestTrickplayLibrary_StatusShape(t *testing.T) {
	ts := newTrickplayLibraryServer(t)
	resp := ts.do("GET", "/api/v1/libraries/"+uuid.New().String()+"/trickplay/status", ts.adminToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data struct {
			Enabled bool  `json:"enabled"`
			Total   int64 `json:"total"`
			Done    int64 `json:"done"`
			Pending int64 `json:"pending"`
			Failed  int64 `json:"failed"`
		} `json:"data"`
	}
	mustDecode(t, resp, &env)
	if !env.Data.Enabled || env.Data.Total != 5 || env.Data.Done != 2 || env.Data.Pending != 2 || env.Data.Failed != 1 {
		t.Errorf("status body = %+v", env.Data)
	}
}

func TestTrickplayLibrary_GenerateShape(t *testing.T) {
	ts := newTrickplayLibraryServer(t)
	resp := ts.do("POST", "/api/v1/libraries/"+uuid.New().String()+"/trickplay/generate", ts.adminToken(), nil)
	assertStatus(t, resp, http.StatusAccepted)
	var env struct {
		Data struct {
			Status string `json:"status"`
			Queued int    `json:"queued"`
		} `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Status != "queued" || env.Data.Queued != 3 {
		t.Errorf("generate body = %+v", env.Data)
	}
}
