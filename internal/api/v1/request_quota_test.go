package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/requests"
)

// Request quotas + the can-request switch + the admin pending badge, at the
// HTTP layer: POST /requests (over_quota, REQUESTS_DISABLED), GET
// /requests/quota, GET /requests/pending-count, and the quota fields of PUT
// /users/{id}/request-permissions and GET /users.

// ── fakes ───────────────────────────────────────────────────────────────────

// rqFakeDB is a minimal in-memory requests.DB. No arr services are configured,
// so nothing is ever auto-approved — these tests are about the quota flag and
// the refusal, which the requests package tests cover in depth.
type rqFakeDB struct {
	mu    sync.Mutex
	perms map[uuid.UUID]gen.GetUserRequestPermissionsRow
	reqs  []gen.MediaRequest
}

func (f *rqFakeDB) GetArrService(context.Context, uuid.UUID) (gen.ArrService, error) {
	return gen.ArrService{}, pgx.ErrNoRows
}
func (f *rqFakeDB) GetDefaultArrServiceByKind(context.Context, string) (gen.ArrService, error) {
	return gen.ArrService{}, pgx.ErrNoRows
}
func (f *rqFakeDB) GetUserRequestPermissions(_ context.Context, id uuid.UUID) (gen.GetUserRequestPermissionsRow, error) {
	if p, ok := f.perms[id]; ok {
		return p, nil
	}
	return gen.GetUserRequestPermissionsRow{}, pgx.ErrNoRows
}
func (f *rqFakeDB) CreateMediaRequest(_ context.Context, p gen.CreateMediaRequestParams) (gen.MediaRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := gen.MediaRequest{ID: uuid.New(), UserID: p.UserID, Type: p.Type, TmdbID: p.TmdbID, Title: p.Title,
		Status: p.Status, CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	f.reqs = append(f.reqs, r)
	return r, nil
}
func (f *rqFakeDB) GetMediaRequest(context.Context, uuid.UUID) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *rqFakeDB) FindActiveRequestForUser(context.Context, gen.FindActiveRequestForUserParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *rqFakeDB) FindActiveRequestsForUserByTMDB(context.Context, gen.FindActiveRequestsForUserByTMDBParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *rqFakeDB) ListMediaRequestsForUser(context.Context, gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *rqFakeDB) CountMediaRequestsForUser(context.Context, gen.CountMediaRequestsForUserParams) (int64, error) {
	return 0, nil
}
func (f *rqFakeDB) CountRecentMediaRequestsForUser(_ context.Context, p gen.CountRecentMediaRequestsForUserParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.reqs {
		if r.UserID == p.UserID && r.Type == p.Type && r.CreatedAt.Time.After(p.Since.Time) && r.Status != requests.StatusDeclined {
			n++
		}
	}
	return n, nil
}
func (f *rqFakeDB) ListAllMediaRequests(context.Context, gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *rqFakeDB) CountAllMediaRequests(_ context.Context, status *string) (int64, error) {
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
func (f *rqFakeDB) ListActiveMediaRequestsForTMDB(context.Context, gen.ListActiveMediaRequestsForTMDBParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (f *rqFakeDB) ListMediaItemsByTMDBIDs(context.Context, gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}
func (f *rqFakeDB) ApproveMediaRequest(context.Context, gen.ApproveMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *rqFakeDB) DeclineMediaRequest(context.Context, gen.DeclineMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, pgx.ErrNoRows
}
func (f *rqFakeDB) MarkMediaRequestDownloading(context.Context, uuid.UUID) error { return nil }
func (f *rqFakeDB) MarkMediaRequestAvailable(context.Context, gen.MarkMediaRequestAvailableParams) error {
	return nil
}
func (f *rqFakeDB) MarkMediaRequestFailed(context.Context, uuid.UUID) error { return nil }
func (f *rqFakeDB) CancelMediaRequest(context.Context, gen.CancelMediaRequestParams) error {
	return nil
}
func (f *rqFakeDB) DeleteMediaRequest(context.Context, uuid.UUID) error { return nil }

type rqFakeTMDB struct{}

func (rqFakeTMDB) RefreshMovie(_ context.Context, id int) (*metadata.MovieResult, error) {
	return &metadata.MovieResult{TMDBID: id, Title: "Heat", Year: 1995}, nil
}
func (rqFakeTMDB) RefreshTV(_ context.Context, id int) (*metadata.TVShowResult, error) {
	return &metadata.TVShowResult{TMDBID: id, Title: "Severance", FirstAirYear: 2022}, nil
}
func (rqFakeTMDB) GetTVExternalIDs(context.Context, int) (int, string, error) { return 0, "", nil }

func newQuotaRequestHandler(db *rqFakeDB, d requests.QuotaDefaults) *RequestHandler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := requests.NewService(db, rqFakeTMDB{}, nil, logger).
		WithQuotaDefaults(func(context.Context) requests.QuotaDefaults { return d })
	return NewRequestHandler(svc, logger)
}

func rqAs(r *http.Request, id uuid.UUID, admin bool) *http.Request {
	return r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: id, Username: "u", IsAdmin: admin}))
}

