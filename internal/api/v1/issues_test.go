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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// ── fakes ─────────────────────────────────────────────────────────────────

type issuesFakeDB struct {
	items  map[uuid.UUID]gen.GetMediaItemRow
	files  map[uuid.UUID]gen.MediaFile
	issues map[uuid.UUID]gen.MediaIssue

	openCount    int32
	createErr    error
	created      []gen.CreateMediaIssueParams
	closed       []gen.CloseMediaIssueParams
	listParams   []gen.ListMediaIssuesAdminParams
	adminRows    []gen.ListMediaIssuesAdminRow
	damagedRows  []gen.ListDamagedMediaFilesRow
	damagedTotal int32
	arrServices  map[string][]gen.ArrService
}

func newIssuesFakeDB() *issuesFakeDB {
	return &issuesFakeDB{
		items:       map[uuid.UUID]gen.GetMediaItemRow{},
		files:       map[uuid.UUID]gen.MediaFile{},
		issues:      map[uuid.UUID]gen.MediaIssue{},
		arrServices: map[string][]gen.ArrService{},
	}
}

func (f *issuesFakeDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := f.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}

func (f *issuesFakeDB) GetMediaFile(_ context.Context, id uuid.UUID) (gen.MediaFile, error) {
	mf, ok := f.files[id]
	if !ok {
		return gen.MediaFile{}, pgx.ErrNoRows
	}
	return mf, nil
}

func (f *issuesFakeDB) CreateMediaIssue(_ context.Context, arg gen.CreateMediaIssueParams) (gen.MediaIssue, error) {
	if f.createErr != nil {
		return gen.MediaIssue{}, f.createErr
	}
	f.created = append(f.created, arg)
	iss := gen.MediaIssue{
		ID: uuid.New(), ItemID: arg.ItemID, FileID: arg.FileID, UserID: arg.UserID,
		Kind: arg.Kind, Note: arg.Note, Status: IssueStatusOpen,
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	f.issues[iss.ID] = iss
	return iss, nil
}

func (f *issuesFakeDB) GetMediaIssue(_ context.Context, id uuid.UUID) (gen.MediaIssue, error) {
	iss, ok := f.issues[id]
	if !ok {
		return gen.MediaIssue{}, pgx.ErrNoRows
	}
	return iss, nil
}

func (f *issuesFakeDB) CountOpenMediaIssuesForUser(context.Context, uuid.UUID) (int32, error) {
	return f.openCount, nil
}

func (f *issuesFakeDB) GetOpenMediaIssueForUserItemKind(_ context.Context, arg gen.GetOpenMediaIssueForUserItemKindParams) (gen.MediaIssue, error) {
	for _, iss := range f.issues {
		if iss.UserID == arg.UserID && iss.ItemID == arg.ItemID && iss.Kind == arg.Kind && iss.Status == IssueStatusOpen {
			return iss, nil
		}
	}
	return gen.MediaIssue{}, pgx.ErrNoRows
}

func (f *issuesFakeDB) ListMediaIssuesForUserItem(_ context.Context, arg gen.ListMediaIssuesForUserItemParams) ([]gen.MediaIssue, error) {
	var out []gen.MediaIssue
	for _, iss := range f.issues {
		if iss.UserID == arg.UserID && iss.ItemID == arg.ItemID {
			out = append(out, iss)
		}
	}
	return out, nil
}

func (f *issuesFakeDB) ListMediaIssuesAdmin(_ context.Context, arg gen.ListMediaIssuesAdminParams) ([]gen.ListMediaIssuesAdminRow, error) {
	f.listParams = append(f.listParams, arg)
	return f.adminRows, nil
}

func (f *issuesFakeDB) CountMediaIssuesAdmin(context.Context, *string) (int32, error) {
	return int32(len(f.adminRows)), nil
}

func (f *issuesFakeDB) CloseMediaIssue(_ context.Context, arg gen.CloseMediaIssueParams) (gen.MediaIssue, error) {
	iss, ok := f.issues[arg.ID]
	if !ok || iss.Status != IssueStatusOpen {
		return gen.MediaIssue{}, pgx.ErrNoRows
	}
	f.closed = append(f.closed, arg)
	iss.Status = arg.Status
	iss.ResolvedBy = arg.ResolvedBy
	iss.ResolutionNote = arg.ResolutionNote
	iss.ResolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.issues[arg.ID] = iss
	return iss, nil
}

func (f *issuesFakeDB) ListDamagedMediaFiles(context.Context, gen.ListDamagedMediaFilesParams) ([]gen.ListDamagedMediaFilesRow, error) {
	return f.damagedRows, nil
}

func (f *issuesFakeDB) CountDamagedMediaFiles(context.Context) (int32, error) {
	return f.damagedTotal, nil
}
func (f *issuesFakeDB) CountMediaItemsMissingArt(context.Context) (int32, error)   { return 4, nil }
func (f *issuesFakeDB) CountUnmatchedTopLevelItems(context.Context) (int32, error) { return 3, nil }

func (f *issuesFakeDB) ListEnabledArrServicesByKind(_ context.Context, kind string) ([]gen.ArrService, error) {
	return f.arrServices[kind], nil
}

type issueNotifyCall struct {
	userID uuid.UUID
	admins bool
	typ    string
	title  string
	body   string
	itemID *uuid.UUID
}

type issuesFakeNotifier struct{ calls []issueNotifyCall }

func (n *issuesFakeNotifier) Notify(_ context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID) {
	n.calls = append(n.calls, issueNotifyCall{userID: userID, typ: typ, title: title, body: body, itemID: itemID})
}

func (n *issuesFakeNotifier) NotifyAdmins(_ context.Context, typ, title, body string, itemID *uuid.UUID) {
	n.calls = append(n.calls, issueNotifyCall{admins: true, typ: typ, title: title, body: body, itemID: itemID})
}

// ── helpers ───────────────────────────────────────────────────────────────

func issueReq(t *testing.T, method, target, body string, claims *auth.Claims, params map[string]string) *http.Request {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for k, v := range params {
		req = withChiParam(req, k, v)
	}
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	return req
}

func issueDecodeData(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, rec.Body.String())
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode data: %v (%s)", err, env.Data)
	}
}

func issueErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Error.Code
}

type issuesFixture struct {
	db       *issuesFakeDB
	notifier *issuesFakeNotifier
	h        *IssueHandler
	lib      uuid.UUID
	movie    gen.GetMediaItemRow
	user     *auth.Claims
	admin    *auth.Claims
}

func newIssuesFixture() *issuesFixture {
	db := newIssuesFakeDB()
	lib := uuid.New()
	pg := "PG"
	movie := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: lib, Type: "movie", Title: "Heat", ContentRating: &pg}
	db.items[movie.ID] = movie
	n := &issuesFakeNotifier{}
	h := NewIssueHandler(db, wsFakeAccess{allowed: lib}, slog.Default()).WithNotifier(n)
	return &issuesFixture{
		db: db, notifier: n, h: h, lib: lib, movie: movie,
		user:  &auth.Claims{UserID: uuid.New(), Username: "alice"},
		admin: &auth.Claims{UserID: uuid.New(), Username: "root", IsAdmin: true},
	}
}

func (fx *issuesFixture) create(t *testing.T, claims *auth.Claims, itemID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	fx.h.Create(rec, issueReq(t, http.MethodPost, "/api/v1/items/"+itemID.String()+"/issues", body, claims,
		map[string]string{"id": itemID.String()}))
	return rec
}

// ── Create ────────────────────────────────────────────────────────────────

func TestIssues_Create_HappyPathNotifiesAdmins(t *testing.T) {
	fx := newIssuesFixture()
	fileID := uuid.New()
	fx.db.files[fileID] = gen.MediaFile{ID: fileID, MediaItemID: fx.movie.ID}

	rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"video","note":"  green frames after 2s  ","file_id":"`+fileID.String()+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got IssueResponse
	issueDecodeData(t, rec, &got)
	if got.Kind != "video" || got.Status != "open" || got.Note == nil || *got.Note != "green frames after 2s" ||
		got.FileID == nil || *got.FileID != fileID.String() || got.ItemID != fx.movie.ID.String() {
		t.Errorf("response = %+v", got)
	}
	if len(fx.db.created) != 1 || fx.db.created[0].UserID != fx.user.UserID {
		t.Fatalf("created = %+v", fx.db.created)
	}
	if len(fx.notifier.calls) != 1 {
		t.Fatalf("notifications = %+v", fx.notifier.calls)
	}
	c := fx.notifier.calls[0]
	if !c.admins || c.typ != notification.TypeIssueReported || c.itemID == nil || *c.itemID != fx.movie.ID ||
		c.body != "alice reported a problem with Heat: video" {
		t.Errorf("notification = %+v", c)
	}
}

