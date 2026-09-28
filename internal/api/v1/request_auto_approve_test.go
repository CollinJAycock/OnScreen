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
	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/requests"
)

// Media-request auto-approval: the per-user toggles endpoint, the admin user
// list fields, the settings defaults, the new-account defaults on each SSO
// provisioning path, and the auto_approved field on request objects.

type fakeRequestDefaults struct{ cfg settings.RequestsConfig }

func (f fakeRequestDefaults) Requests(_ context.Context) settings.RequestsConfig { return f.cfg }

// ── new-account defaults ────────────────────────────────────────────────────

func TestNewAccountRequestDefaults_NilReaderIsOff(t *testing.T) {
	movies, tv := NewAccountRequestDefaults(context.Background(), nil)
	if movies || tv {
		t.Errorf("nil reader: got movies=%v tv=%v, want both false", movies, tv)
	}
	movies, tv = NewAccountRequestDefaults(context.Background(),
		fakeRequestDefaults{settings.RequestsConfig{DefaultAutoApproveTV: true}})
	if movies || !tv {
		t.Errorf("tv default only: got movies=%v tv=%v", movies, tv)
	}
}

func TestOIDCService_NewUserGetsRequestDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		src    RequestDefaultsReader
		movies bool
		tv     bool
	}{
		{"defaults on", fakeRequestDefaults{settings.RequestsConfig{DefaultAutoApproveMovies: true, DefaultAutoApproveTV: true}}, true, true},
		{"movies only", fakeRequestDefaults{settings.RequestsConfig{DefaultAutoApproveMovies: true}}, true, false},
		{"not wired", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &mockOIDCDB{
				subjErr:   pgx.ErrNoRows,
				emailErr:  pgx.ErrNoRows,
				count:     3, // not the first user
				createOut: gen.User{ID: uuid.New(), Username: "dana"},
			}
			svc := NewOIDCAuthService(db, fakeIssueTokens, slog.Default()).WithRequestDefaults(tc.src)
			if _, err := svc.LoginOrCreateOIDCUser(context.Background(), OIDCProfile{
				Issuer: "https://idp", Subject: "dana-sub", Username: "dana",
			}); err != nil {
				t.Fatalf("LoginOrCreateOIDCUser: %v", err)
			}
			if db.lastCreate.AutoApproveMovies != tc.movies || db.lastCreate.AutoApproveTv != tc.tv {
				t.Errorf("created with movies=%v tv=%v, want %v/%v",
					db.lastCreate.AutoApproveMovies, db.lastCreate.AutoApproveTv, tc.movies, tc.tv)
			}
		})
	}
}

func TestSAMLService_NewUserGetsRequestDefaults(t *testing.T) {
	db := newFakeSAMLDB()
	svc := NewSAMLAuthService(db, fakeIssuer, slog.Default()).
		WithRequestDefaults(fakeRequestDefaults{settings.RequestsConfig{DefaultAutoApproveTV: true}})
	if _, err := svc.LoginOrCreateSAMLUser(context.Background(), SAMLProfile{
		Issuer: "idp", Subject: "erin", Email: "erin@example.com", Username: "erin",
	}); err != nil {
		t.Fatalf("LoginOrCreateSAMLUser: %v", err)
	}
	if len(db.users) != 1 {
		t.Fatalf("users created = %d, want 1", len(db.users))
	}
	for _, u := range db.users {
		if u.AutoApproveMovies || !u.AutoApproveTv {
			t.Errorf("created with movies=%v tv=%v, want false/true", u.AutoApproveMovies, u.AutoApproveTv)
		}
	}
}

func TestLDAPService_NewUserGetsRequestDefaults(t *testing.T) {
	cfg := &fakeLDAPSettings{cfg: validLDAPCfg()}
	dn := "uid=finn,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "finn", "finn@ex.com"),
	}}}
	db := &mockLDAPDB{
		dnErr:     pgx.ErrNoRows,
		emailErr:  pgx.ErrNoRows,
		unameErr:  pgx.ErrNoRows,
		count:     2,
		createOut: gen.User{ID: uuid.New(), Username: "finn"},
	}
	svc := NewLDAPAuthService(cfg, &fakeLDAPDialer{conn: conn}, db, fakeIssueTokens, slog.Default()).
		WithRequestDefaults(fakeRequestDefaults{settings.RequestsConfig{DefaultAutoApproveMovies: true}})
	if _, err := svc.LoginLDAP(context.Background(), "finn", "pw"); err != nil {
		t.Fatalf("LoginLDAP: %v", err)
	}
	if !db.lastCreate.AutoApproveMovies || db.lastCreate.AutoApproveTv {
		t.Errorf("created with movies=%v tv=%v, want true/false",
			db.lastCreate.AutoApproveMovies, db.lastCreate.AutoApproveTv)
	}
}

