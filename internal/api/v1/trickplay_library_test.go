package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/trickplay"
)

type fakeTrickplayPlanner struct {
	queued        int
	queueErr      error
	queuedLib     uuid.UUID
	includeFailed bool
	counts        trickplay.LibraryCounts
	countsErr     error
	items         []uuid.UUID
}

func (f *fakeTrickplayPlanner) QueueLibrary(_ context.Context, id uuid.UUID, includeFailed bool) (int, error) {
	f.queuedLib = id
	f.includeFailed = includeFailed
	return f.queued, f.queueErr
}

func (f *fakeTrickplayPlanner) LibraryCounts(_ context.Context, _ uuid.UUID) (trickplay.LibraryCounts, error) {
	return f.counts, f.countsErr
}

func (f *fakeTrickplayPlanner) QueueItem(id uuid.UUID) { f.items = append(f.items, id) }

type fakeTrickplayLibs struct {
	lib *library.Library
	err error
}

func (f *fakeTrickplayLibs) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	if f.err != nil {
		return nil, f.err
	}
	l := *f.lib
	l.ID = id
	return &l, nil
}

func tpAdminClaims() *auth.Claims { return &auth.Claims{UserID: uuid.New(), IsAdmin: true} }

func tpWithClaims(r *http.Request, c *auth.Claims) context.Context {
	return middleware.WithClaims(r.Context(), c)
}

func tpLibHandler(p *fakeTrickplayPlanner, libs *fakeTrickplayLibs) *TrickplayHandler {
	return NewTrickplayHandler(&fakeTrickplayService{}, &fakeTrickplayMedia{item: tpItem()}, silentLogger()).
		WithPlanner(p, libs)
}

// ── per-item status/generate envelope ──────────────────────────────────────

