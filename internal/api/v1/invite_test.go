package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// ── mock InviteDB ────────────────────────────────────────────────────────────

type mockInviteDB struct {
	createErr       error
	createCalled    bool
	storedHash      string
	storedEmail     *string
	storedExpires   time.Time
	createdInviteID uuid.UUID

	getToken    InviteTokenRow
	getTokenErr error

	markUsedErr    error
	markUsedCalled bool

	list    []InviteTokenSummaryRow
	listErr error

	deleteErr    error
	deleteCalled bool
	deletedID    uuid.UUID

	createUserErr    error
	createUserCalled bool
	createdUserID    uuid.UUID
	createdUsername  string
}

func (m *mockInviteDB) CreateInviteToken(_ context.Context, _ uuid.UUID, tokenHash string, e *string, expiresAt time.Time) (uuid.UUID, error) {
	m.createCalled = true
	m.storedHash = tokenHash
	m.storedEmail = e
	m.storedExpires = expiresAt
	if m.createErr != nil {
		return uuid.Nil, m.createErr
	}
	if m.createdInviteID == uuid.Nil {
		m.createdInviteID = uuid.New()
	}
	return m.createdInviteID, nil
}

func (m *mockInviteDB) GetInviteToken(_ context.Context, _ string) (InviteTokenRow, error) {
	return m.getToken, m.getTokenErr
}

func (m *mockInviteDB) MarkInviteTokenUsed(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	m.markUsedCalled = true
	return m.markUsedErr
}

func (m *mockInviteDB) ListInviteTokens(_ context.Context) ([]InviteTokenSummaryRow, error) {
	return m.list, m.listErr
}

func (m *mockInviteDB) DeleteInviteToken(_ context.Context, id uuid.UUID) error {
	m.deleteCalled = true
	m.deletedID = id
	return m.deleteErr
}

func (m *mockInviteDB) CreateUser(_ context.Context, username string, _ *string, _ string) (uuid.UUID, error) {
	m.createUserCalled = true
	m.createdUsername = username
	if m.createUserErr != nil {
		return uuid.Nil, m.createUserErr
	}
	if m.createdUserID == uuid.Nil {
		m.createdUserID = uuid.New()
	}
	return m.createdUserID, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newInviteHandler(db InviteDB) *InviteHandler {
	return NewInviteHandler(db, nil, "http://localhost:3000", slog.Default())
}

func withAdminClaims(req *http.Request, userID uuid.UUID, username string) *http.Request {
	claims := &auth.Claims{UserID: userID, Username: username, IsAdmin: true}
	return req.WithContext(middleware.WithClaims(req.Context(), claims))
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestInvite_Create_StoresHashNotRawToken(t *testing.T) {
	db := &mockInviteDB{}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminClaims(req, uuid.New(), "admin")
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	data := decodeData(t, rec)
	inviteURL, _ := data.Data["invite_url"].(string)
	if inviteURL == "" {
		t.Fatal("expected invite_url in response")
	}
	idx := strings.Index(inviteURL, "token=")
	if idx < 0 {
		t.Fatalf("invite URL missing token param: %q", inviteURL)
	}
	rawToken := inviteURL[idx+len("token="):]
	if len(rawToken) != 64 { // 32 bytes hex-encoded
		t.Errorf("token length: got %d, want 64", len(rawToken))
	}

	// The DB must have received the SHA-256 hash, not the raw token.
	wantHash := sha256.Sum256([]byte(rawToken))
	wantHex := hex.EncodeToString(wantHash[:])
	if db.storedHash != wantHex {
		t.Errorf("stored hash does not match SHA-256 of raw token")
	}
	if db.storedHash == rawToken {
		t.Error("raw token was stored in DB — must store only the hash")
	}
}

func TestInvite_Create_Unauthenticated(t *testing.T) {
	db := &mockInviteDB{}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 (no claims)", rec.Code)
	}
	if db.createCalled {
		t.Error("CreateInviteToken must not be called without claims")
	}
}

func TestInvite_Create_GeneratesUniqueTokens(t *testing.T) {
	h := newInviteHandler(&mockInviteDB{})
	tokens := make(map[string]struct{})
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader(`{}`))
		req = withAdminClaims(req, uuid.New(), "admin")
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		data := decodeData(t, rec)
		url, _ := data.Data["invite_url"].(string)
		idx := strings.Index(url, "token=")
		if idx < 0 {
			t.Fatalf("missing token in url %q", url)
		}
		tokens[url[idx+len("token="):]] = struct{}{}
	}
	if len(tokens) != 10 {
		t.Errorf("expected 10 unique tokens, got %d", len(tokens))
	}
}