// ── PUT /users/{id}/request-permissions ─────────────────────────────────────

func putRequestPermissions(h *UserHandler, id, body string, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/api/v1/users/"+id+"/request-permissions", strings.NewReader(body))
	if admin {
		req = adminAuthedRequest(req, uuid.New())
	} else {
		req = authedRequest(req)
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.SetRequestPermissions(rec, req)
	return rec
}

func TestUser_SetRequestPermissions_Success(t *testing.T) {
	db := &mockUserDB{reqPermsRows: 1}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	target := uuid.New()

	rec := putRequestPermissions(h, target.String(), `{"auto_approve_movies":true,"auto_approve_tv":false}`, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 — body=%s", rec.Code, rec.Body)
	}
	got := db.lastReqPerms
	if got == nil || got.ID != target || !got.AutoApproveMovies || got.AutoApproveTv {
		t.Errorf("stored %+v, want id=%s movies=true tv=false", got, target)
	}
}

func TestUser_SetRequestPermissions_AdminOnly(t *testing.T) {
	db := &mockUserDB{reqPermsRows: 1}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := putRequestPermissions(h, uuid.New().String(), `{"auto_approve_movies":true,"auto_approve_tv":true}`, false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status = %d, want 403", rec.Code)
	}
	if db.lastReqPerms != nil {
		t.Error("non-admin call reached the DB")
	}
}

func TestUser_SetRequestPermissions_Validation(t *testing.T) {
	cases := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"bad id", "not-a-uuid", `{"auto_approve_movies":true,"auto_approve_tv":true}`, http.StatusBadRequest},
		{"not json", "", `nope`, http.StatusBadRequest},
		{"unknown field", "", `{"auto_approve_movie":true,"auto_approve_tv":true}`, http.StatusBadRequest},
		{"wrong type", "", `{"auto_approve_movies":"yes","auto_approve_tv":true}`, http.StatusBadRequest},
		{"missing tv", "", `{"auto_approve_movies":true}`, http.StatusUnprocessableEntity},
		{"missing both", "", `{}`, http.StatusUnprocessableEntity},
		{"null movies", "", `{"auto_approve_movies":null,"auto_approve_tv":true}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &mockUserDB{reqPermsRows: 1}
			h := NewUserHandler(&mockUserService{}).WithDB(db)
			id := tc.id
			if id == "" {
				id = uuid.New().String()
			}
			rec := putRequestPermissions(h, id, tc.body, true)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d — body=%s", rec.Code, tc.want, rec.Body)
			}
			if db.lastReqPerms != nil {
				t.Errorf("invalid request reached the DB: %+v", db.lastReqPerms)
			}
		})
	}
}

func TestUser_SetRequestPermissions_UnknownUser(t *testing.T) {
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{reqPermsRows: 0})
	rec := putRequestPermissions(h, uuid.New().String(), `{"auto_approve_movies":false,"auto_approve_tv":false}`, true)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestUser_SetRequestPermissions_DBError(t *testing.T) {
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{reqPermsErr: errors.New("db down")})
	rec := putRequestPermissions(h, uuid.New().String(), `{"auto_approve_movies":true,"auto_approve_tv":true}`, true)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestUser_ListUsers_IncludesRequestPermissions(t *testing.T) {
	alice, bob := uuid.New(), uuid.New()
	pg := "PG"
	db := &mockUserDB{listUsersRows: []gen.ListUsersRow{
		{ID: alice, Username: "alice", AutoApproveMovies: true},
		{ID: bob, Username: "bob", AutoApproveTv: true, MaxContentRating: &pg},
	}}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	rec := httptest.NewRecorder()
	h.ListUsers(rec, httptest.NewRequest("GET", "/api/v1/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("users = %d, want 2", len(resp.Data))
	}
	for _, u := range resp.Data {
		// Both keys are always present (not omitempty) so the UI can bind them.
		if _, ok := u["auto_approve_movies"]; !ok {
			t.Errorf("%v: auto_approve_movies missing", u["username"])
		}
		if _, ok := u["auto_approve_tv"]; !ok {
			t.Errorf("%v: auto_approve_tv missing", u["username"])
		}
	}
	if resp.Data[0]["auto_approve_movies"] != true || resp.Data[0]["auto_approve_tv"] != false {
		t.Errorf("alice = %v", resp.Data[0])
	}
	if resp.Data[1]["auto_approve_movies"] != false || resp.Data[1]["auto_approve_tv"] != true {
		t.Errorf("bob = %v", resp.Data[1])
	}
	// The ceiling rides along so the admin UI can say the toggles are inert
	// for a rating-limited full account (not only for household profiles).
	if _, ok := resp.Data[0]["max_content_rating"]; ok {
		t.Errorf("alice has no ceiling but max_content_rating was sent: %v", resp.Data[0])
	}
	if resp.Data[1]["max_content_rating"] != "PG" {
		t.Errorf("bob max_content_rating = %v, want PG", resp.Data[1]["max_content_rating"])
	}
}

// ── settings "requests" defaults ────────────────────────────────────────────

func TestSettings_Get_IncludesRequestDefaults(t *testing.T) {
	svc := &mockSettingsService{requests: settings.RequestsConfig{DefaultAutoApproveMovies: true}}
	rec := httptest.NewRecorder()
	newSettingsHandler(svc).Get(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Data struct {
			Requests map[string]bool `json:"requests"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{"default_auto_approve_movies": true, "default_auto_approve_tv": false}
	if len(resp.Data.Requests) != 2 ||
		resp.Data.Requests["default_auto_approve_movies"] != want["default_auto_approve_movies"] ||
		resp.Data.Requests["default_auto_approve_tv"] != want["default_auto_approve_tv"] {
		t.Errorf("requests = %v, want %v", resp.Data.Requests, want)
	}
}

func TestSettings_Update_RequestDefaultsRoundTrip(t *testing.T) {
	svc := &mockSettingsService{}
	h := newSettingsHandler(svc)

	patch := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.Update(rec, httptest.NewRequest("PATCH", "/", strings.NewReader(body)))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("PATCH %s: status = %d, want 204 — body=%s", body, rec.Code, rec.Body)
		}
	}

	patch(`{"requests":{"default_auto_approve_movies":true,"default_auto_approve_tv":true}}`)
	if svc.requests != (settings.RequestsConfig{DefaultAutoApproveMovies: true, DefaultAutoApproveTV: true}) {
		t.Fatalf("after full PATCH: %+v", svc.requests)
	}
	// A partial object changes only the field it names.
	patch(`{"requests":{"default_auto_approve_tv":false}}`)
	if svc.requests != (settings.RequestsConfig{DefaultAutoApproveMovies: true}) {
		t.Errorf("after partial PATCH: %+v, want movies kept on, tv off", svc.requests)
	}
	// A PATCH that doesn't mention "requests" leaves them alone.
	patch(`{"tmdb_api_key":"k"}`)
	if svc.requests != (settings.RequestsConfig{DefaultAutoApproveMovies: true}) {
		t.Errorf("unrelated PATCH changed request defaults: %+v", svc.requests)
	}

	// And GET reflects what was stored.
	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), `"requests":{"default_auto_approve_movies":true,"default_auto_approve_tv":false}`) {
		t.Errorf("GET body lacks the stored defaults: %s", rec.Body)
	}
}

func TestSettings_Update_RequestDefaultsServiceError(t *testing.T) {
	svc := &mockSettingsService{setErr: errors.New("db down")}
	rec := httptest.NewRecorder()
	newSettingsHandler(svc).Update(rec, httptest.NewRequest("PATCH", "/",
		strings.NewReader(`{"requests":{"default_auto_approve_movies":true}}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ── request objects carry auto_approved ─────────────────────────────────────

func TestRequestDTO_AutoApproved(t *testing.T) {
	for _, auto := range []bool{true, false} {
		b, err := json.Marshal(toRequestDTO(gen.MediaRequest{
			ID: uuid.New(), UserID: uuid.New(), Type: requests.TypeMovie, TmdbID: 949,
			Title: "Heat", Status: requests.StatusDownloading, AutoApproved: auto,
		}))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		// Present even when false, so clients needn't treat absence as false.
		if got, ok := m["auto_approved"]; !ok || got != auto {
			t.Errorf("auto_approved = %v (present=%v), want %v", got, ok, auto)
		}
	}
}
