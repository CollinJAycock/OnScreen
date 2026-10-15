package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

// ── label gate ───────────────────────────────────────────────────────────────

type fakeLabelGate struct {
	permitted bool
	err       error
	calls     int
	kept      []uuid.UUID
	last      gen.ItemLabelsPermittedParams
}

func (g *fakeLabelGate) ItemLabelsPermitted(_ context.Context, arg gen.ItemLabelsPermittedParams) (bool, error) {
	g.calls++
	g.last = arg
	return g.permitted, g.err
}

func (g *fakeLabelGate) FilterLabelPermittedItems(_ context.Context, _ gen.FilterLabelPermittedItemsParams) ([]uuid.UUID, error) {
	g.calls++
	return g.kept, g.err
}

func TestLabelsPermit(t *testing.T) {
	item := uuid.New()
	restricted := &auth.Claims{UserID: uuid.New(), BlockedLabels: []string{"horror"}}
	free := &auth.Claims{UserID: uuid.New()}

	// Unrestricted callers never query, even with no gate wired.
	g := &fakeLabelGate{}
	if !labelsPermit(WithItemLabelGate(context.Background(), g), free, item) || g.calls != 0 {
		t.Errorf("unrestricted: permitted with %d queries, want true with 0", g.calls)
	}
	if !labelsPermit(context.Background(), nil, item) {
		t.Error("nil claims must pass")
	}
	// Fail closed: a restricted caller without a gate, or on a lookup error.
	if labelsPermit(context.Background(), restricted, item) {
		t.Error("restricted caller with no gate on the context was admitted")
	}
	if labelsPermit(WithItemLabelGate(context.Background(), &fakeLabelGate{permitted: true, err: errors.New("db down")}), restricted, item) {
		t.Error("restricted caller admitted on a lookup error")
	}
	g = &fakeLabelGate{permitted: true}
	if !labelsPermit(WithItemLabelGate(context.Background(), g), restricted, item) {
		t.Error("permitted item refused")
	}
	if g.last.ID != item || len(g.last.BlockedLabels) != 1 || g.last.AllowedLabels != nil {
		t.Errorf("gate got %+v, want the item and the caller's lists (allowed NULL)", g.last)
	}
	if labelsPermit(WithItemLabelGate(context.Background(), &fakeLabelGate{permitted: false}), restricted, item) {
		t.Error("refused item admitted")
	}
}

func TestItemPermitted_RatingAndLabels(t *testing.T) {
	ctx := WithItemLabelGate(context.Background(), &fakeLabelGate{permitted: false})
	capped := &auth.Claims{MaxContentRating: "PG"}
	if itemPermitted(ctx, capped, uuid.New(), "R") {
		t.Error("over-ceiling item admitted")
	}
	if !itemPermitted(ctx, capped, uuid.New(), "G") {
		t.Error("in-ceiling item refused for a caller with no label restriction")
	}
	labelled := &auth.Claims{AllowedLabels: []string{"kids"}}
	if itemPermitted(ctx, labelled, uuid.New(), "G") {
		t.Error("label-refused item admitted")
	}
	if !viewerRestricted(labelled) || viewerRestricted(&auth.Claims{}) || viewerRestricted(nil) {
		t.Error("viewerRestricted must be true for a label-only restriction and false otherwise")
	}
}

func TestLabelPermittedSet(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	free := &auth.Claims{}
	if set := labelPermittedSet(context.Background(), free, []uuid.UUID{a, b}); set != nil || !inLabelSet(set, a) {
		t.Error("unrestricted: want nil set admitting everything")
	}
	restricted := &auth.Claims{BlockedLabels: []string{"x"}}
	ctx := WithItemLabelGate(context.Background(), &fakeLabelGate{kept: []uuid.UUID{a}})
	set := labelPermittedSet(ctx, restricted, []uuid.UUID{a, b})
	if !inLabelSet(set, a) || inLabelSet(set, b) {
		t.Errorf("set = %v, want only a", set)
	}
	if set := labelPermittedSet(context.Background(), restricted, []uuid.UUID{a}); inLabelSet(set, a) {
		t.Error("no gate: want the empty set (fail closed)")
	}
}

// ── admin label handler ──────────────────────────────────────────────────────

type mockLabelsDB struct {
	labels     []string
	getErr     error
	setRows    int64
	lastSet    *gen.SetMediaItemLabelsParams
	lastAdd    *gen.AddMediaItemLabelsParams
	lastRemove *gen.RemoveMediaItemLabelsParams
	addRows    int64
	removeRows int64
	inUse      []gen.ListLabelsInUseRow
}

