package requests

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

	// now stamps created_at on inserted rows (the harness shares its clock
	// with the service so quota windows line up).
	now func() time.Time
	// countErr fails CountRecentMediaRequestsForUser.
	countErr error
	// sinceSeen records the window start the service asked about.
	sinceSeen []time.Time
	// countPending is what CountMediaRequestsForUser (the flood cap) returns.
	countPending int64
	// createErr fails CreateMediaRequest (e.g. a unique violation from a
	// concurrent duplicate insert).
	createErr error

	// owned is what ListOwnedEpisodeCountsByShowTMDB reports (seasons_test.go).
	owned []gen.ListOwnedEpisodeCountsByShowTMDBRow
	// libraryItems is what ListMediaItemsByTMDBIDs reports.
	libraryItems []gen.ListMediaItemsByTMDBIDsRow
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		perms:    map[uuid.UUID]gen.GetUserRequestPermissionsRow{},
		services: map[string]gen.ArrService{},
		byID:     map[uuid.UUID]gen.ArrService{},
		reqs:     map[uuid.UUID]gen.MediaRequest{},
		now:      time.Now,
	}
}

// seed stores a pre-existing request (e.g. an older one inside or outside the
// quota window).
func (f *fakeDB) seed(userID uuid.UUID, typ, status string, createdAt time.Time) gen.MediaRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := gen.MediaRequest{
		ID: uuid.New(), UserID: userID, Type: typ, TmdbID: int32(len(f.reqs) + 5000),
		Title: "seed", Status: status, CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
	f.reqs[r.ID] = r
	return r
}

