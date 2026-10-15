package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeSessionsDB holds sessions per user and records the bulk calls.
type fakeSessionsDB struct {
	sessions    map[uuid.UUID][]gen.ListActiveSessionsForUserRow // by user
	deletedAll  []uuid.UUID
	epochBumped []uuid.UUID
}

func (f *fakeSessionsDB) ListActiveSessionsForUser(_ context.Context, userID uuid.UUID) ([]gen.ListActiveSessionsForUserRow, error) {
	return f.sessions[userID], nil
}

func (f *fakeSessionsDB) DeleteUserSession(_ context.Context, arg gen.DeleteUserSessionParams) (gen.DeleteUserSessionRow, error) {
	rows := f.sessions[arg.UserID]
	for i, r := range rows {
		if r.ID == arg.ID {
			f.sessions[arg.UserID] = append(rows[:i:i], rows[i+1:]...)
			return gen.DeleteUserSessionRow{ID: r.ID, ClientName: r.ClientName, Platform: r.Platform}, nil
		}
	}
	return gen.DeleteUserSessionRow{}, pgx.ErrNoRows
}

func (f *fakeSessionsDB) DeleteOtherSessionsForUser(_ context.Context, arg gen.DeleteOtherSessionsForUserParams) (int64, error) {
	var keep []gen.ListActiveSessionsForUserRow
	var n int64
	for _, r := range f.sessions[arg.UserID] {
		if r.ID == arg.ID {
			keep = append(keep, r)
		} else {
			n++
		}
	}
	f.sessions[arg.UserID] = keep
	return n, nil
}

func (f *fakeSessionsDB) DeleteSessionsForUser(_ context.Context, userID uuid.UUID) error {
	f.deletedAll = append(f.deletedAll, userID)
	delete(f.sessions, userID)
	return nil
}

func (f *fakeSessionsDB) BumpSessionEpoch(_ context.Context, id uuid.UUID) error {
	f.epochBumped = append(f.epochBumped, id)
	return nil
}

func sessionRow(name string) gen.ListActiveSessionsForUserRow {
	ip := netip.MustParseAddr("203.0.113.7")
	now := time.Now()
	return gen.ListActiveSessionsForUserRow{
		ID: uuid.New(), ClientName: &name, IpAddr: &ip,
		CreatedAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
		LastSeen:  pgtype.Timestamptz{Time: now, Valid: true},
	}
}

func sessionsReq(method, path string, claims *auth.Claims, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(middleware.WithClaims(ctx, claims))
}

