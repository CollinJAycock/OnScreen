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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock user service ────────────────────────────────────────────────────────

type mockUserService struct {
	setPINErr   error
	clearPINErr error

	switchableUsers []SwitchableUser
	switchableErr   error

	verifyPINResult *PINSwitchResult
	verifyPINErr    error
}

func (m *mockUserService) SetPIN(_ context.Context, _ uuid.UUID, _, _ string) error {
	return m.setPINErr
}
func (m *mockUserService) ClearPIN(_ context.Context, _ uuid.UUID, _ string) error {
	return m.clearPINErr
}
func (m *mockUserService) ListSwitchable(_ context.Context) ([]SwitchableUser, error) {
	if m.switchableErr != nil {
		return nil, m.switchableErr
	}
	return m.switchableUsers, nil
}
func (m *mockUserService) VerifyPIN(_ context.Context, _ uuid.UUID, _ string) (*PINSwitchResult, error) {
	if m.verifyPINErr != nil {
		return nil, m.verifyPINErr
	}
	return m.verifyPINResult, nil
}

// ── mock user DB ────────────────────────────────────────────────────────────

type mockUserDB struct {
	listUsersRows []gen.ListUsersRow
	listUsersErr  error

	deleteUserErr error

	setAdminErr error

	countAdmins    int64
	countAdminsErr error

	updatePasswordErr error
}

func (m *mockUserDB) ListUsers(_ context.Context) ([]gen.ListUsersRow, error) {
	if m.listUsersErr != nil {
		return nil, m.listUsersErr
	}
	return m.listUsersRows, nil
}
func (m *mockUserDB) DeleteUser(_ context.Context, _ uuid.UUID) error {
	return m.deleteUserErr
}
func (m *mockUserDB) BumpSessionEpoch(_ context.Context, _ uuid.UUID) error { return nil }

func (m *mockUserDB) SetUserAdmin(_ context.Context, _ gen.SetUserAdminParams) error {
	return m.setAdminErr
}
func (m *mockUserDB) CountAdmins(_ context.Context) (int64, error) {
	if m.countAdminsErr != nil {
		return 0, m.countAdminsErr
	}
	return m.countAdmins, nil
}

func (m *mockUserDB) UpdateUserPassword(_ context.Context, _ gen.UpdateUserPasswordParams) error {
	return m.updatePasswordErr
}
func (m *mockUserDB) ListManagedProfiles(_ context.Context, _ pgtype.UUID) ([]gen.ListManagedProfilesRow, error) {
	return nil, nil
}
func (m *mockUserDB) ListAllManagedProfiles(_ context.Context) ([]gen.ListAllManagedProfilesRow, error) {
	return nil, nil
}
func (m *mockUserDB) CreateManagedProfile(_ context.Context, _ gen.CreateManagedProfileParams) (gen.CreateManagedProfileRow, error) {
	return gen.CreateManagedProfileRow{}, nil
}
func (m *mockUserDB) UpdateManagedProfile(_ context.Context, _ gen.UpdateManagedProfileParams) (gen.UpdateManagedProfileRow, error) {
	return gen.UpdateManagedProfileRow{}, nil
}
func (m *mockUserDB) UpdateManagedProfileAdmin(_ context.Context, _ gen.UpdateManagedProfileAdminParams) (gen.UpdateManagedProfileAdminRow, error) {
	return gen.UpdateManagedProfileAdminRow{}, nil
}
func (m *mockUserDB) DeleteManagedProfile(_ context.Context, _ gen.DeleteManagedProfileParams) error {
	return nil
}
func (m *mockUserDB) DeleteManagedProfileAdmin(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockUserDB) GetUserPreferences(_ context.Context, _ uuid.UUID) (gen.GetUserPreferencesRow, error) {
	return gen.GetUserPreferencesRow{}, nil
}
func (m *mockUserDB) UpdateUserPreferences(_ context.Context, _ gen.UpdateUserPreferencesParams) error {
	return nil
}

func (m *mockUserDB) UpdateUserContentRating(_ context.Context, _ gen.UpdateUserContentRatingParams) error {
	return nil
}

func (m *mockUserDB) UpdateUserQualityProfile(_ context.Context, _ gen.UpdateUserQualityProfileParams) error {
	return nil
}

func authedRequest(r *http.Request) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "testuser",
	})
	return r.WithContext(ctx)
}

// ── SetPIN ───────────────────────────────────────────────────────────────────