// ── Accept ───────────────────────────────────────────────────────────────────

func TestInvite_Accept_RejectsInvalidToken(t *testing.T) {
	db := &mockInviteDB{getTokenErr: errors.New("not found")}
	h := newInviteHandler(db)

	body := `{"token":"deadbeef","username":"newuser","password":"hunter22secure"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Accept(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if db.createUserCalled {
		t.Error("CreateUser must not be called for an invalid invite")
	}
}

func TestInvite_Accept_RejectsShortPassword(t *testing.T) {
	db := &mockInviteDB{}
	h := newInviteHandler(db)

	body := `{"token":"anything","username":"newuser","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Accept(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if db.createUserCalled {
		t.Error("CreateUser must not be called when password is too short")
	}
}

func TestInvite_Accept_RejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"username":"u","password":"hunter22secure"}`, // no token
		`{"token":"t","password":"hunter22secure"}`,    // no username
		`{"token":"t","username":"u"}`,                 // no password
		``,                                             // empty body
	}
	for _, body := range cases {
		db := &mockInviteDB{}
		h := newInviteHandler(db)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.Accept(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
		if db.createUserCalled {
			t.Errorf("body %q: CreateUser must not be called", body)
		}
	}
}

func TestInvite_Accept_Success_MarksTokenUsed(t *testing.T) {
	inviteID := uuid.New()
	adminID := uuid.New()
	db := &mockInviteDB{
		getToken: InviteTokenRow{ID: inviteID, CreatedBy: adminID},
	}
	h := newInviteHandler(db)

	body := `{"token":"rawtokenhex","username":"newuser","password":"hunter22secure"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Accept(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !db.createUserCalled {
		t.Error("expected CreateUser to be called")
	}
	if db.createdUsername != "newuser" {
		t.Errorf("username: got %q, want %q", db.createdUsername, "newuser")
	}
	if !db.markUsedCalled {
		t.Error("expected MarkInviteTokenUsed to be called — single-use enforcement")
	}
}

func TestInvite_Accept_CreateUserFails_DoesNotMarkUsed(t *testing.T) {
	db := &mockInviteDB{
		getToken:      InviteTokenRow{ID: uuid.New()},
		createUserErr: errors.New("username taken"),
	}
	h := newInviteHandler(db)

	body := `{"token":"rawtokenhex","username":"taken","password":"hunter22secure"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites/accept", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Accept(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if db.markUsedCalled {
		t.Error("token must not be marked used if user creation failed")
	}
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestInvite_List_ReturnsRows(t *testing.T) {
	e := "a@b.c"
	now := time.Now().UTC()
	db := &mockInviteDB{
		list: []InviteTokenSummaryRow{
			{ID: uuid.New(), Email: &e, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		},
	}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invites", nil)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "a@b.c") {
		t.Errorf("list output missing email: %s", rec.Body.String())
	}
}

func TestInvite_List_DBError(t *testing.T) {
	db := &mockInviteDB{listErr: errors.New("boom")}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invites", nil)
	rec := httptest.NewRecorder()

	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestInvite_Delete_Success(t *testing.T) {
	id := uuid.New()
	db := &mockInviteDB{}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/invites/"+id.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !db.deleteCalled || db.deletedID != id {
		t.Errorf("expected DeleteInviteToken(%s); got called=%v id=%s", id, db.deleteCalled, db.deletedID)
	}
}

func TestInvite_Delete_InvalidID(t *testing.T) {
	db := &mockInviteDB{}
	h := newInviteHandler(db)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/invites/not-a-uuid", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.Delete(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if db.deleteCalled {
		t.Error("DeleteInviteToken must not be called for invalid id")
	}
}