func newSessionsHandler(db *fakeSessionsDB) *UserSessionsHandler {
	return NewUserSessionsHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestUserSessions_ListMine_MarksCurrent(t *testing.T) {
	uid := uuid.New()
	cur, other := sessionRow("Chrome on Windows"), sessionRow("Roku")
	db := &fakeSessionsDB{sessions: map[uuid.UUID][]gen.ListActiveSessionsForUserRow{uid: {cur, other}}}
	h := newSessionsHandler(db)

	rec := httptest.NewRecorder()
	h.ListMine(rec, sessionsReq("GET", "/users/me/sessions", &auth.Claims{UserID: uid, SessionID: cur.ID}, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Data []signedInSession `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 {
		t.Fatalf("got %d rows", len(body.Data))
	}
	for _, s := range body.Data {
		if want := s.ID == cur.ID.String(); s.Current != want {
			t.Errorf("%s current=%v, want %v", s.ClientName, s.Current, want)
		}
		if s.IPAddress != "203.0.113.7" {
			t.Errorf("ip = %q", s.IPAddress)
		}
	}
	if raw := rec.Body.String(); strings.Contains(raw, "token") {
		t.Errorf("response mentions a token: %s", raw)
	}
}

func TestUserSessions_RevokeMine(t *testing.T) {
	uid, victim := uuid.New(), uuid.New()
	mine, theirs := sessionRow("Phone"), sessionRow("Laptop")
	db := &fakeSessionsDB{sessions: map[uuid.UUID][]gen.ListActiveSessionsForUserRow{
		uid: {mine}, victim: {theirs},
	}}
	h := newSessionsHandler(db)
	claims := &auth.Claims{UserID: uid, SessionID: uuid.New()}

	// Another user's session ID is a 404 and stays put.
	rec := httptest.NewRecorder()
	h.RevokeMine(rec, sessionsReq("DELETE", "/", claims, map[string]string{"id": theirs.ID.String()}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user revoke: status %d, want 404", rec.Code)
	}
	if len(db.sessions[victim]) != 1 {
		t.Fatal("cross-user revoke deleted the other user's session")
	}

	// One of the caller's other devices: deleted, cookies untouched.
	rec = httptest.NewRecorder()
	h.RevokeMine(rec, sessionsReq("DELETE", "/", claims, map[string]string{"id": mine.ID.String()}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	if len(db.sessions[uid]) != 0 {
		t.Fatal("session not deleted")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("revoking another device cleared the caller's cookies")
	}
	if len(db.epochBumped) != 0 {
		t.Error("single revoke bumped the epoch (would cut every other device's streams)")
	}
}

func TestUserSessions_RevokeCurrentSignsOut(t *testing.T) {
	uid := uuid.New()
	cur := sessionRow("Browser")
	db := &fakeSessionsDB{sessions: map[uuid.UUID][]gen.ListActiveSessionsForUserRow{uid: {cur}}}
	h := newSessionsHandler(db)

	rec := httptest.NewRecorder()
	h.RevokeMine(rec, sessionsReq("DELETE", "/", &auth.Claims{UserID: uid, SessionID: cur.ID},
		map[string]string{"id": cur.ID.String()}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	cleared := 0
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared++
		}
	}
	if cleared != 2 {
		t.Errorf("cleared %d auth cookies, want 2", cleared)
	}
}

func TestUserSessions_RevokeOthers(t *testing.T) {
	uid := uuid.New()
	cur, a, b := sessionRow("Here"), sessionRow("TV"), sessionRow("Phone")
	db := &fakeSessionsDB{sessions: map[uuid.UUID][]gen.ListActiveSessionsForUserRow{uid: {cur, a, b}}}
	h := newSessionsHandler(db)

	// A token with no sid can't say which session to keep.
	rec := httptest.NewRecorder()
	h.RevokeOthers(rec, sessionsReq("DELETE", "/", &auth.Claims{UserID: uid}, nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("sid-less: status %d, want 409", rec.Code)
	}
	if len(db.sessions[uid]) != 3 {
		t.Fatal("sid-less request deleted sessions")
	}

	rec = httptest.NewRecorder()
	h.RevokeOthers(rec, sessionsReq("DELETE", "/", &auth.Claims{UserID: uid, SessionID: cur.ID}, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := db.sessions[uid]; len(got) != 1 || got[0].ID != cur.ID {
		t.Fatalf("remaining sessions = %v, want only the current one", got)
	}
}

func TestUserSessions_AdminRoutes(t *testing.T) {
	admin, target := uuid.New(), uuid.New()
	s := sessionRow("TV")
	db := &fakeSessionsDB{sessions: map[uuid.UUID][]gen.ListActiveSessionsForUserRow{target: {s, sessionRow("Phone")}}}
	h := newSessionsHandler(db)

	// Non-admins are refused even if the router gate were missing.
	rec := httptest.NewRecorder()
	h.ListForUser(rec, sessionsReq("GET", "/", &auth.Claims{UserID: admin}, map[string]string{"id": target.String()}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: status %d", rec.Code)
	}

	adminClaims := &auth.Claims{UserID: admin, IsAdmin: true, SessionID: uuid.New()}
	rec = httptest.NewRecorder()
	h.RevokeForUser(rec, sessionsReq("DELETE", "/", adminClaims,
		map[string]string{"id": target.String(), "sid": s.ID.String()}))
	if rec.Code != http.StatusNoContent || len(db.sessions[target]) != 1 {
		t.Fatalf("admin single revoke: status %d, remaining %d", rec.Code, len(db.sessions[target]))
	}

	rec = httptest.NewRecorder()
	h.RevokeAllForUser(rec, sessionsReq("DELETE", "/", adminClaims, map[string]string{"id": target.String()}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin revoke all: status %d", rec.Code)
	}
	if len(db.deletedAll) != 1 || len(db.epochBumped) != 1 || db.epochBumped[0] != target {
		t.Errorf("revoke all: deleted=%v bumped=%v", db.deletedAll, db.epochBumped)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("signing another user out cleared the admin's cookies")
	}
}
