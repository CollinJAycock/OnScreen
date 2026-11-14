package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

func profileReq(method, body string, claims *auth.Claims, id string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/profiles", strings.NewReader(body))
	if id != "" {
		r = usersWithChiParam(r, "id", id)
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

// S7: an account can own at most maxProfilesPerAccount managed profiles.
func TestUser_CreateProfile_PerAccountCap(t *testing.T) {
	owner := &auth.Claims{UserID: uuid.New()}
	full := make([]uuid.UUID, maxProfilesPerAccount)
	db := &mockUserDB{profileIDs: full}
	h := NewUserHandler(&mockUserService{}).WithDB(db)

	rec := httptest.NewRecorder()
	h.CreateProfile(rec, profileReq("POST", `{"username":"kid21"}`, owner, ""))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	if code, msg := errorCode(t, rec); code != "PROFILE_LIMIT" || !strings.Contains(msg, "20 profiles") {
		t.Errorf("error = %s %q", code, msg)
	}
	if len(db.createProfileArgs) != 0 {
		t.Error("a profile was created past the cap")
	}

	// One under the cap is fine.
	db = &mockUserDB{profileIDs: full[:maxProfilesPerAccount-1]}
	h = NewUserHandler(&mockUserService{}).WithDB(db)
	rec = httptest.NewRecorder()
	h.CreateProfile(rec, profileReq("POST", `{"username":"kid20"}`, owner, ""))
	if rec.Code != http.StatusCreated {
		t.Errorf("under the cap: status %d", rec.Code)
	}
}

// A taken name is a 409, on create as on rename — create used to answer 500.
func TestUser_Profile_NameTakenIsConflict(t *testing.T) {
	taken := &pgconn.PgError{Code: "23505", ConstraintName: "users_username_key"}
	owner := &auth.Claims{UserID: uuid.New()}

	h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{createProfileErr: taken})
	rec := httptest.NewRecorder()
	h.CreateProfile(rec, profileReq("POST", `{"username":"alice"}`, owner, ""))
	if code, _ := errorCode(t, rec); rec.Code != http.StatusConflict || code != "CONFLICT" {
		t.Errorf("create: status %d code %q, want 409 CONFLICT", rec.Code, code)
	}

	for _, admin := range []bool{false, true} {
		h = NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{updateProfileErr: taken})
		rec = httptest.NewRecorder()
		c := &auth.Claims{UserID: uuid.New(), IsAdmin: admin}
		h.UpdateProfile(rec, profileReq("PATCH", `{"username":"alice"}`, c, uuid.New().String()))
		if code, _ := errorCode(t, rec); rec.Code != http.StatusConflict || code != "CONFLICT" {
			t.Errorf("rename (admin=%v): status %d code %q, want 409 CONFLICT", admin, rec.Code, code)
		}
	}

	// No such profile (or not the caller's) stays a 404.
	h = NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{updateProfileErr: pgx.ErrNoRows})
	rec = httptest.NewRecorder()
	h.UpdateProfile(rec, profileReq("PATCH", `{"username":"alice"}`, owner, uuid.New().String()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("rename of an unknown profile: status %d, want 404", rec.Code)
	}
}

// Item 8: a rename is held to the same username rule as creating the profile.
func TestUser_UpdateProfile_UsernameRule(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, bad := range []string{"a", "has space", "dots.are.out", "<script>", strings.Repeat("x", 33), "ünïcode"} {
			h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})
			rec := httptest.NewRecorder()
			body, _ := json.Marshal(map[string]string{"username": bad})
			c := &auth.Claims{UserID: uuid.New(), IsAdmin: admin}
			h.UpdateProfile(rec, profileReq("PATCH", string(body), c, uuid.New().String()))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("admin=%v rename to %q: status %d, want 400", admin, bad, rec.Code)
			}
		}
		h := NewUserHandler(&mockUserService{}).WithDB(&mockUserDB{})
		rec := httptest.NewRecorder()
		h.UpdateProfile(rec, profileReq("PATCH", `{"username":"Kid_2"}`, &auth.Claims{UserID: uuid.New(), IsAdmin: admin}, uuid.New().String()))
		if rec.Code != http.StatusOK {
			t.Errorf("admin=%v valid rename: status %d", admin, rec.Code)
		}
	}
}