func TestIssues_Create_Validation(t *testing.T) {
	fx := newIssuesFixture()
	otherFile := uuid.New()
	fx.db.files[otherFile] = gen.MediaFile{ID: otherFile, MediaItemID: uuid.New()}
	cases := map[string]struct {
		body string
		code int
	}{
		"bad json":            {`{`, http.StatusBadRequest},
		"missing kind":        {`{}`, http.StatusUnprocessableEntity},
		"unknown kind":        {`{"kind":"smell"}`, http.StatusUnprocessableEntity},
		"note too long":       {`{"kind":"other","note":"` + strings.Repeat("é", 1001) + `"}`, http.StatusUnprocessableEntity},
		"bad file id":         {`{"kind":"video","file_id":"nope"}`, http.StatusUnprocessableEntity},
		"unknown file":        {`{"kind":"video","file_id":"` + uuid.NewString() + `"}`, http.StatusUnprocessableEntity},
		"another item's file": {`{"kind":"video","file_id":"` + otherFile.String() + `"}`, http.StatusUnprocessableEntity},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if rec := fx.create(t, fx.user, fx.movie.ID, tc.body); rec.Code != tc.code {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.code, rec.Body)
			}
		})
	}
	if len(fx.db.created) != 0 {
		t.Errorf("invalid bodies created %d issues", len(fx.db.created))
	}
	// Exactly 1000 characters is fine; a blank note is stored as NULL.
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"other","note":"`+strings.Repeat("é", 1000)+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("1000-char note: %d", rec.Code)
	}
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"audio","note":"   "}`); rec.Code != http.StatusCreated {
		t.Errorf("blank note: %d", rec.Code)
	} else if fx.db.created[len(fx.db.created)-1].Note != nil {
		t.Error("blank note must be stored as NULL")
	}
}

func TestIssues_Create_ACLAndRatingCeiling(t *testing.T) {
	fx := newIssuesFixture()
	hidden := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Hidden"}
	fx.db.items[hidden.ID] = hidden
	if rec := fx.create(t, fx.user, hidden.ID, `{"kind":"video"}`); rec.Code != http.StatusNotFound {
		t.Errorf("inaccessible library: %d, want 404", rec.Code)
	}
	// Over the caller's ceiling: 404, same as a missing item.
	r := "R"
	mature := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "movie", Title: "Mature", ContentRating: &r}
	fx.db.items[mature.ID] = mature
	kid := &auth.Claims{UserID: uuid.New(), Username: "kid", MaxContentRating: "PG"}
	if rec := fx.create(t, kid, mature.ID, `{"kind":"video"}`); rec.Code != http.StatusNotFound {
		t.Errorf("over ceiling: %d, want 404", rec.Code)
	}
	// Unrated counts as most restrictive for a capped caller.
	unrated := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "movie", Title: "Unrated"}
	fx.db.items[unrated.ID] = unrated
	if rec := fx.create(t, kid, unrated.ID, `{"kind":"video"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unrated for capped caller: %d, want 404", rec.Code)
	}
	if rec := fx.create(t, fx.user, uuid.New(), `{"kind":"video"}`); rec.Code != http.StatusNotFound {
		t.Errorf("missing item: %d, want 404", rec.Code)
	}
	if rec := fx.create(t, nil, fx.movie.ID, `{"kind":"video"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", rec.Code)
	}
	if len(fx.db.created) != 0 || len(fx.notifier.calls) != 0 {
		t.Error("a refused report must not be stored or announced")
	}
}

func TestIssues_Create_DuplicateAndCaps(t *testing.T) {
	fx := newIssuesFixture()
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"video"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"video"}`)
	if rec.Code != http.StatusConflict || issueErrorCode(t, rec) != "ALREADY_REPORTED" {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body)
	}
	// A different kind on the same item is a separate report.
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"audio"}`); rec.Code != http.StatusCreated {
		t.Errorf("other kind: %d", rec.Code)
	}

	// Per-user open cap.
	fx.db.openCount = maxOpenIssuesPerUser
	rec = fx.create(t, fx.user, fx.movie.ID, `{"kind":"subtitles"}`)
	if rec.Code != http.StatusTooManyRequests || issueErrorCode(t, rec) != "TOO_MANY_OPEN_ISSUES" {
		t.Errorf("over cap: %d %s", rec.Code, rec.Body)
	}
	// Admins are exempt.
	if rec := fx.create(t, fx.admin, fx.movie.ID, `{"kind":"subtitles"}`); rec.Code != http.StatusCreated {
		t.Errorf("admin over cap: %d", rec.Code)
	}

	// A concurrent duplicate that slips past the pre-check hits the unique
	// index: still a 409, not a 500.
	fx.db.openCount = 0
	fx.db.createErr = &pgconn.PgError{Code: "23505"}
	rec = fx.create(t, fx.user, fx.movie.ID, `{"kind":"wrong_match"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("unique violation: %d, want 409", rec.Code)
	}
	fx.db.createErr = errors.New("boom")
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"wrong_match"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("db failure: %d, want 500", rec.Code)
	}
}