func rqCreate(h *RequestHandler, uid uuid.UUID, tmdb int) *httptest.ResponseRecorder {
	body := `{"type":"movie","tmdb_id":` + strconv.Itoa(tmdb) + `}`
	rec := httptest.NewRecorder()
	h.Create(rec, rqAs(httptest.NewRequest("POST", "/api/v1/requests", strings.NewReader(body)), uid, false))
	return rec
}

// ── POST /requests ──────────────────────────────────────────────────────────

func TestRequests_Create_OverQuotaFlag(t *testing.T) {
	uid := uuid.New()
	one := int32(1)
	db := &rqFakeDB{perms: map[uuid.UUID]gen.GetUserRequestPermissionsRow{
		uid: {CanRequest: true, RequestQuotaMovies: &one, Username: "alice"},
	}}
	h := newQuotaRequestHandler(db, requests.QuotaDefaults{})

	type created struct {
		Data struct {
			Status    string `json:"status"`
			OverQuota *bool  `json:"over_quota"`
		} `json:"data"`
	}
	var first, second created
	rec := rqCreate(h, uid, 949)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &first)
	if first.Data.OverQuota == nil || *first.Data.OverQuota {
		t.Errorf("first request: over_quota = %v, want present and false — %s", first.Data.OverQuota, rec.Body)
	}

	rec = rqCreate(h, uid, 950)
	if rec.Code != http.StatusCreated {
		t.Fatalf("over-quota create must still be 201, got %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &second)
	if second.Data.OverQuota == nil || !*second.Data.OverQuota || second.Data.Status != requests.StatusPending {
		t.Errorf("second request = %+v, want over_quota true and pending — %s", second.Data, rec.Body)
	}
}

func TestRequests_Create_RequestsDisabled(t *testing.T) {
	uid := uuid.New()
	db := &rqFakeDB{perms: map[uuid.UUID]gen.GetUserRequestPermissionsRow{uid: {CanRequest: false}}}
	h := newQuotaRequestHandler(db, requests.QuotaDefaults{})
	rec := rqCreate(h, uid, 949)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"REQUESTS_DISABLED"`) {
		t.Errorf("status = %d body=%s, want 403 REQUESTS_DISABLED", rec.Code, rec.Body)
	}
	if len(db.reqs) != 0 {
		t.Error("a request was stored for a blocked user")
	}
}

// ── GET /requests/quota ─────────────────────────────────────────────────────

func TestRequests_Quota_Shape(t *testing.T) {
	uid := uuid.New()
	zero := int32(0)
	db := &rqFakeDB{perms: map[uuid.UUID]gen.GetUserRequestPermissionsRow{
		uid: {CanRequest: true, RequestQuotaTv: &zero},
	}}
	db.reqs = append(db.reqs, gen.MediaRequest{UserID: uid, Type: requests.TypeMovie, Status: requests.StatusPending,
		CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}})
	h := newQuotaRequestHandler(db, requests.QuotaDefaults{Movies: 3, TV: 2, WindowDays: 14})

	rec := httptest.NewRecorder()
	h.Quota(rec, rqAs(httptest.NewRequest("GET", "/api/v1/requests/quota", nil), uid, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	want := `{"data":{"can_request":true,"window_days":14,` +
		`"movies":{"limit":3,"used":1,"remaining":2},` +
		`"tv":{"limit":0,"used":0,"remaining":null}}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}

	// No claims → 401.
	rec = httptest.NewRecorder()
	h.Quota(rec, httptest.NewRequest("GET", "/api/v1/requests/quota", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", rec.Code)
	}
}

// ── GET /requests/pending-count ─────────────────────────────────────────────

func TestRequests_PendingCount(t *testing.T) {
	db := &rqFakeDB{}
	for _, st := range []string{requests.StatusPending, requests.StatusPending, requests.StatusDeclined} {
		db.reqs = append(db.reqs, gen.MediaRequest{Status: st})
	}
	h := newQuotaRequestHandler(db, requests.QuotaDefaults{})

	rec := httptest.NewRecorder()
	h.PendingCount(rec, rqAs(httptest.NewRequest("GET", "/api/v1/requests/pending-count", nil), uuid.New(), true))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"data":{"count":2}}` {
		t.Errorf("admin: status = %d body = %s, want 200 {count:2}", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.PendingCount(rec, rqAs(httptest.NewRequest("GET", "/api/v1/requests/pending-count", nil), uuid.New(), false))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status = %d, want 403", rec.Code)
	}
}

// ── PUT /users/{id}/request-permissions: quota + can_request ────────────────

func TestUser_SetRequestPermissions_QuotaFields(t *testing.T) {
	db := &mockUserDB{reqPermsRows: 1}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	target := uuid.New()

	rec := putRequestPermissions(h, target.String(), `{"can_request":false,"quota_movies":3,"quota_tv":null}`, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 — body=%s", rec.Code, rec.Body)
	}
	p := db.lastReqPerms
	if p == nil || p.ID != target {
		t.Fatalf("params = %+v", p)
	}
	if p.CanRequest == nil || *p.CanRequest {
		t.Errorf("can_request = %v, want false", p.CanRequest)
	}
	if !p.SetQuotaMovies || p.RequestQuotaMovies == nil || *p.RequestQuotaMovies != 3 {
		t.Errorf("quota_movies set=%v value=%v, want set to 3", p.SetQuotaMovies, p.RequestQuotaMovies)
	}
	// null = reset to the server default: written, as NULL.
	if !p.SetQuotaTv || p.RequestQuotaTv != nil {
		t.Errorf("quota_tv set=%v value=%v, want set to NULL", p.SetQuotaTv, p.RequestQuotaTv)
	}
	// The toggles weren't mentioned, so they're left alone.
	if p.AutoApproveMovies != nil || p.AutoApproveTv != nil {
		t.Errorf("auto-approve toggles touched: %+v", p)
	}

	// A single field on its own is enough; 0 = unlimited is a real value.
	rec = putRequestPermissions(h, target.String(), `{"quota_tv":0}`, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d — body=%s", rec.Code, rec.Body)
	}
	p = db.lastReqPerms
	if p.SetQuotaMovies || !p.SetQuotaTv || p.RequestQuotaTv == nil || *p.RequestQuotaTv != 0 || p.CanRequest != nil {
		t.Errorf("single-field params = %+v, want only quota_tv=0", p)
	}
}

func TestUser_ListUsers_IncludesRequestQuotas(t *testing.T) {
	five := int32(5)
	db := &mockUserDB{listUsersRows: []gen.ListUsersRow{
		{ID: uuid.New(), Username: "alice", CanRequest: true, RequestQuotaMovies: &five},
		{ID: uuid.New(), Username: "bob", CanRequest: false},
	}}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	rec := httptest.NewRecorder()
	h.ListUsers(rec, httptest.NewRequest("GET", "/api/v1/users", nil))
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Data) != 2 {
		t.Fatalf("decode: %v %s", err, rec.Body)
	}
	for _, u := range resp.Data {
		for _, k := range []string{"can_request", "request_quota_movies", "request_quota_tv"} {
			if _, ok := u[k]; !ok {
				t.Errorf("%v: %s missing (must always be present, null = server default)", u["username"], k)
			}
		}
	}
	if resp.Data[0]["can_request"] != true || resp.Data[0]["request_quota_movies"] != float64(5) || resp.Data[0]["request_quota_tv"] != nil {
		t.Errorf("alice = %v", resp.Data[0])
	}
	if resp.Data[1]["can_request"] != false {
		t.Errorf("bob = %v", resp.Data[1])
	}
}