// Q6: the role change goes through the one guarded statement (refusal of the
// last admin, epoch bump and sign-in deletion together); an unknown user is a
// 404, the last admin a 400.
func TestUser_SetAdmin_Guarded(t *testing.T) {
	do := func(db *mockUserDB, body string) *httptest.ResponseRecorder {
		h := NewUserHandler(&mockUserService{}).WithDB(db)
		target := uuid.New()
		req := httptest.NewRequest("PATCH", "/api/v1/users/"+target.String(), strings.NewReader(body))
		req = usersWithChiParam(adminAuthedRequest(req, uuid.New()), "id", target.String())
		rec := httptest.NewRecorder()
		h.SetAdmin(rec, req)
		return rec
	}
	db := &mockUserDB{}
	if rec := do(db, `{"is_admin":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("demote: status %d", rec.Code)
	}
	if len(db.setAdminCalls) != 1 || db.setAdminCalls[0].IsAdmin {
		t.Errorf("guarded update calls: %+v", db.setAdminCalls)
	}
	// The epoch bump and session wipe are part of the guarded statement now;
	// the handler must not run them as separate, fallible steps.
	if db.epochBumps != 0 || db.allSessionsDeleted != 0 {
		t.Errorf("separate revocation calls: bumps=%d deletes=%d", db.epochBumps, db.allSessionsDeleted)
	}
	if rec := do(&mockUserDB{lastAdmin: true}, `{"is_admin":false}`); rec.Code != http.StatusBadRequest {
		t.Errorf("demoting the last admin: status %d, want 400", rec.Code)
	}
	if rec := do(&mockUserDB{userMissing: true}, `{"is_admin":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d, want 404", rec.Code)
	}
	if rec := do(&mockUserDB{lastAdmin: true}, `{"is_admin":true}`); rec.Code != http.StatusNoContent {
		t.Errorf("promoting while one admin exists: status %d, want 204", rec.Code)
	}
}

// Q6: deleting another user is guarded too — two admins deleting each other
// at once must not leave none.
func TestUser_DeleteUser_LastAdminRefused(t *testing.T) {
	db := &mockUserDB{lastAdmin: true}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	target := uuid.New()
	req := httptest.NewRequest("DELETE", "/api/v1/users/"+target.String(), nil)
	req = usersWithChiParam(adminAuthedRequest(req, uuid.New()), "id", target.String())
	rec := httptest.NewRecorder()
	h.DeleteUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
	if len(db.deletedUsers) != 0 {
		t.Error("the last admin was deleted")
	}
}

// An admin's password reset is a recovery: it revokes the user's API keys
// along with their sign-ins.
func TestUser_AdminResetPassword_RevokesAPIKeys(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	target := uuid.New()
	req := httptest.NewRequest("PUT", "/api/v1/users/"+target.String()+"/password",
		strings.NewReader(`{"password":"a-new-password-1"}`))
	req = usersWithChiParam(adminAuthedRequest(req, uuid.New()), "id", target.String())
	rec := httptest.NewRecorder()
	h.ResetPassword(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d (%s)", rec.Code, rec.Body.String())
	}
	if len(db.apiKeysDeleted) != 1 || db.apiKeysDeleted[0] != target {
		t.Errorf("api keys deleted for %v, want [%s]", db.apiKeysDeleted, target)
	}
}

// A self-service password change keeps the account's API keys (documented in
// docs/security.md): the user is signed in and manages them on the same page.
func TestUser_ChangePassword_KeepsAPIKeys(t *testing.T) {
	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	rec := httptest.NewRecorder()
	h.ChangePassword(rec, changePasswordRequest(&auth.Claims{UserID: uuid.New(), SessionID: uuid.New()}, goodPasswordChange))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(db.apiKeysDeleted) != 0 {
		t.Error("a self-service password change revoked the API keys")
	}
}