func TestIssues_Create_EpisodeTitleInNotification(t *testing.T) {
	fx := newIssuesFixture()
	one, two := int32(1), int32(2)
	show := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "show", Title: "Andor"}
	season := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "season", Title: "Season 1", Index: &one,
		ParentID: pgtype.UUID{Bytes: show.ID, Valid: true}}
	pg := "TV-14"
	ep := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: fx.lib, Type: "episode", Title: "Aldhani", Index: &two,
		ContentRating: &pg, ParentID: pgtype.UUID{Bytes: season.ID, Valid: true}}
	for _, it := range []gen.GetMediaItemRow{show, season, ep} {
		fx.db.items[it.ID] = it
	}
	if rec := fx.create(t, fx.user, ep.ID, `{"kind":"subtitles"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if got := fx.notifier.calls[0].body; got != "alice reported a problem with Andor — S01E02 · Aldhani: subtitles" {
		t.Errorf("body = %q", got)
	}
}

// ── ListMine ──────────────────────────────────────────────────────────────

func TestIssues_ListMine_OwnReportsOnly(t *testing.T) {
	fx := newIssuesFixture()
	_ = fx.create(t, fx.user, fx.movie.ID, `{"kind":"video"}`)
	other := &auth.Claims{UserID: uuid.New(), Username: "bob"}
	_ = fx.create(t, other, fx.movie.ID, `{"kind":"audio"}`)

	rec := httptest.NewRecorder()
	fx.h.ListMine(rec, issueReq(t, http.MethodGet, "/", "", fx.user, map[string]string{"id": fx.movie.ID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got []IssueResponse
	issueDecodeData(t, rec, &got)
	if len(got) != 1 || got[0].Kind != "video" {
		t.Errorf("mine = %+v, want only alice's video report", got)
	}

	// Hidden items 404 here too.
	hidden := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie"}
	fx.db.items[hidden.ID] = hidden
	rec = httptest.NewRecorder()
	fx.h.ListMine(rec, issueReq(t, http.MethodGet, "/", "", fx.user, map[string]string{"id": hidden.ID.String()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("hidden item: %d, want 404", rec.Code)
	}
	// Empty list is [] not null.
	fresh := &auth.Claims{UserID: uuid.New()}
	rec = httptest.NewRecorder()
	fx.h.ListMine(rec, issueReq(t, http.MethodGet, "/", "", fresh, map[string]string{"id": fx.movie.ID.String()}))
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("empty list body = %s", rec.Body)
	}
}

// ── Admin list / update ───────────────────────────────────────────────────

func TestIssues_AdminList_FiltersAndShape(t *testing.T) {
	fx := newIssuesFixture()
	two, five := int32(2), int32(5)
	show := "Andor"
	path := `D:\media\tv\Andor\Season 02\Andor.S02E05.mkv`
	fx.db.adminRows = []gen.ListMediaIssuesAdminRow{{
		ID: uuid.New(), ItemID: uuid.New(), UserID: fx.user.UserID, Kind: "video", Status: "open",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		LibraryID: fx.lib, ItemType: "episode", ItemTitle: "Ep", ItemIndex: &five,
		ParentTitle: new(string), ParentIndex: &two, GrandparentTitle: &show,
		ReporterUsername: "alice", FilePath: &path,
		FileID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
	}}

	for _, tc := range []struct {
		query string
		want  *string
		code  int
	}{
		{"", issuePtrStr("open"), http.StatusOK},
		{"?status=resolved", issuePtrStr("resolved"), http.StatusOK},
		{"?status=dismissed", issuePtrStr("dismissed"), http.StatusOK},
		{"?status=all", nil, http.StatusOK},
		{"?status=bogus", nil, http.StatusUnprocessableEntity},
	} {
		fx.db.listParams = nil
		rec := httptest.NewRecorder()
		fx.h.AdminList(rec, issueReq(t, http.MethodGet, "/api/v1/admin/issues"+tc.query, "", fx.admin, nil))
		if rec.Code != tc.code {
			t.Errorf("%q: status %d, want %d", tc.query, rec.Code, tc.code)
			continue
		}
		if tc.code != http.StatusOK {
			continue
		}
		p := fx.db.listParams[0]
		if (p.Status == nil) != (tc.want == nil) || (p.Status != nil && *p.Status != *tc.want) {
			t.Errorf("%q: status filter = %v, want %v", tc.query, p.Status, tc.want)
		}
	}

	rec := httptest.NewRecorder()
	fx.h.AdminList(rec, issueReq(t, http.MethodGet, "/api/v1/admin/issues?limit=5000&offset=10", "", fx.admin, nil))
	if p := fx.db.listParams[len(fx.db.listParams)-1]; p.Lim != issueListMaxLimit || p.Off != 10 {
		t.Errorf("paging = %+v", p)
	}
	var got adminIssueListResponse
	issueDecodeData(t, rec, &got)
	if got.Total != 1 || len(got.Items) != 1 {
		t.Fatalf("list = %+v", got)
	}
	row := got.Items[0]
	if row.ShowTitle == nil || *row.ShowTitle != "Andor" || row.SeasonNumber == nil || *row.SeasonNumber != 2 ||
		row.EpisodeNumber == nil || *row.EpisodeNumber != 5 || row.ReporterUsername != "alice" ||
		row.FileName == nil || *row.FileName != "Andor.S02E05.mkv" {
		t.Errorf("row = %+v", row)
	}
	if strings.Contains(rec.Body.String(), `D:\\media`) || strings.Contains(rec.Body.String(), "Season 02") {
		t.Errorf("full server path leaked: %s", rec.Body)
	}

	// Non-admins are refused even if the route guard were missing.
	rec = httptest.NewRecorder()
	fx.h.AdminList(rec, issueReq(t, http.MethodGet, "/api/v1/admin/issues", "", fx.user, nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d, want 403", rec.Code)
	}
}

func issuePtrStr(s string) *string { return &s }

func TestIssues_AdminUpdate_ResolveNotifiesAndAudits(t *testing.T) {
	fx := newIssuesFixture()
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	fx.h.WithAudit(audit.New(capture, slog.Default()))
	_ = fx.create(t, fx.user, fx.movie.ID, `{"kind":"video"}`)
	var issueID uuid.UUID
	for id := range fx.db.issues {
		issueID = id
	}
	fx.notifier.calls = nil

	patch := func(claims *auth.Claims, id uuid.UUID, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		fx.h.AdminUpdate(rec, issueReq(t, http.MethodPatch, "/api/v1/admin/issues/"+id.String(), body, claims,
			map[string]string{"id": id.String()}))
		return rec
	}

	if rec := patch(fx.admin, issueID, `{"status":"open"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("reopen via PATCH: %d, want 422", rec.Code)
	}
	if rec := patch(fx.user, issueID, `{"status":"resolved"}`); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d, want 403", rec.Code)
	}
	if rec := patch(fx.admin, uuid.New(), `{"status":"resolved"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown issue: %d, want 404", rec.Code)
	}

	rec := patch(fx.admin, issueID, `{"status":"resolved","note":"Replaced the file."}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	var got IssueResponse
	issueDecodeData(t, rec, &got)
	if got.Status != "resolved" || got.ResolvedAt == nil || got.ResolutionNote == nil || *got.ResolutionNote != "Replaced the file." {
		t.Errorf("response = %+v", got)
	}
	if c := fx.db.closed[0]; !c.ResolvedBy.Valid || uuid.UUID(c.ResolvedBy.Bytes) != fx.admin.UserID {
		t.Errorf("resolved_by = %+v", c.ResolvedBy)
	}
	if len(fx.notifier.calls) != 1 {
		t.Fatalf("notifications = %+v", fx.notifier.calls)
	}
	n := fx.notifier.calls[0]
	if n.admins || n.userID != fx.user.UserID || n.typ != notification.TypeIssueResolved ||
		n.body != "An admin resolved your report about Heat. Replaced the file." || n.itemID == nil || *n.itemID != fx.movie.ID {
		t.Errorf("notification = %+v", n)
	}
	select {
	case a := <-capture.ch:
		if a.Action != audit.ActionIssueUpdate || a.Target == nil || *a.Target != "issue:"+issueID.String() ||
			!strings.Contains(string(a.Detail), `"status":"resolved"`) {
			t.Errorf("audit = %+v detail=%s", a, a.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no audit entry")
	}

	// Closing it again: 409.
	if rec := patch(fx.admin, issueID, `{"status":"dismissed"}`); rec.Code != http.StatusConflict || issueErrorCode(t, rec) != "NOT_OPEN" {
		t.Errorf("already closed: %d %s", rec.Code, rec.Body)
	}
}

func TestIssues_AdminUpdate_OwnReportNoSelfNotification(t *testing.T) {
	fx := newIssuesFixture()
	_ = fx.create(t, fx.admin, fx.movie.ID, `{"kind":"other"}`)
	var issueID uuid.UUID
	for id := range fx.db.issues {
		issueID = id
	}
	fx.notifier.calls = nil
	rec := httptest.NewRecorder()
	fx.h.AdminUpdate(rec, issueReq(t, http.MethodPatch, "/", `{"status":"dismissed"}`, fx.admin,
		map[string]string{"id": issueID.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rec.Code)
	}
	if len(fx.notifier.calls) != 0 {
		t.Errorf("admin closing their own report notified themselves: %+v", fx.notifier.calls)
	}
}

// ── Library health ────────────────────────────────────────────────────────

func TestIssues_LibraryHealth_Shape(t *testing.T) {
	fx := newIssuesFixture()
	fx.db.adminRows = make([]gen.ListMediaIssuesAdminRow, 2)
	detail := "decode failed at 00:10:00"
	fx.db.damagedRows = []gen.ListDamagedMediaFilesRow{{
		FileID: uuid.New(), ItemID: fx.movie.ID, LibraryID: fx.lib, ItemType: "movie", ItemTitle: "Heat",
		FilePath: "/mnt/media/movies/Heat (1995)/Heat.1995.mkv", IntegrityDetail: &detail,
		IntegrityCheckedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}}
	fx.db.damagedTotal = 1

	rec := httptest.NewRecorder()
	fx.h.LibraryHealth(rec, issueReq(t, http.MethodGet, "/api/v1/admin/library-health", "", fx.admin, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var raw map[string]any
	issueDecodeData(t, rec, &raw)
	for _, k := range []string{"open_issues", "damaged_files", "damaged_total", "unmatched_count", "missing_art_count"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing key %q in %v", k, raw)
		}
	}
	var got LibraryHealthResponse
	issueDecodeData(t, rec, &got)
	if got.OpenIssues != 2 || got.UnmatchedCount != 3 || got.MissingArtCount != 4 || got.DamagedTotal != 1 || len(got.DamagedFiles) != 1 {
		t.Errorf("health = %+v", got)
	}
	d := got.DamagedFiles[0]
	if d.PathBasename != "Heat.1995.mkv" || d.Title != "Heat" || d.CheckedAt == nil || d.IntegrityDetail == nil {
		t.Errorf("damaged = %+v", d)
	}
	if strings.Contains(rec.Body.String(), "/mnt/media") {
		t.Errorf("full path leaked: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	fx.h.LibraryHealth(rec, issueReq(t, http.MethodGet, "/", "", fx.user, nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d, want 403", rec.Code)
	}
}

func TestIssues_Helpers(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b/c.mkv":        "c.mkv",
		`C:\media\x y.mp4`:  "x y.mp4",
		"plain.mkv":         "plain.mkv",
		`\\nas\share\z.mkv`: "z.mkv",
	} {
		if got := issuePathBasename(in); got != want {
			t.Errorf("issuePathBasename(%q) = %q, want %q", in, got, want)
		}
	}
	nul := "a\x00b"
	if got, msg := cleanIssueNote(&nul); msg != "" || got == nil || *got != "ab" {
		t.Errorf("cleanIssueNote(NUL) = %v %q", got, msg)
	}
	for _, k := range []string{"video", "audio", "subtitles", "wrong_match", "other"} {
		if !IsValidIssueKind(k) {
			t.Errorf("%q should be valid", k)
		}
	}
}
