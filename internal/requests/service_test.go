package requests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// fakeDB is an in-memory requests.DB: enough state to drive Create through
// the auto-approval path (insert → approve → mark downloading) and to assert
// on the rows it leaves behind.
type fakeDB struct {
	mu       sync.Mutex
	perms    map[uuid.UUID]gen.GetUserRequestPermissionsRow
	permsErr error
	services map[string]gen.ArrService // default service by kind
	byID     map[uuid.UUID]gen.ArrService
	reqs     map[uuid.UUID]gen.MediaRequest

	approveCalls []gen.ApproveMediaRequestParams
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		perms:    map[uuid.UUID]gen.GetUserRequestPermissionsRow{},
		services: map[string]gen.ArrService{},
		byID:     map[uuid.UUID]gen.ArrService{},
		reqs:     map[uuid.UUID]gen.MediaRequest{},
	}
}

func (f *fakeDB) addService(svc gen.ArrService) {
	f.byID[svc.ID] = svc
	if svc.IsDefault {
		f.services[svc.Kind] = svc
	}
}

func (f *fakeDB) GetArrService(_ context.Context, id uuid.UUID) (gen.ArrService, error) {
	if svc, ok := f.byID[id]; ok {
		return svc, nil
	}
	return gen.ArrService{}, pgx.ErrNoRows
}
func (f *fakeDB) GetDefaultArrServiceByKind(_ context.Context, kind string) (gen.ArrService, error) {
	if svc, ok := f.services[kind]; ok {
		return svc, nil
	}
	return gen.ArrService{}, pgx.ErrNoRows
}
func (f *fakeDB) GetUserRequestPermissions(_ context.Context, id uuid.UUID) (gen.GetUserRequestPermissionsRow, error) {
	if f.permsErr != nil {
		return gen.GetUserRequestPermissionsRow{}, f.permsErr
	}
	p, ok := f.perms[id]
	if !ok {
		return gen.GetUserRequestPermissionsRow{}, pgx.ErrNoRows
	}
	return p, nil
}
func (f *fakeDB) CreateMediaRequest(_ context.Context, p gen.CreateMediaRequestParams) (gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := gen.MediaRequest{
		ID:                 uuid.New(),
		UserID:             p.UserID,
		Type:               p.Type,
		TmdbID:             p.TmdbID,
		Title:              p.Title,
		Year:               p.Year,
		Status:             p.Status,
		Seasons:            p.Seasons,
		RequestedServiceID: p.RequestedServiceID,
		QualityProfileID:   p.QualityProfileID,
		RootFolder:         p.RootFolder,
	}
	f.reqs[r.ID] = r
	return r, nil
}
func (f *fakeDB) GetMediaRequest(_ context.Context, id uuid.UUID) (gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.reqs[id]; ok {
		return r, nil
	}
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *fakeDB) FindActiveRequestForUser(_ context.Context, _ gen.FindActiveRequestForUserParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *fakeDB) FindActiveRequestsForUserByTMDB(_ context.Context, _ gen.FindActiveRequestsForUserByTMDBParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *fakeDB) ListMediaRequestsForUser(_ context.Context, _ gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *fakeDB) CountMediaRequestsForUser(_ context.Context, _ gen.CountMediaRequestsForUserParams) (int64, error) {
	return 0, nil
}
func (f *fakeDB) ListAllMediaRequests(_ context.Context, _ gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *fakeDB) CountAllMediaRequests(_ context.Context, _ *string) (int64, error) { return 0, nil }
func (f *fakeDB) ListActiveMediaRequestsForTMDB(_ context.Context, _ gen.ListActiveMediaRequestsForTMDBParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *fakeDB) ListMediaItemsByTMDBIDs(_ context.Context, _ gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}
func (f *fakeDB) ApproveMediaRequest(_ context.Context, p gen.ApproveMediaRequestParams) (gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approveCalls = append(f.approveCalls, p)
	r, ok := f.reqs[p.ID]
	if !ok || r.Status != StatusPending {
		return gen.MediaRequest{}, pgx.ErrNoRows
	}
	r.Status = StatusApproved
	r.ServiceID = p.ServiceID
	r.DecidedBy = p.DecidedBy
	r.AutoApproved = p.AutoApproved
	r.DecidedAt = pgtype.Timestamptz{Valid: true}
	f.reqs[r.ID] = r
	return r, nil
}
func (f *fakeDB) DeclineMediaRequest(_ context.Context, _ gen.DeclineMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *fakeDB) MarkMediaRequestDownloading(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.reqs[id]; ok && r.Status == StatusApproved {
		r.Status = StatusDownloading
		f.reqs[id] = r
	}
	return nil
}
func (f *fakeDB) MarkMediaRequestAvailable(_ context.Context, _ gen.MarkMediaRequestAvailableParams) error {
	return nil
}
func (f *fakeDB) MarkMediaRequestFailed(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeDB) CancelMediaRequest(_ context.Context, _ gen.CancelMediaRequestParams) error {
	return nil
}
func (f *fakeDB) DeleteMediaRequest(_ context.Context, _ uuid.UUID) error { return nil }