func TestUser_SetPIN_Success(t *testing.T) {
	h := NewUserHandler(&mockUserService{})

	rec := httptest.NewRecorder()
	body := `{"pin":"1234","password":"pass"}`
	req := authedRequest(httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	h.SetPIN(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_SetPIN_NoClaims(t *testing.T) {
	h := NewUserHandler(&mockUserService{})

	rec := httptest.NewRecorder()
	body := `{"pin":"1234","password":"pass"}`
	req := httptest.NewRequest("PUT", "/", strings.NewReader(body))
	h.SetPIN(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUser_SetPIN_BadPIN(t *testing.T) {
	h := NewUserHandler(&mockUserService{setPINErr: ErrBadPIN})

	rec := httptest.NewRecorder()
	body := `{"pin":"abc","password":"pass"}`
	req := authedRequest(httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	h.SetPIN(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_SetPIN_WrongPassword(t *testing.T) {
	h := NewUserHandler(&mockUserService{setPINErr: ErrInvalidCredentials})

	rec := httptest.NewRecorder()
	body := `{"pin":"1234","password":"wrong"}`
	req := authedRequest(httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	h.SetPIN(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUser_SetPIN_InvalidBody(t *testing.T) {
	h := NewUserHandler(&mockUserService{})

	rec := httptest.NewRecorder()
	req := authedRequest(httptest.NewRequest("PUT", "/", strings.NewReader("bad")))
	h.SetPIN(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── ClearPIN ─────────────────────────────────────────────────────────────────

func TestUser_ClearPIN_Success(t *testing.T) {
	h := NewUserHandler(&mockUserService{})

	rec := httptest.NewRecorder()
	body := `{"password":"pass"}`
	req := authedRequest(httptest.NewRequest("DELETE", "/", strings.NewReader(body)))
	h.ClearPIN(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_ClearPIN_NoClaims(t *testing.T) {
	h := NewUserHandler(&mockUserService{})

	rec := httptest.NewRecorder()
	body := `{"password":"pass"}`
	req := httptest.NewRequest("DELETE", "/", strings.NewReader(body))
	h.ClearPIN(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUser_ClearPIN_WrongPassword(t *testing.T) {
	h := NewUserHandler(&mockUserService{clearPINErr: ErrInvalidCredentials})

	rec := httptest.NewRecorder()
	body := `{"password":"wrong"}`
	req := authedRequest(httptest.NewRequest("DELETE", "/", strings.NewReader(body)))
	h.ClearPIN(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// ── helper: admin-authed request with a fixed user ID ───────────────────────

func adminAuthedRequest(r *http.Request, userID uuid.UUID) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{
		UserID:   userID,
		Username: "admin",
		IsAdmin:  true,
	})
	return r.WithContext(ctx)
}

// ── ListUsers ───────────────────────────────────────────────────────────────

func TestUser_ListUsers_Success(t *testing.T) {
	db := &mockUserDB{
		listUsersRows: []gen.ListUsersRow{
			{ID: uuid.New(), Username: "alice", IsAdmin: true},
			{ID: uuid.New(), Username: "bob", IsAdmin: false},
		},
	}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("users: got %d, want 2", len(resp.Data))
	}
	if resp.Meta.Total != 2 {
		t.Errorf("total: got %d, want 2", resp.Meta.Total)
	}
}

func TestUser_ListUsers_NoDB(t *testing.T) {
	h := NewUserHandler(&mockUserService{}) // no WithDB call

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	h.ListUsers(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_ListUsers_DBError(t *testing.T) {
	db := &mockUserDB{listUsersErr: errors.New("db down")}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	h.ListUsers(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── DeleteUser ──────────────────────────────────────────────────────────────

func TestUser_DeleteUser_Success(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/"+targetID.String(), nil)
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.DeleteUser(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_DeleteUser_SelfDeletion_Blocked(t *testing.T) {
	callerID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/"+callerID.String(), nil)
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", callerID.String())
	h.DeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_DeleteUser_NoClaims(t *testing.T) {
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/"+targetID.String(), nil)
	req = withChiParam(req, "id", targetID.String())
	h.DeleteUser(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUser_DeleteUser_InvalidID(t *testing.T) {
	callerID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/not-a-uuid", nil)
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", "not-a-uuid")
	h.DeleteUser(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_DeleteUser_DBError(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{deleteUserErr: errors.New("db error")}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/users/"+targetID.String(), nil)
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.DeleteUser(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── SetAdmin ────────────────────────────────────────────────────────────────

func TestUser_SetAdmin_Promote_Success(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":true}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_SetAdmin_Demote_Success(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{countAdmins: 3} // more than 1 admin
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":false}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_SetAdmin_LastAdmin_Protected(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{countAdmins: 1} // only 1 admin
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":false}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_SetAdmin_NoClaims(t *testing.T) {
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":true}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = withChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUser_SetAdmin_MissingField(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{}` // no is_admin field
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = withChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── ListSwitchable ──────────────────────────────────────────────────────────

func TestUser_ListSwitchable_Success(t *testing.T) {
	svc := &mockUserService{
		switchableUsers: []SwitchableUser{
			{ID: uuid.New(), Username: "alice", IsAdmin: true, HasPin: true},
			{ID: uuid.New(), Username: "bob", IsAdmin: false, HasPin: false},
		},
	}
	h := NewUserHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users/switchable", nil)
	h.ListSwitchable(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []SwitchableUser `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("users: got %d, want 2", len(resp.Data))
	}
}

func TestUser_ListSwitchable_ServiceError(t *testing.T) {
	svc := &mockUserService{switchableErr: errors.New("db error")}
	h := NewUserHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/users/switchable", nil)
	h.ListSwitchable(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── PINSwitch ───────────────────────────────────────────────────────────────

func TestUser_PINSwitch_WrongPIN(t *testing.T) {
	targetID := uuid.New()
	svc := &mockUserService{verifyPINErr: ErrInvalidCredentials}
	h := NewUserHandler(svc).WithDB(&mockUserDB{})
	// PINSwitch needs a TokenMaker, but the wrong-PIN path returns before token issuance.
	// We still need tokens to be non-nil to pass the nil check.
	// However, looking at the handler, it checks h.tokens == nil first.
	// So for the wrong-PIN test we need tokens set. We'll skip that check by
	// testing that without tokens it returns 500.

	// Test: no token maker returns 500.
	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":"9999"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d (no token maker)", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_PINSwitch_InvalidBody(t *testing.T) {
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader("bad"))
	h.PINSwitch(rec, req)

	// No token maker — returns 500 before body parsing.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_PINSwitch_InvalidUserID(t *testing.T) {
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})

	rec := httptest.NewRecorder()
	body := `{"user_id":"not-a-uuid","pin":"1234"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	// No token maker — returns 500 before body parsing.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_PINSwitch_EmptyPIN(t *testing.T) {
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})

	rec := httptest.NewRecorder()
	targetID := uuid.New()
	body := `{"user_id":"` + targetID.String() + `","pin":""}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	// No token maker — returns 500 before body parsing.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── helper: create a test TokenMaker ────────────────────────────────────────

func testTokenMaker(t *testing.T) *auth.TokenMaker {
	t.Helper()
	key := make([]byte, 32)
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	return tm
}

// helper: withChiParam for users_test (same as items_test but avoids cross-file dep)
func usersWithChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ── WithTokenMaker ──────────────────────────────────────────────────────────

func TestUser_WithTokenMaker_ReturnsHandler(t *testing.T) {
	h := NewUserHandler(&mockUserService{})
	tm := testTokenMaker(t)
	logger := slog.Default()

	got := h.WithTokenMaker(tm, logger)
	if got != h {
		t.Error("WithTokenMaker should return the same handler for chaining")
	}
	if h.tokens != tm {
		t.Error("WithTokenMaker should set the tokens field")
	}
	if h.logger != logger {
		t.Error("WithTokenMaker should set the logger field")
	}
}

// ── ResetPassword ───────────────────────────────────────────────────────────

func TestUser_ResetPassword_Success(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	targetID := uuid.New()
	rec := httptest.NewRecorder()
	body := `{"password":"longEnoughPassword"}`
	req := httptest.NewRequest("PUT", "/api/v1/users/"+targetID.String()+"/password", strings.NewReader(body))
	req = usersWithChiParam(req, "id", targetID.String())
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestUser_ResetPassword_NoDB(t *testing.T) {
	h := NewUserHandler(&mockUserService{}) // no WithDB

	rec := httptest.NewRecorder()
	body := `{"password":"longEnoughPassword"}`
	req := httptest.NewRequest("PUT", "/api/v1/users/some-id/password", strings.NewReader(body))
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_ResetPassword_InvalidID(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"password":"longEnoughPassword"}`
	req := httptest.NewRequest("PUT", "/api/v1/users/not-a-uuid/password", strings.NewReader(body))
	req = usersWithChiParam(req, "id", "not-a-uuid")
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_ResetPassword_InvalidBody(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	targetID := uuid.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/users/"+targetID.String()+"/password", strings.NewReader("bad"))
	req = usersWithChiParam(req, "id", targetID.String())
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_ResetPassword_TooShort(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	targetID := uuid.New()
	rec := httptest.NewRecorder()
	body := `{"password":"short"}`
	req := httptest.NewRequest("PUT", "/api/v1/users/"+targetID.String()+"/password", strings.NewReader(body))
	req = usersWithChiParam(req, "id", targetID.String())
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_ResetPassword_DBError(t *testing.T) {
	db := &mockUserDB{updatePasswordErr: errors.New("db error")}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	targetID := uuid.New()
	rec := httptest.NewRecorder()
	body := `{"password":"longEnoughPassword"}`
	req := httptest.NewRequest("PUT", "/api/v1/users/"+targetID.String()+"/password", strings.NewReader(body))
	req = usersWithChiParam(req, "id", targetID.String())
	h.ResetPassword(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── PINSwitch (with TokenMaker) ─────────────────────────────────────────────

func TestUser_PINSwitch_Success(t *testing.T) {
	targetID := uuid.New()
	svc := &mockUserService{
		verifyPINResult: &PINSwitchResult{
			UserID:   targetID,
			Username: "alice",
			IsAdmin:  false,
		},
	}
	tm := testTokenMaker(t)
	h := NewUserHandler(svc).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":"1234"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Data struct {
			AccessToken string `json:"access_token"`
			Username    string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.AccessToken == "" {
		t.Error("expected non-empty access_token")
	}
	if resp.Data.Username != "alice" {
		t.Errorf("username: got %q, want %q", resp.Data.Username, "alice")
	}
}

func TestUser_PINSwitch_WrongPIN_WithTokens(t *testing.T) {
	targetID := uuid.New()
	svc := &mockUserService{verifyPINErr: ErrInvalidCredentials}
	tm := testTokenMaker(t)
	h := NewUserHandler(svc).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":"9999"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestUser_PINSwitch_BadPIN_WithTokens(t *testing.T) {
	targetID := uuid.New()
	svc := &mockUserService{verifyPINErr: ErrBadPIN}
	tm := testTokenMaker(t)
	h := NewUserHandler(svc).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":"abc"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestUser_PINSwitch_InvalidBody_WithTokens(t *testing.T) {
	tm := testTokenMaker(t)
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader("bad"))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_PINSwitch_InvalidUserID_WithTokens(t *testing.T) {
	tm := testTokenMaker(t)
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	body := `{"user_id":"not-a-uuid","pin":"1234"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_PINSwitch_EmptyPIN_WithTokens(t *testing.T) {
	tm := testTokenMaker(t)
	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	targetID := uuid.New()
	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":""}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_PINSwitch_VerifyPIN_InternalError(t *testing.T) {
	targetID := uuid.New()
	svc := &mockUserService{verifyPINErr: errors.New("unexpected error")}
	tm := testTokenMaker(t)
	h := NewUserHandler(svc).WithDB(&mockUserDB{}).WithTokenMaker(tm, slog.Default())

	rec := httptest.NewRecorder()
	body := `{"user_id":"` + targetID.String() + `","pin":"1234"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/pin-switch", strings.NewReader(body))
	h.PINSwitch(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── SetAdmin (edge cases) ───────────────────────────────────────────────────

func TestUser_SetAdmin_InvalidID(t *testing.T) {
	callerID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":true}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/not-a-uuid", strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = usersWithChiParam(req, "id", "not-a-uuid")
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_SetAdmin_InvalidBody(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader("bad json"))
	req = adminAuthedRequest(req, callerID)
	req = usersWithChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUser_SetAdmin_NoDB(t *testing.T) {
	callerID := uuid.New()
	h := NewUserHandler(&mockUserService{}) // no WithDB

	rec := httptest.NewRecorder()
	body := `{"is_admin":true}`
	targetID := uuid.New()
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = usersWithChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_SetAdmin_Demote_CountAdminsError(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{countAdminsErr: errors.New("db error")}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":false}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = usersWithChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestUser_SetAdmin_SetUserAdmin_DBError(t *testing.T) {
	callerID := uuid.New()
	targetID := uuid.New()
	db := &mockUserDB{setAdminErr: errors.New("db error")}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	body := `{"is_admin":true}`
	req := httptest.NewRequest("PATCH", "/api/v1/users/"+targetID.String(), strings.NewReader(body))
	req = adminAuthedRequest(req, callerID)
	req = usersWithChiParam(req, "id", targetID.String())
	h.SetAdmin(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
