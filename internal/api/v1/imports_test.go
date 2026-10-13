package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/serverimport"
)

// fakeImports records what the handler asks of the service.
type fakeImports struct {
	err       error
	connected serverimport.ConnectParams
	started   *serverimport.StartParams
	cancelled uuid.UUID
}

func (f *fakeImports) Connect(_ context.Context, p serverimport.ConnectParams) (serverimport.ServerInfo, []serverimport.Account, error) {
	f.connected = p
	if f.err != nil {
		return serverimport.ServerInfo{}, nil, f.err
	}
	return serverimport.ServerInfo{ID: "m", Name: "Den", Version: "1.40"},
		[]serverimport.Account{{ID: "1", Name: "Alice", FullState: true}, {ID: "2", Name: "kid"}}, nil
}

func (f *fakeImports) Start(_ context.Context, p serverimport.StartParams) (gen.ImportJob, error) {
	f.started = &p
	if f.err != nil {
		return gen.ImportJob{}, f.err
	}
	return gen.ImportJob{ID: uuid.New(), SourceKind: string(p.Kind), SourceUrl: p.URL, Status: "running",
		DryRun: p.DryRun, Options: []byte(`{"watched":true}`), Mapping: []byte(`[]`)}, nil
}

func (f *fakeImports) Jobs(context.Context, int32) ([]gen.ImportJob, error) {
	return []gen.ImportJob{{ID: uuid.New(), Status: "succeeded", Stats: []byte(`{"matched":4}`)}}, f.err
}

func (f *fakeImports) Job(_ context.Context, id uuid.UUID) (gen.ImportJob, error) {
	if f.err != nil {
		return gen.ImportJob{}, f.err
	}
	return gen.ImportJob{ID: id, Status: "running"}, nil
}

func (f *fakeImports) Cancel(_ context.Context, id uuid.UUID) error {
	f.cancelled = id
	return f.err
}

type fakeImportUsers struct{ users []gen.ListUsersRow }

func (f fakeImportUsers) ListUsers(context.Context) ([]gen.ListUsersRow, error) { return f.users, nil }

func importRequest(method, target, body, id string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func newImportHandler(f *fakeImports, users ...gen.ListUsersRow) *ImportHandler {
	return NewImportHandler(f, fakeImportUsers{users: users}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestImportConnect_SuggestsUsersByName(t *testing.T) {
	alice, kid := uuid.New(), uuid.New()
	f := &fakeImports{}
	h := newImportHandler(f, gen.ListUsersRow{ID: alice, Username: "alice"}, gen.ListUsersRow{ID: kid, Username: "Kid"})
	w := httptest.NewRecorder()
	h.Connect(w, importRequest(http.MethodPost, "/api/v1/admin/imports/connect",
		`{"kind":"plex","url":"http://10.0.0.5:32400","token":" tok "}`, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if f.connected.Token != "tok" || f.connected.Kind != serverimport.KindPlex {
		t.Errorf("connect params = %+v (token trimmed)", f.connected)
	}
	var resp struct {
		Data importConnectDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	d := resp.Data
	if d.ServerName != "Den" || len(d.Accounts) != 2 {
		t.Fatalf("response = %+v", d)
	}
	if d.Accounts[0].SuggestedUserID == nil || *d.Accounts[0].SuggestedUserID != alice || !d.Accounts[0].FullState {
		t.Errorf("Alice = %+v", d.Accounts[0])
	}
	if d.Accounts[1].SuggestedUserID == nil || *d.Accounts[1].SuggestedUserID != kid {
		t.Errorf("kid = %+v (names match case-insensitively)", d.Accounts[1])
	}
	if strings.Contains(w.Body.String(), "tok") {
		t.Error("the token is echoed back")
	}
}

func TestImportStart(t *testing.T) {
	user := uuid.New()
	f := &fakeImports{}
	h := newImportHandler(f, gen.ListUsersRow{ID: user, Username: "alice"})
	body := `{"kind":"jellyfin","url":"http://jf:8096","token":"k","dry_run":true,
		"mapping":[{"account_id":"u1","user_id":"` + user.String() + `"}],
		"options":{"watched":true,"history":true}}`
	w := httptest.NewRecorder()
	h.Start(w, importRequest(http.MethodPost, "/api/v1/admin/imports", body, ""))
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	p := f.started
	if p == nil || p.Kind != serverimport.KindJellyfin || !p.DryRun || !p.Options.Watched || !p.Options.History ||
		p.Options.Ratings || len(p.Mapping) != 1 || p.Mapping[0].UserID != user || p.CreatedBy == nil {
		t.Errorf("start params = %+v", p)
	}
}

func TestImportStart_UnknownUser(t *testing.T) {
	f := &fakeImports{}
	h := newImportHandler(f, gen.ListUsersRow{ID: uuid.New(), Username: "alice"})
	body := `{"kind":"plex","url":"http://x","token":"t","mapping":[{"account_id":"1","user_id":"` + uuid.NewString() + `"}],"options":{"watched":true}}`
	w := httptest.NewRecorder()
	h.Start(w, importRequest(http.MethodPost, "/api/v1/admin/imports", body, ""))
	if w.Code != http.StatusUnprocessableEntity || f.started != nil {
		t.Errorf("status %d, started %v", w.Code, f.started != nil)
	}
}

func TestImport_ErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
		key  string
	}{
		{serverimport.ErrUnauthorized, http.StatusUnprocessableEntity, "IMPORT_SOURCE_UNAUTHORIZED"},
		{errors.Join(serverimport.ErrUnreachable, errors.New("dial")), http.StatusBadGateway, "IMPORT_SOURCE_UNAVAILABLE"},
		{&serverimport.FetchError{Msg: "not Plex"}, http.StatusBadGateway, "IMPORT_SOURCE_UNAVAILABLE"},
		{serverimport.ErrInvalidURL, http.StatusUnprocessableEntity, "\"VALIDATION\""},
		{serverimport.ErrImportRunning, http.StatusConflict, "IMPORT_RUNNING"},
		{serverimport.ErrJobNotFound, http.StatusNotFound, ""},
		{errors.New("db down"), http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		h := newImportHandler(&fakeImports{err: c.err})
		w := httptest.NewRecorder()
		h.Connect(w, importRequest(http.MethodPost, "/x", `{"kind":"plex","url":"http://x","token":"t"}`, ""))
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.key) {
			t.Errorf("%v: %d %s, want %d %s", c.err, w.Code, w.Body, c.code, c.key)
		}
	}
}

func TestImportCancelAndGet(t *testing.T) {
	f := &fakeImports{}
	h := newImportHandler(f)
	id := uuid.New()
	w := httptest.NewRecorder()
	h.Cancel(w, importRequest(http.MethodPost, "/x", "", id.String()))
	if w.Code != http.StatusNoContent || f.cancelled != id {
		t.Errorf("cancel: %d, cancelled %v", w.Code, f.cancelled)
	}
	f.err = serverimport.ErrJobNotRunning
	w = httptest.NewRecorder()
	h.Cancel(w, importRequest(http.MethodPost, "/x", "", id.String()))
	if w.Code != http.StatusConflict {
		t.Errorf("cancel of a finished import: %d", w.Code)
	}
	f.err = nil
	w = httptest.NewRecorder()
	h.Get(w, importRequest(http.MethodGet, "/x", "", "not-a-uuid"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.List(w, importRequest(http.MethodGet, "/x", "", ""))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"matched":4`) {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}
}