type fakeTMDB struct{}

func (fakeTMDB) RefreshMovie(_ context.Context, id int) (*metadata.MovieResult, error) {
	return &metadata.MovieResult{TMDBID: id, Title: "Heat", Year: 1995}, nil
}
func (fakeTMDB) RefreshTV(_ context.Context, id int) (*metadata.TVShowResult, error) {
	return &metadata.TVShowResult{TMDBID: id, Title: "Severance", FirstAirYear: 2022}, nil
}
func (fakeTMDB) GetTVExternalIDs(_ context.Context, _ int) (int, string, error) {
	return 371980, "", nil
}

type notice struct{ typ, title, body string }

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notice
}

func (n *fakeNotifier) Notify(_ context.Context, _ uuid.UUID, typ, title, body string, _ *uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, notice{typ, title, body})
}

// fakeArr stands in for Radarr and Sonarr. fail makes every add return 500,
// the "reachable but rejected the add" case.
type fakeArr struct {
	mu    sync.Mutex
	fail  bool
	adds  []string // paths of the POSTs it accepted or refused
	calls int
}

func (a *fakeArr) handler(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.calls++
	fail := a.fail
	if r.Method == http.MethodPost {
		a.adds = append(a.adds, r.URL.Path)
	}
	a.mu.Unlock()
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup":
		_ = json.NewEncoder(w).Encode([]map[string]any{{"title": "Heat", "tmdbId": 949, "year": 1995, "titleSlug": "heat-949"}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup":
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"title": "Severance", "tvdbId": 371980, "year": 2022, "titleSlug": "severance",
			"seasons": []map[string]any{{"seasonNumber": 1, "monitored": true}},
		}})
	case r.Method == http.MethodPost && fail:
		http.Error(w, `[{"errorMessage":"path is not writable"}]`, http.StatusInternalServerError)
	case r.Method == http.MethodPost:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
	default:
		http.NotFound(w, r)
	}
}

type harness struct {
	svc    *Service
	db     *fakeDB
	notify *fakeNotifier
	arr    *fakeArr
}