// The web client and both Android apps read `.data`; the bare top-level
// fields stay for anything that learned the old shape.
func TestTrickplay_Status_CarriesDataEnvelopeAndBareFields(t *testing.T) {
	svc := &fakeTrickplayService{statusOK: true, statusKind: "done", statusCount: 3,
		statusSpec: trickplay.Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180}}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	rec := httptest.NewRecorder()
	h.Status(rec, tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var body struct {
		Data   TrickplayStatusJSON `json:"data"`
		Status string              `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Status != "done" || body.Data.SpriteCount != 3 || body.Data.IntervalSec != 10 {
		t.Errorf("data envelope = %+v; body=%s", body.Data, rec.Body.String())
	}
	if body.Status != "done" {
		t.Errorf("legacy top-level status = %q", body.Status)
	}
}

func TestTrickplay_Generate_WithPlannerQueuesInsteadOfSpawning(t *testing.T) {
	svc := &fakeTrickplayService{}
	p := &fakeTrickplayPlanner{}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger()).
		WithPlanner(p, &fakeTrickplayLibs{lib: &library.Library{}})
	id := uuid.New()
	rec := httptest.NewRecorder()
	h.Generate(rec, tpReq(http.MethodPost, "/", tpAdminClaims(), id.String(), ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.items) != 1 || p.items[0] != id {
		t.Errorf("queued items = %v, want [%s]", p.items, id)
	}
	if svc.generated.Load() != 0 {
		t.Error("with a planner wired the handler must not run its own generation")
	}
	var body struct {
		Data TrickplayStatusJSON `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data.Status != "pending" {
		t.Errorf("body = %s (err %v)", rec.Body.String(), err)
	}
}

// ── GET /libraries/{id}/trickplay/status ───────────────────────────────────

func TestTrickplay_LibraryStatus_Shape(t *testing.T) {
	p := &fakeTrickplayPlanner{counts: trickplay.NewLibraryCounts(10, 6, 1)}
	h := tpLibHandler(p, &fakeTrickplayLibs{lib: &library.Library{TrickplayEnabled: true}})
	rec := httptest.NewRecorder()
	h.LibraryStatus(rec, tpReq(http.MethodGet, "/", tpAdminClaims(), uuid.New().String(), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"enabled": true, "total": 10.0, "done": 6.0, "pending": 3.0, "failed": 1.0}
	if len(body.Data) != len(want) {
		t.Errorf("fields = %v, want exactly %v", body.Data, want)
	}
	for k, v := range want {
		if body.Data[k] != v {
			t.Errorf("%s = %v, want %v", k, body.Data[k], v)
		}
	}
}

func TestTrickplay_LibraryEndpoints_Gates(t *testing.T) {
	libID := uuid.New().String()
	cases := []struct {
		name   string
		h      *TrickplayHandler
		claims *auth.Claims
		id     string
		want   int
	}{
		{"non-admin", tpLibHandler(&fakeTrickplayPlanner{}, &fakeTrickplayLibs{lib: &library.Library{}}),
			&auth.Claims{UserID: uuid.New()}, libID, http.StatusForbidden},
		{"no claims", tpLibHandler(&fakeTrickplayPlanner{}, &fakeTrickplayLibs{lib: &library.Library{}}),
			nil, libID, http.StatusForbidden},
		{"bad id", tpLibHandler(&fakeTrickplayPlanner{}, &fakeTrickplayLibs{lib: &library.Library{}}),
			tpAdminClaims(), "nope", http.StatusBadRequest},
		{"unknown library", tpLibHandler(&fakeTrickplayPlanner{}, &fakeTrickplayLibs{err: library.ErrNotFound}),
			tpAdminClaims(), libID, http.StatusNotFound},
		{"library lookup error", tpLibHandler(&fakeTrickplayPlanner{}, &fakeTrickplayLibs{err: errors.New("db")}),
			tpAdminClaims(), libID, http.StatusInternalServerError},
		{"planner not wired", NewTrickplayHandler(&fakeTrickplayService{}, &fakeTrickplayMedia{}, silentLogger()),
			tpAdminClaims(), libID, http.StatusNotFound},
	}
	for _, tc := range cases {
		for _, ep := range []struct {
			method string
			fn     func(http.ResponseWriter, *http.Request)
		}{
			{http.MethodGet, tc.h.LibraryStatus},
			{http.MethodPost, tc.h.GenerateLibrary},
		} {
			rec := httptest.NewRecorder()
			ep.fn(rec, tpReq(ep.method, "/", tc.claims, tc.id, ""))
			if rec.Code != tc.want {
				t.Errorf("%s %s: got %d, want %d", tc.name, ep.method, rec.Code, tc.want)
			}
		}
	}
}

func TestTrickplay_LibraryStatus_CountsError(t *testing.T) {
	h := tpLibHandler(&fakeTrickplayPlanner{countsErr: errors.New("db")}, &fakeTrickplayLibs{lib: &library.Library{}})
	rec := httptest.NewRecorder()
	h.LibraryStatus(rec, tpReq(http.MethodGet, "/", tpAdminClaims(), uuid.New().String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}

// ── POST /libraries/{id}/trickplay/generate ────────────────────────────────

func TestTrickplay_GenerateLibrary_Accepted(t *testing.T) {
	p := &fakeTrickplayPlanner{queued: 7}
	h := tpLibHandler(p, &fakeTrickplayLibs{lib: &library.Library{}})
	libID := uuid.New()
	rec := httptest.NewRecorder()
	h.GenerateLibrary(rec, tpReq(http.MethodPost, "/", tpAdminClaims(), libID.String(), ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d; body=%s", rec.Code, rec.Body.String())
	}
	if p.queuedLib != libID || p.includeFailed {
		t.Errorf("QueueLibrary(%s, includeFailed=%v)", p.queuedLib, p.includeFailed)
	}
	var body struct {
		Data struct {
			Status string `json:"status"`
			Queued int    `json:"queued"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Status != "queued" || body.Data.Queued != 7 {
		t.Errorf("body = %+v", body.Data)
	}
}

func TestTrickplay_GenerateLibrary_IncludeFailedAndDisabledLibrary(t *testing.T) {
	p := &fakeTrickplayPlanner{}
	// An explicit request works even with the automatic toggle off.
	h := tpLibHandler(p, &fakeTrickplayLibs{lib: &library.Library{TrickplayEnabled: false}})
	rec := httptest.NewRecorder()
	h.GenerateLibrary(rec, tpReq(http.MethodPost, "/?include_failed=true", tpAdminClaims(), uuid.New().String(), ""))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d", rec.Code)
	}
	if !p.includeFailed {
		t.Error("include_failed=true not passed through")
	}
}

// ── library create / update / get carry trickplay_enabled ──────────────────

type captureLibService struct {
	mockLibraryService
	createP *library.CreateLibraryParams
	updateP *library.UpdateLibraryParams
}

func (c *captureLibService) Create(_ context.Context, p library.CreateLibraryParams) (*library.Library, error) {
	c.createP = &p
	return &library.Library{ID: uuid.New(), Name: p.Name, Type: p.Type,
		TrickplayEnabled: p.TrickplayEnabled != nil && *p.TrickplayEnabled}, nil
}

func (c *captureLibService) Update(_ context.Context, p library.UpdateLibraryParams) (*library.Library, error) {
	c.updateP = &p
	return &library.Library{ID: p.ID, Name: p.Name,
		TrickplayEnabled: p.TrickplayEnabled != nil && *p.TrickplayEnabled}, nil
}

func TestLibrary_Create_TrickplayEnabledPassThrough(t *testing.T) {
	for _, tc := range []struct {
		body string
		want *bool
	}{
		{`{"name":"M","type":"movie","scan_paths":["/media/m"]}`, nil},
		{`{"name":"M","type":"movie","scan_paths":["/media/m"],"trickplay_enabled":false}`, new(bool)},
	} {
		svc := &captureLibService{}
		h := NewLibraryHandler(svc, silentLogger())
		rec := httptest.NewRecorder()
		h.Create(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s: got %d %s", tc.body, rec.Code, rec.Body.String())
		}
		got := svc.createP.TrickplayEnabled
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: TrickplayEnabled = %v, want %v (nil = per-type default)", tc.body, got, tc.want)
		}
	}
}

func TestLibrary_Update_TrickplayEnabledPatchSemantics(t *testing.T) {
	id := uuid.New()
	existing := &library.Library{ID: id, Name: "M", Paths: []string{"/m"}, TrickplayEnabled: true}

	svc := &captureLibService{mockLibraryService: mockLibraryService{lib: existing}}
	h := NewLibraryHandler(svc, silentLogger())
	rec := httptest.NewRecorder()
	h.Update(rec, withChiParam(httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"trickplay_enabled":false}`)), "id", id.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if p := svc.updateP.TrickplayEnabled; p == nil || *p {
		t.Errorf("PATCH trickplay_enabled=false passed %v", p)
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if v, ok := body.Data["trickplay_enabled"]; !ok || v != false {
		t.Errorf("response trickplay_enabled = %v (present=%v)", v, ok)
	}

	// Omitted = preserve (nil → COALESCE keeps the stored value).
	svc2 := &captureLibService{mockLibraryService: mockLibraryService{lib: existing}}
	rec = httptest.NewRecorder()
	NewLibraryHandler(svc2, silentLogger()).Update(rec, withChiParam(httptest.NewRequest(http.MethodPatch, "/",
		strings.NewReader(`{"name":"Renamed"}`)), "id", id.String()))
	if rec.Code != http.StatusOK || svc2.updateP.TrickplayEnabled != nil {
		t.Errorf("rename-only PATCH: code=%d TrickplayEnabled=%v, want nil", rec.Code, svc2.updateP.TrickplayEnabled)
	}
}

func TestLibrary_Get_ExposesTrickplayEnabled(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{lib: &library.Library{ID: id, Name: "M", TrickplayEnabled: true}}
	h := NewLibraryHandler(svc, silentLogger())
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest(http.MethodGet, "/", nil), "id", id.String())
	h.Get(rec, req.WithContext(tpWithClaims(req, &auth.Claims{UserID: uuid.New()})))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"trickplay_enabled":true`) {
		t.Errorf("body missing trickplay_enabled: %s", rec.Body.String())
	}
}

func TestTrickplay_GenerateLibrary_QueueError(t *testing.T) {
	h := tpLibHandler(&fakeTrickplayPlanner{queueErr: errors.New("db")}, &fakeTrickplayLibs{lib: &library.Library{}})
	rec := httptest.NewRecorder()
	h.GenerateLibrary(rec, tpReq(http.MethodPost, "/", tpAdminClaims(), uuid.New().String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}
