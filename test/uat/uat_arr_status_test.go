package uat

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/requests"
)

// ── requests.DB: live download status (migration 00025) ─────────────────────

func (s *stubRequestsDB) ListMediaRequestsForDownloadSync(context.Context, gen.ListMediaRequestsForDownloadSyncParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (s *stubRequestsDB) SetMediaRequestArrItem(context.Context, gen.SetMediaRequestArrItemParams) error {
	return nil
}
func (s *stubRequestsDB) UpdateMediaRequestDownload(context.Context, gen.UpdateMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (s *stubRequestsDB) FailMediaRequestDownload(context.Context, gen.FailMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (s *stubRequestsDB) RecoverMediaRequestDownload(context.Context, gen.RecoverMediaRequestDownloadParams) (int64, error) {
	return 0, nil
}
func (s *stubRequestsDB) ListUsernamesByIDs(context.Context, []uuid.UUID) ([]gen.ListUsernamesByIDsRow, error) {
	return nil, nil
}

var _ requests.DB = (*stubRequestsDB)(nil)

// ── Arr services — health (admin only) ───────────────────────────────────────

// stubHealthArrDB serves one arr service pointing at a fake Radarr.
type stubHealthArrDB struct {
	stubArrServicesDB
	svc gen.ArrService
}

func (s *stubHealthArrDB) GetArrService(context.Context, uuid.UUID) (gen.ArrService, error) {
	return s.svc, nil
}

func TestArrServices_HealthRouteAdminOnly(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/system/status":
			_, _ = io.WriteString(w, `{"appName":"Radarr","version":"5.9.1"}`)
		case "/api/v3/health":
			_, _ = io.WriteString(w, `[{"source":"IndexerStatusCheck","type":"warning","message":"Indexers unavailable"}]`)
		case "/api/v3/diskspace":
			_, _ = io.WriteString(w, `[{"path":"/movies","label":"media","freeSpace":5,"totalSpace":100}]`)
		case "/api/v3/rootfolder":
			_, _ = io.WriteString(w, `[{"id":1,"path":"/movies","freeSpace":5}]`)
		case "/api/v3/queue":
			_, _ = io.WriteString(w, `{"totalRecords":2,"records":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.Close)

	id := uuid.New()
	ts := newExtrasServer(t, func(h *api.Handlers) {
		db := &stubHealthArrDB{svc: gen.ArrService{ID: id, Name: "Radarr", Kind: "radarr", BaseUrl: fake.URL, ApiKey: "k", Enabled: true}}
		as := v1.NewArrServicesHandler(db, slog.Default())
		as.SetArrClientFactory(func(base, key string) *arr.Client {
			c := arr.New(base, key)
			c.HTTPClient = fake.Client()
			return c
		})
		h.ArrServices = as
	})
	path := "/api/v1/admin/arr-services/" + id.String() + "/health"

	resp := ts.do("GET", path, "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	resp = ts.do("GET", path, ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin: status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = ts.do("GET", path, ts.adminToken(), nil)
	body := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin: status = %d body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"reachable":true`, `"version":"5.9.1"`, `"Indexers unavailable"`, `"free_bytes":5`, `"total_bytes":100`, `"queue_count":2`} {
		if !strings.Contains(body, want) {
			t.Errorf("admin body missing %s: %s", want, body)
		}
	}
}

// TestRequests_ListStillServesWithDownloadFields proves the additive request fields
// don't disturb the listing for a user (no rows → still a 200 list).
func TestRequests_ListStillServesWithDownloadFields(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		svc := requests.NewService(&stubRequestsDB{}, nil, nil, slog.Default())
		h.Requests = v1.NewRequestHandler(svc, slog.Default())
	})
	resp := ts.do("GET", "/api/v1/requests?scope=all", ts.adminToken(), nil)
	if body := readBody(resp); resp.StatusCode != http.StatusOK {
		t.Errorf("admin scope=all: %d %s", resp.StatusCode, body)
	}
}