// newHarness wires a Service to the fakes with a default Radarr and Sonarr,
// both pointing at one fake arr server.
func newHarness(t *testing.T) *harness {
	t.Helper()
	fa := &fakeArr{}
	srv := httptest.NewServer(http.HandlerFunc(fa.handler))
	t.Cleanup(srv.Close)

	db := newFakeDB()
	qp := int32(4)
	rf := "/data/media"
	for _, kind := range []string{"radarr", "sonarr"} {
		db.addService(gen.ArrService{
			ID: uuid.New(), Name: kind, Kind: kind, BaseUrl: srv.URL, ApiKey: "k",
			DefaultQualityProfileID: &qp, DefaultRootFolder: &rf,
			IsDefault: true, Enabled: true,
		})
	}
	n := &fakeNotifier{}
	svc := NewService(db, fakeTMDB{}, n, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &harness{svc: svc, db: db, notify: n, arr: fa}
}

func (h *harness) user(p gen.GetUserRequestPermissionsRow) uuid.UUID {
	id := uuid.New()
	h.db.perms[id] = p
	return id
}

func ptr[T any](v T) *T { return &v }

// ── eligibility ─────────────────────────────────────────────────────────────

func TestCreate_AutoApproveEligibilityMatrix(t *testing.T) {
	cases := []struct {
		name     string
		perms    gen.GetUserRequestPermissionsRow
		typ      string
		wantAuto bool
	}{
		{"admin, toggles off", gen.GetUserRequestPermissionsRow{IsAdmin: true}, TypeMovie, true},
		{"admin show, toggles off", gen.GetUserRequestPermissionsRow{IsAdmin: true}, TypeShow, true},
		{"movie toggle on, movie", gen.GetUserRequestPermissionsRow{AutoApproveMovies: true}, TypeMovie, true},
		{"movie toggle on, show", gen.GetUserRequestPermissionsRow{AutoApproveMovies: true}, TypeShow, false},
		{"tv toggle on, show", gen.GetUserRequestPermissionsRow{AutoApproveTv: true}, TypeShow, true},
		{"tv toggle on, movie", gen.GetUserRequestPermissionsRow{AutoApproveTv: true}, TypeMovie, false},
		{"both off", gen.GetUserRequestPermissionsRow{}, TypeMovie, false},
		{"capped, both on, movie", gen.GetUserRequestPermissionsRow{
			MaxContentRating: ptr("PG-13"), AutoApproveMovies: true, AutoApproveTv: true}, TypeMovie, false},
		{"capped, both on, show", gen.GetUserRequestPermissionsRow{
			MaxContentRating: ptr("TV-PG"), AutoApproveMovies: true, AutoApproveTv: true}, TypeShow, false},
		// A stored "" becomes an empty claim, which rating enforcement treats
		// as no ceiling; the auto-approval veto must agree with it.
		{"empty ceiling, movie on", gen.GetUserRequestPermissionsRow{
			MaxContentRating: ptr(""), AutoApproveMovies: true}, TypeMovie, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			uid := h.user(tc.perms)
			got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: tc.typ, TMDBID: 949})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if got.AutoApproved != tc.wantAuto {
				t.Errorf("AutoApproved = %v, want %v", got.AutoApproved, tc.wantAuto)
			}
			wantStatus := StatusPending
			if tc.wantAuto {
				wantStatus = StatusDownloading
			}
			if got.Status != wantStatus {
				t.Errorf("status = %q, want %q", got.Status, wantStatus)
			}
			if !tc.wantAuto && len(h.arr.adds) != 0 {
				t.Errorf("arr add sent for a queued request: %v", h.arr.adds)
			}
		})
	}
}

// The requester's policy comes from the DB, never the caller: an unknown
// user (or a lookup failure) must fall back to the queue.
func TestCreate_PermissionLookupFailureQueues(t *testing.T) {
	h := newHarness(t)
	h.db.permsErr = errors.New("db down")
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uuid.New(), Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Status != StatusPending || got.AutoApproved {
		t.Errorf("got status=%q auto=%v, want pending/false", got.Status, got.AutoApproved)
	}
	if h.arr.calls != 0 {
		t.Errorf("arr contacted %d times on a lookup failure", h.arr.calls)
	}
}

