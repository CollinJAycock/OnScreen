package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

func wlIntPtr(i int) *int { return &i }

// mockWatchLimitStore is an in-memory WatchLimitStore for handler tests.
type mockWatchLimitStore struct {
	policy    watchlimit.Policy
	policyErr error
	usage     int
	usageErr  error

	setErr    error
	setCalled bool
	setUserID uuid.UUID
	setPolicy watchlimit.Policy
}

var _ WatchLimitStore = (*mockWatchLimitStore)(nil)

func (m *mockWatchLimitStore) GetPolicy(_ context.Context, _ uuid.UUID) (watchlimit.Policy, error) {
	return m.policy, m.policyErr
}

func (m *mockWatchLimitStore) SetPolicy(_ context.Context, uid uuid.UUID, p watchlimit.Policy) error {
	m.setCalled = true
	m.setUserID = uid
	m.setPolicy = p
	return m.setErr
}

func (m *mockWatchLimitStore) TodayUsageSeconds(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	return m.usage, m.usageErr
}

// wlReq builds a request, optionally attaching auth claims and a chi "id" URL
// param so the admin Set path can resolve its target user.
func wlReq(method, url string, claims *auth.Claims, targetID, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	}
	if claims != nil {
		r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	}
	if targetID != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", targetID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func TestWatchLimit_Get_RequiresAuth(t *testing.T) {
	h := NewWatchLimitHandler(&mockWatchLimitStore{})
	rec := httptest.NewRecorder()
	h.Get(rec, wlReq(http.MethodGet, "/api/v1/users/me/watch-limit", nil, "", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestWatchLimit_Get_ReturnsPolicyAndUsage(t *testing.T) {
	store := &mockWatchLimitStore{
		policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(120)},
		usage:  30 * 60,
	}
	h := NewWatchLimitHandler(store)
	rec := httptest.NewRecorder()
	h.Get(rec, wlReq(http.MethodGet, "/api/v1/users/me/watch-limit", &auth.Claims{UserID: uuid.New()}, "", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data watchLimitResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.UsedMinutesToday != 30 {
		t.Errorf("used_minutes_today: got %d, want 30", resp.Data.UsedMinutesToday)
	}
	if resp.Data.RemainingMinutes == nil || *resp.Data.RemainingMinutes != 90 {
		t.Errorf("remaining_minutes: got %v, want 90", resp.Data.RemainingMinutes)
	}
	if !resp.Data.Allowed {
		t.Errorf("allowed: got false, want true (under cap, no hours window)")
	}
}

func TestWatchLimit_Set_RequiresAdmin(t *testing.T) {
	for _, claims := range []*auth.Claims{
		nil,
		{UserID: uuid.New(), IsAdmin: false},
	} {
		store := &mockWatchLimitStore{}
		h := NewWatchLimitHandler(store)
		rec := httptest.NewRecorder()
		h.Set(rec, wlReq(http.MethodPut, "/api/v1/users/x/watch-limit", claims, uuid.New().String(),
			`{"daily_limit_minutes":60}`))
		if rec.Code != http.StatusForbidden {
			t.Errorf("claims=%v: status got %d, want 403", claims, rec.Code)
		}
		if store.setCalled {
			t.Errorf("claims=%v: SetPolicy must not be called", claims)
		}
	}
}

func TestWatchLimit_Set_InvalidUserID(t *testing.T) {
	store := &mockWatchLimitStore{}
	h := NewWatchLimitHandler(store)
	rec := httptest.NewRecorder()
	// No chi "id" param → empty → parse fails → 400.
	h.Set(rec, wlReq(http.MethodPut, "/api/v1/users//watch-limit",
		&auth.Claims{UserID: uuid.New(), IsAdmin: true}, "", `{"daily_limit_minutes":60}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestWatchLimit_Set_Validation(t *testing.T) {
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	target := uuid.New().String()
	cases := []struct {
		name string
		body string
	}{
		{"negative cap", `{"daily_limit_minutes":-5}`},
		{"start out of range", `{"allowed_start_minute":1500,"allowed_end_minute":60}`},
		{"end out of range", `{"allowed_start_minute":60,"allowed_end_minute":2000}`},
		{"only start set", `{"allowed_start_minute":480}`},
		{"only end set", `{"allowed_end_minute":1200}`},
		{"unknown field", `{"daily_limit":60}`},
		{"bad json", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockWatchLimitStore{}
			h := NewWatchLimitHandler(store)
			rec := httptest.NewRecorder()
			h.Set(rec, wlReq(http.MethodPut, "/api/v1/users/x/watch-limit", admin, target, tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status: got %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if store.setCalled {
				t.Error("SetPolicy must not be called on invalid input")
			}
		})
	}
}

func TestWatchLimit_Set_Valid(t *testing.T) {
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	target := uuid.New()
	store := &mockWatchLimitStore{}
	h := NewWatchLimitHandler(store)
	rec := httptest.NewRecorder()
	h.Set(rec, wlReq(http.MethodPut, "/api/v1/users/x/watch-limit", admin, target.String(),
		`{"daily_limit_minutes":120,"allowed_start_minute":480,"allowed_end_minute":1200}`))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !store.setCalled {
		t.Fatal("expected SetPolicy to be called")
	}
	if store.setUserID != target {
		t.Errorf("user id: got %s, want %s", store.setUserID, target)
	}
	if store.setPolicy.DailyLimitMinutes == nil || *store.setPolicy.DailyLimitMinutes != 120 {
		t.Errorf("daily cap: got %v, want 120", store.setPolicy.DailyLimitMinutes)
	}
	if store.setPolicy.AllowedStartMinute == nil || *store.setPolicy.AllowedStartMinute != 480 {
		t.Errorf("start: got %v, want 480", store.setPolicy.AllowedStartMinute)
	}
	if store.setPolicy.AllowedEndMinute == nil || *store.setPolicy.AllowedEndMinute != 1200 {
		t.Errorf("end: got %v, want 1200", store.setPolicy.AllowedEndMinute)
	}
}

func TestWatchLimit_Set_ClearAll(t *testing.T) {
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	store := &mockWatchLimitStore{}
	h := NewWatchLimitHandler(store)
	rec := httptest.NewRecorder()
	// Empty object clears every limit (all nil) — a valid "remove limits" call.
	h.Set(rec, wlReq(http.MethodPut, "/api/v1/users/x/watch-limit", admin, uuid.New().String(), `{}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !store.setCalled {
		t.Fatal("expected SetPolicy to be called")
	}
	if store.setPolicy.Restricted() {
		t.Errorf("expected unrestricted policy, got %+v", store.setPolicy)
	}
}