// CountRecentMediaRequestsForUser mirrors the SQL: same user and type,
// created strictly after since, not declined.
func (f *fakeDB) CountRecentMediaRequestsForUser(_ context.Context, p gen.CountRecentMediaRequestsForUserParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sinceSeen = append(f.sinceSeen, p.Since.Time)
	if f.countErr != nil {
		return 0, f.countErr
	}
	var n int64
	for _, r := range f.reqs {
		if r.UserID == p.UserID && r.Type == p.Type && r.CreatedAt.Time.After(p.Since.Time) && r.Status != StatusDeclined {
			n++
		}
	}
	return n, nil
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
	if f.createErr != nil {
		return gen.MediaRequest{}, f.createErr
	}
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
		CreatedAt:          pgtype.Timestamptz{Time: f.now(), Valid: true},
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

// FindActiveRequestsForUserByTMDB mirrors the SQL: the user's pending /
// approved / downloading requests whose tmdb_id is in the list.
func (f *fakeDB) FindActiveRequestsForUserByTMDB(_ context.Context, p gen.FindActiveRequestsForUserByTMDBParams) ([]gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.MediaRequest
	for _, r := range f.reqs {
		if r.UserID != p.UserID || (r.Status != StatusPending && r.Status != StatusApproved && r.Status != StatusDownloading) {
			continue
		}
		for _, id := range p.TmdbIds {
			if r.TmdbID == id {
				out = append(out, r)
			}
		}
	}
	return out, nil
}
func (f *fakeDB) ListMediaRequestsForUser(_ context.Context, _ gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *fakeDB) CountMediaRequestsForUser(_ context.Context, _ gen.CountMediaRequestsForUserParams) (int64, error) {
	return f.countPending, nil
}
func (f *fakeDB) ListAllMediaRequests(_ context.Context, p gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.MediaRequest
	for _, r := range f.reqs {
		if p.Status == nil || r.Status == *p.Status {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeDB) CountAllMediaRequests(_ context.Context, status *string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.reqs {
		if status == nil || r.Status == *status {
			n++
		}
	}
	return n, nil
}
func (f *fakeDB) ListActiveMediaRequestsForTMDB(_ context.Context, p gen.ListActiveMediaRequestsForTMDBParams) ([]gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.MediaRequest
	for _, r := range f.reqs {
		if r.Type == p.Type && r.TmdbID == p.TmdbID && (r.Status == StatusApproved || r.Status == StatusDownloading) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeDB) ListMediaItemsByTMDBIDs(_ context.Context, p gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.ListMediaItemsByTMDBIDsRow
	for _, row := range f.libraryItems {
		for _, id := range p.TmdbIds {
			if row.TmdbID != nil && *row.TmdbID == id {
				out = append(out, row)
			}
		}
	}
	return out, nil
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
func (f *fakeDB) DeclineMediaRequest(_ context.Context, p gen.DeclineMediaRequestParams) (gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reqs[p.ID]
	if !ok || r.Status != StatusPending {
		return gen.MediaRequest{}, pgx.ErrNoRows
	}
	r.Status = StatusDeclined
	r.DeclineReason = p.DeclineReason
	f.reqs[r.ID] = r
	return r, nil
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
func (f *fakeDB) MarkMediaRequestAvailable(_ context.Context, p gen.MarkMediaRequestAvailableParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.reqs[p.ID]; ok && (r.Status == StatusApproved || r.Status == StatusDownloading) {
		r.Status = StatusAvailable
		r.FulfilledItemID = p.FulfilledItemID
		f.reqs[p.ID] = r
	}
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
	// to / items parallel sent: the recipient and item of each notice.
	to    []uuid.UUID
	items []*uuid.UUID
	// adminSent collects NotifyAdmins calls.
	adminSent []notice
}

func (n *fakeNotifier) Notify(_ context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, notice{typ, title, body})
	n.to = append(n.to, userID)
	n.items = append(n.items, itemID)
}

func (n *fakeNotifier) NotifyAdmins(_ context.Context, typ, title, body string, _ *uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.adminSent = append(n.adminSent, notice{typ, title, body})
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
	// now is the fixed clock shared by the service and the fake DB.
	now time.Time
	// defaults is what the service's QuotaDefaultsFunc returns.
	defaults QuotaDefaults
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
	h := &harness{svc: svc, db: db, notify: n, arr: fa,
		now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	svc.now = func() time.Time { return h.now }
	db.now = func() time.Time { return h.now }
	svc.WithQuotaDefaults(func(context.Context) QuotaDefaults { return h.defaults })
	return h
}

// user registers a requester with the given policy. can_request is forced on
// (the column defaults true); use userRow for a blocked account.
func (h *harness) user(p gen.GetUserRequestPermissionsRow) uuid.UUID {
	p.CanRequest = true
	return h.userRow(p)
}

// userRow registers a requester exactly as given.
func (h *harness) userRow(p gen.GetUserRequestPermissionsRow) uuid.UUID {
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

// ── movie already in Radarr ─────────────────────────────────────────────────

// radarrMovieExistsBody is what Radarr really answers (400) when asked to add
// a movie already in its library.
const radarrMovieExistsBody = `[{"propertyName":"TmdbId","errorMessage":"This movie has already been added",
  "attemptedValue":949,"severity":"error","errorCode":"MovieExistsValidator",
  "formattedMessagePlaceholderValues":{"propertyName":"Tmdb Id","propertyValue":949}}]`

// movieRadarr is a fake Radarr for the movie add path. With exists set it
// already manages Heat (movie 7): the tmdbId listing returns it (with raced,
// only once an add was attempted — another add won the race) and an add is
// refused with addStatus / addBody (default: Radarr's real 400 answer).
type movieRadarr struct {
	mu          sync.Mutex
	exists      bool
	raced       bool
	addStatus   int
	addBody     string
	movie       string // GET /api/v3/movie/7 body
	addAttempts int
	put         map[string]any
	commands    []map[string]any
}

func (a *movieRadarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie" && r.URL.Query().Get("tmdbId") == "949":
		if a.exists && (!a.raced || a.addAttempts > 0) {
			_, _ = io.WriteString(w, `[{"id":7,"tmdbId":949,"title":"Heat"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup":
		_, _ = io.WriteString(w, `[{"title":"Heat","tmdbId":949,"year":1995,"titleSlug":"heat-949"}]`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie":
		a.addAttempts++
		if a.exists {
			status, body := a.addStatus, a.addBody
			if status == 0 {
				status, body = http.StatusBadRequest, radarrMovieExistsBody
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":77,"title":"Heat"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/7":
		_, _ = io.WriteString(w, a.movie)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/7":
		_ = json.Unmarshal(raw, &a.put)
		_, _ = w.Write(raw)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		a.commands = append(a.commands, body)
		_, _ = io.WriteString(w, `{"id":5}`)
	default:
		http.NotFound(w, r)
	}
}

func pointRadarrAt(t *testing.T, h *harness, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc := h.db.services["radarr"]
	svc.BaseUrl = srv.URL
	h.db.addService(svc)
}

// approveMovie creates a (queued) request for Heat and approves it.
func approveMovie(t *testing.T, h *harness, uid uuid.UUID) (gen.MediaRequest, error) {
	t.Helper()
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: uuid.New()})
}

var moviesSearch7 = []map[string]any{{"name": "MoviesSearch", "movieIds": []any{float64(7)}}}

// A movie Radarr already has isn't re-added (Radarr would refuse it): it is
// switched to monitored — the rest of the movie sent back untouched — and
// searched, and the request is approved and linked to it.
func TestApprove_ExistingMovieMonitoredAndSearched(t *testing.T) {
	h := newHarness(t)
	fr := &movieRadarr{exists: true, movie: `{"id":7,"tmdbId":949,"title":"Heat","monitored":false,"hasFile":false,
	  "path":"/movies/Heat (1995)","qualityProfileId":9,"tags":[3]}`}
	pointRadarrAt(t, h, fr)

	got, err := approveMovie(t, h, h.user(gen.GetUserRequestPermissionsRow{}))
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.Status != StatusDownloading || got.ArrItemID == nil || *got.ArrItemID != 7 {
		t.Fatalf("status = %q arr item = %v, want downloading linked to movie 7", got.Status, got.ArrItemID)
	}
	if fr.addAttempts != 0 {
		t.Errorf("add attempts = %d; a movie Radarr already has must not be re-added", fr.addAttempts)
	}
	if fr.put == nil || fr.put["monitored"] != true {
		t.Fatalf("PUT = %v, want the movie switched to monitored", fr.put)
	}
	if fr.put["path"] != "/movies/Heat (1995)" || fr.put["qualityProfileId"] != float64(9) || fr.put["title"] != "Heat" {
		t.Errorf("PUT lost movie fields: %v", fr.put)
	}
	if !reflect.DeepEqual(fr.commands, moviesSearch7) {
		t.Errorf("commands = %v, want %v", fr.commands, moviesSearch7)
	}
}

// Re-requesting a movie whose download failed: Radarr still has it, monitored
// and missing. The new request is approved (no add, no PUT) and a search sent.
func TestApprove_ReRequestAfterFailureSearchesExistingMovie(t *testing.T) {
	h := newHarness(t)
	fr := &movieRadarr{exists: true, movie: `{"id":7,"tmdbId":949,"title":"Heat","monitored":true,"hasFile":false}`}
	pointRadarrAt(t, h, fr)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	old := h.db.seed(uid, TypeMovie, StatusFailed, h.now.Add(-24*time.Hour))
	old.TmdbID = 949
	h.db.reqs[old.ID] = old

	got, err := approveMovie(t, h, uid)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.Status != StatusDownloading || got.ArrItemID == nil || *got.ArrItemID != 7 {
		t.Fatalf("status = %q arr item = %v, want downloading linked to movie 7", got.Status, got.ArrItemID)
	}
	if fr.addAttempts != 0 || fr.put != nil {
		t.Errorf("add attempts = %d, PUT = %v; want neither for a monitored movie", fr.addAttempts, fr.put)
	}
	if !reflect.DeepEqual(fr.commands, moviesSearch7) {
		t.Errorf("commands = %v, want %v", fr.commands, moviesSearch7)
	}
}

// With the file already in Radarr a search could only grab an upgrade: the
// request is approved and linked, nothing searched.
func TestApprove_ExistingMovieWithFileNotSearched(t *testing.T) {
	h := newHarness(t)
	fr := &movieRadarr{exists: true, movie: `{"id":7,"tmdbId":949,"title":"Heat","monitored":true,"hasFile":true}`}
	pointRadarrAt(t, h, fr)
	got, err := approveMovie(t, h, h.user(gen.GetUserRequestPermissionsRow{}))
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.ArrItemID == nil || *got.ArrItemID != 7 {
		t.Errorf("arr item = %v, want 7", got.ArrItemID)
	}
	if fr.addAttempts != 0 || fr.put != nil || len(fr.commands) != 0 {
		t.Errorf("adds=%d put=%v commands=%v, want nothing sent", fr.addAttempts, fr.put, fr.commands)
	}
}

// The existence check can miss the movie (another add won the race); Radarr
// then refuses the add — its real 400 MovieExistsValidator answer, or a 409 —
// and the request switches to the existing movie instead of failing.
func TestApprove_ExistingMovieAddRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"400 MovieExistsValidator", http.StatusBadRequest, radarrMovieExistsBody},
		{"409", http.StatusConflict, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			fr := &movieRadarr{exists: true, raced: true, addStatus: tc.status, addBody: tc.body,
				movie: `{"id":7,"tmdbId":949,"title":"Heat","monitored":false,"hasFile":false}`}
			pointRadarrAt(t, h, fr)
			got, err := approveMovie(t, h, h.user(gen.GetUserRequestPermissionsRow{}))
			if err != nil {
				t.Fatalf("Approve: %v", err)
			}
			if fr.addAttempts != 1 {
				t.Errorf("add attempts = %d, want 1", fr.addAttempts)
			}
			if got.Status != StatusDownloading || got.ArrItemID == nil || *got.ArrItemID != 7 {
				t.Fatalf("status = %q arr item = %v, want downloading linked to movie 7", got.Status, got.ArrItemID)
			}
			if fr.put == nil || fr.put["monitored"] != true || !reflect.DeepEqual(fr.commands, moviesSearch7) {
				t.Errorf("PUT = %v commands = %v, want monitored + MoviesSearch", fr.put, fr.commands)
			}
		})
	}
}

// Any other 400 from the add is still a rejected add: the request stays
// pending for an admin.
func TestApprove_MovieAddValidationErrorStaysPending(t *testing.T) {
	h := newHarness(t)
	fr := &movieRadarr{}
	pointRadarrAt(t, h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v3/movie" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `[{"propertyName":"RootFolderPath","errorMessage":"Folder is not writable","errorCode":"FolderWritableValidator"}]`)
			return
		}
		fr.ServeHTTP(w, r)
	}))
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: uuid.New()}); !errors.Is(err, ErrArrAddFailed) {
		t.Fatalf("Approve err = %v, want ErrArrAddFailed", err)
	}
	if st := h.db.get(req.ID).Status; st != StatusPending {
		t.Errorf("status = %q, want pending", st)
	}
}

// ── concurrent duplicate create ─────────────────────────────────────────────

// A double submit / retry can pass the read-then-insert duplicate check
// together; the loser's insert hits a unique index (migration 00028 covers
// identical season sets too) and must read as "already requested", not as a
// server error — and send no notices.
func TestCreate_UniqueViolationIsAlreadyRequested(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		seasons []int
		want    error
	}{
		{"movie", TypeMovie, nil, ErrAlreadyRequested},
		{"all-seasons show", TypeShow, nil, ErrAlreadyRequested},
		{"same season set", TypeShow, []int{1, 2}, ErrSeasonsAlreadyRequested},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.db.createErr = &pgconn.PgError{Code: "23505", ConstraintName: "media_requests_unique_active_seasons"}
			uid := h.user(gen.GetUserRequestPermissionsRow{IsAdmin: true})
			_, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: tc.typ, TMDBID: 1399, Seasons: tc.seasons})
			if !errors.Is(err, tc.want) || !errors.Is(err, ErrAlreadyRequested) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(h.notify.sent)+len(h.notify.adminSent) != 0 || h.arr.calls != 0 {
				t.Errorf("notices %v / %v, arr calls %d; a refused duplicate sends nothing", h.notify.sent, h.notify.adminSent, h.arr.calls)
			}
		})
	}
}