// A non-admin's explicit service choice is a preference for the approving
// admin; with auto-approval on it still goes to the queue. An admin's choice
// is honoured.
func TestCreate_RequestedServiceSkipsAutoApproveForNonAdmin(t *testing.T) {
	h := newHarness(t)
	svcID := h.db.services["radarr"].ID

	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true})
	got, err := h.svc.Create(context.Background(), CreateInput{
		UserID: uid, Type: TypeMovie, TMDBID: 949, RequestedServiceID: &svcID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Status != StatusPending || got.AutoApproved {
		t.Errorf("user with service pick: status=%q auto=%v, want pending/false", got.Status, got.AutoApproved)
	}

	admin := h.user(gen.GetUserRequestPermissionsRow{IsAdmin: true})
	got, err = h.svc.Create(context.Background(), CreateInput{
		UserID: admin, Type: TypeMovie, TMDBID: 949, RequestedServiceID: &svcID,
	})
	if err != nil {
		t.Fatalf("Create (admin): %v", err)
	}
	if !got.AutoApproved {
		t.Errorf("admin with service pick: auto=%v, want true", got.AutoApproved)
	}
}

// ── success path ────────────────────────────────────────────────────────────

func TestCreate_AutoApproveSuccess(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveTv: true})

	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 95396})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Status != StatusDownloading || !got.AutoApproved {
		t.Fatalf("returned status=%q auto=%v, want downloading/true", got.Status, got.AutoApproved)
	}
	// The stored row agrees with what was returned.
	stored := h.db.reqs[got.ID]
	if stored.Status != StatusDownloading || !stored.AutoApproved {
		t.Errorf("stored status=%q auto=%v, want downloading/true", stored.Status, stored.AutoApproved)
	}
	// Recorded as automatic: flag set in the approve UPDATE, no admin named.
	if len(h.db.approveCalls) != 1 {
		t.Fatalf("approve calls = %d, want 1", len(h.db.approveCalls))
	}
	call := h.db.approveCalls[0]
	if !call.AutoApproved {
		t.Error("ApproveMediaRequest.AutoApproved = false, want true")
	}
	if call.DecidedBy.Valid {
		t.Errorf("DecidedBy = %v, want NULL for an automatic approval", call.DecidedBy)
	}
	if call.ServiceID.Bytes != h.db.services["sonarr"].ID {
		t.Errorf("service = %v, want the default sonarr", call.ServiceID)
	}
	if len(h.arr.adds) != 1 || h.arr.adds[0] != "/api/v3/series" {
		t.Errorf("arr adds = %v, want one POST /api/v3/series", h.arr.adds)
	}
	// Exactly one notice, saying it was automatic — not "awaiting approval".
	if len(h.notify.sent) != 1 {
		t.Fatalf("notifications = %+v, want exactly one", h.notify.sent)
	}
	n := h.notify.sent[0]
	if n.typ != "request_approved" || !strings.Contains(n.body, "approved automatically") {
		t.Errorf("notification = %+v, want request_approved saying 'approved automatically'", n)
	}
	if strings.Contains(n.body, "awaiting") {
		t.Errorf("auto-approved notification still says awaiting: %q", n.body)
	}
}

// ── failure paths ───────────────────────────────────────────────────────────

func TestCreate_AutoApproveFailureStaysPending(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness)
	}{
		{"arr rejects the add", func(h *harness) { h.arr.fail = true }},
		{"no arr service configured", func(h *harness) { delete(h.db.services, "radarr") }},
		{"arr unreachable", func(h *harness) {
			svc := h.db.services["radarr"]
			svc.BaseUrl = "http://127.0.0.1:1" // nothing listens on port 1
			h.db.services["radarr"] = svc
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true})

			got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
			if err != nil {
				t.Fatalf("Create must still succeed when auto-approval fails, got %v", err)
			}
			if got.Status != StatusPending || got.AutoApproved {
				t.Errorf("returned status=%q auto=%v, want pending/false", got.Status, got.AutoApproved)
			}
			stored := h.db.reqs[got.ID]
			if stored.Status != StatusPending || stored.AutoApproved {
				t.Errorf("stored status=%q auto=%v, want pending/false", stored.Status, stored.AutoApproved)
			}
			if len(h.db.approveCalls) != 0 {
				t.Errorf("row was transitioned despite the failed send: %+v", h.db.approveCalls)
			}
			// Same notice as a queued request today.
			if len(h.notify.sent) != 1 || h.notify.sent[0].typ != "request_created" ||
				!strings.Contains(h.notify.sent[0].body, "awaiting admin approval") {
				t.Errorf("notifications = %+v, want one request_created 'awaiting admin approval'", h.notify.sent)
			}
		})
	}
}

// A queued request keeps today's notice text.
func TestCreate_QueuedNotificationUnchanged(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := notice{"request_created", "Request submitted", `"Heat" is awaiting admin approval.`}
	if len(h.notify.sent) != 1 || h.notify.sent[0] != want {
		t.Errorf("notifications = %+v, want [%+v]", h.notify.sent, want)
	}
}

// ── manual approve is unchanged ─────────────────────────────────────────────

func TestApprove_ManualRecordsAdminNotAuto(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	adminID := uuid.New()
	got, err := h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: adminID})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.AutoApproved {
		t.Error("manual approval marked auto_approved")
	}
	call := h.db.approveCalls[0]
	if call.AutoApproved || !call.DecidedBy.Valid || call.DecidedBy.Bytes != adminID {
		t.Errorf("approve params = %+v, want the admin id and auto=false", call)
	}
	last := h.notify.sent[len(h.notify.sent)-1]
	if last.body != `"Heat" is being downloaded.` {
		t.Errorf("manual approval notice = %q, want the unchanged text", last.body)
	}
}