func (m *mockLabelsDB) GetMediaItemLabels(context.Context, uuid.UUID) ([]string, error) {
	return m.labels, m.getErr
}
func (m *mockLabelsDB) SetMediaItemLabels(_ context.Context, arg gen.SetMediaItemLabelsParams) (int64, error) {
	m.lastSet = &arg
	return m.setRows, nil
}
func (m *mockLabelsDB) AddMediaItemLabels(_ context.Context, arg gen.AddMediaItemLabelsParams) (int64, error) {
	m.lastAdd = &arg
	return m.addRows, nil
}
func (m *mockLabelsDB) RemoveMediaItemLabels(_ context.Context, arg gen.RemoveMediaItemLabelsParams) (int64, error) {
	m.lastRemove = &arg
	return m.removeRows, nil
}
func (m *mockLabelsDB) ListLabelsInUse(context.Context) ([]gen.ListLabelsInUseRow, error) {
	return m.inUse, nil
}

func labelReq(method, path, body string, admin bool, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: admin}))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestLabelHandler_SetItem(t *testing.T) {
	db := &mockLabelsDB{setRows: 1}
	h := NewLabelHandler(db, slog.Default())
	id := uuid.New()

	rec := httptest.NewRecorder()
	h.SetItem(rec, labelReq("PUT", "/", `{"labels":["  Kids ","WAR","kids",""]}`, true, map[string]string{"id": id.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if db.lastSet == nil || db.lastSet.ID != id || strings.Join(db.lastSet.Labels, ",") != "kids,war" {
		t.Errorf("stored %+v, want normalised [kids war]", db.lastSet)
	}
	var env struct {
		Data itemLabelsResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if strings.Join(env.Data.Labels, ",") != "kids,war" {
		t.Errorf("response labels = %v", env.Data.Labels)
	}

	// Clearing stores an empty (non-NULL) array.
	rec = httptest.NewRecorder()
	h.SetItem(rec, labelReq("PUT", "/", `{"labels":[]}`, true, map[string]string{"id": id.String()}))
	if rec.Code != http.StatusOK || db.lastSet.Labels == nil {
		t.Errorf("clear: status %d, stored %v (want [] not nil)", rec.Code, db.lastSet.Labels)
	}

	cases := []struct {
		name  string
		body  string
		admin bool
		rows  int64
		want  int
	}{
		{"non-admin", `{"labels":["a"]}`, false, 1, http.StatusForbidden},
		{"unknown field", `{"label":["a"]}`, true, 1, http.StatusBadRequest},
		{"too long", `{"labels":["` + strings.Repeat("a", itemlabel.MaxLen+1) + `"]}`, true, 1, http.StatusUnprocessableEntity},
		{"missing item", `{"labels":["a"]}`, true, 0, http.StatusNotFound},
	}
	for _, tc := range cases {
		db := &mockLabelsDB{setRows: tc.rows}
		rec := httptest.NewRecorder()
		NewLabelHandler(db, slog.Default()).SetItem(rec, labelReq("PUT", "/", tc.body, tc.admin, map[string]string{"id": id.String()}))
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}

func TestLabelHandler_Apply(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	db := &mockLabelsDB{addRows: 2, removeRows: 1}
	h := NewLabelHandler(db, slog.Default())
	body := `{"item_ids":["` + a.String() + `","` + b.String() + `"],"add":["Kids"],"remove":["Horror"]}`
	rec := httptest.NewRecorder()
	h.Apply(rec, labelReq("POST", "/", body, true, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"added":2`) || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Errorf("body = %s", rec.Body)
	}
	if db.lastAdd == nil || strings.Join(db.lastAdd.Labels, ",") != "kids" || db.lastAdd.MaxPerItem != itemlabel.MaxPerItem || len(db.lastAdd.Ids) != 2 {
		t.Errorf("add = %+v", db.lastAdd)
	}
	if db.lastRemove == nil || strings.Join(db.lastRemove.Labels, ",") != "horror" {
		t.Errorf("remove = %+v", db.lastRemove)
	}

	many := make([]string, maxBulkLabelItems+1)
	for i := range many {
		many[i] = `"` + uuid.NewString() + `"`
	}
	cases := []struct {
		name  string
		body  string
		admin bool
		want  int
	}{
		{"non-admin", `{"item_ids":["` + a.String() + `"],"add":["x"]}`, false, http.StatusForbidden},
		{"no ids", `{"item_ids":[],"add":["x"]}`, true, http.StatusBadRequest},
		{"too many ids", `{"item_ids":[` + strings.Join(many, ",") + `],"add":["x"]}`, true, http.StatusBadRequest},
		{"bad id", `{"item_ids":["nope"],"add":["x"]}`, true, http.StatusBadRequest},
		{"duplicate id", `{"item_ids":["` + a.String() + `","` + a.String() + `"],"add":["x"]}`, true, http.StatusBadRequest},
		{"nothing to do", `{"item_ids":["` + a.String() + `"],"add":["  "]}`, true, http.StatusBadRequest},
	}
	for _, tc := range cases {
		db := &mockLabelsDB{}
		rec := httptest.NewRecorder()
		NewLabelHandler(db, slog.Default()).Apply(rec, labelReq("POST", "/", tc.body, tc.admin, nil))
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
		if db.lastAdd != nil || db.lastRemove != nil {
			t.Errorf("%s: reached the DB", tc.name)
		}
	}
}

func TestLabelHandler_GetItemAndList(t *testing.T) {
	db := &mockLabelsDB{labels: nil, inUse: []gen.ListLabelsInUseRow{{Label: "kids", ItemCount: 3}}}
	h := NewLabelHandler(db, slog.Default())
	rec := httptest.NewRecorder()
	h.GetItem(rec, labelReq("GET", "/", "", true, map[string]string{"id": uuid.NewString()}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"labels":[]`) {
		t.Errorf("get item: %d %s, want labels []", rec.Code, rec.Body)
	}
	db.getErr = pgx.ErrNoRows
	rec = httptest.NewRecorder()
	h.GetItem(rec, labelReq("GET", "/", "", true, map[string]string{"id": uuid.NewString()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing item: %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.List(rec, labelReq("GET", "/", "", true, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"label":"kids","item_count":3`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.List(rec, labelReq("GET", "/", "", false, nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list: %d, want 403", rec.Code)
	}
}

// ── per-user label restriction ───────────────────────────────────────────────

func TestUser_SetLabelRestrictions(t *testing.T) {
	target := uuid.New()
	db := &mockUserDB{labelRestrictions: gen.GetUserLabelRestrictionsRow{BlockedLabels: []string{"war"}, AllowedLabels: []string{}}}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	// Only allowed_labels sent: blocked keeps its stored value.
	rec := httptest.NewRecorder()
	h.SetLabelRestrictions(rec, labelReq("PUT", "/", `{"allowed_labels":[" Kids ","kids","Family"]}`, true, map[string]string{"id": target.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	got := db.lastLabelRestrictions
	if got == nil || got.ID != target || strings.Join(got.BlockedLabels, ",") != "war" || strings.Join(got.AllowedLabels, ",") != "family,kids" {
		t.Errorf("stored %+v, want blocked [war] (kept) allowed [family kids]", got)
	}

	// [] clears a list.
	rec = httptest.NewRecorder()
	h.SetLabelRestrictions(rec, labelReq("PUT", "/", `{"blocked_labels":[]}`, true, map[string]string{"id": target.String()}))
	if rec.Code != http.StatusOK || db.lastLabelRestrictions.BlockedLabels == nil || len(db.lastLabelRestrictions.BlockedLabels) != 0 {
		t.Errorf("clear: %d, stored %+v", rec.Code, db.lastLabelRestrictions)
	}

	tooMany := make([]string, itemlabel.MaxPerUser+1)
	for i := range tooMany {
		tooMany[i] = `"l` + uuid.NewString()[:6] + `"`
	}
	cases := []struct {
		name  string
		id    string
		body  string
		admin bool
		err   error
		want  int
	}{
		{"non-admin", target.String(), `{"blocked_labels":["x"]}`, false, nil, http.StatusForbidden},
		{"bad id", "nope", `{"blocked_labels":["x"]}`, true, nil, http.StatusBadRequest},
		{"unknown field", target.String(), `{"blocked":["x"]}`, true, nil, http.StatusBadRequest},
		{"too many", target.String(), `{"blocked_labels":[` + strings.Join(tooMany, ",") + `]}`, true, nil, http.StatusUnprocessableEntity},
		{"unknown user", target.String(), `{"blocked_labels":["x"]}`, true, pgx.ErrNoRows, http.StatusNotFound},
	}
	for _, tc := range cases {
		db := &mockUserDB{labelRestrictionsErr: tc.err}
		rec := httptest.NewRecorder()
		NewUserHandler(&mockUserService{}).WithDB(db).SetLabelRestrictions(rec, labelReq("PUT", "/", tc.body, tc.admin, map[string]string{"id": tc.id}))
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
		if db.lastLabelRestrictions != nil {
			t.Errorf("%s: reached the DB update", tc.name)
		}
	}
}
