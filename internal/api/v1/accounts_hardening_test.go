package v1

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// S4: a session entered with a profile's 4-digit PIN can't mint a permanent
// API key for that profile.
func TestAPIKeys_CreateRefusedForPINSwitchedSession(t *testing.T) {
	db := &akDB{}
	h := NewAPIKeyHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	switched := &auth.Claims{UserID: uuid.New(), Switched: true}

	w := akDo(h.CreateMine, switched, http.MethodPost, "", `{"name":"forever"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "PIN_SESSION_FORBIDDEN") ||
		!strings.Contains(w.Body.String(), "to create an API key") {
		t.Errorf("body = %s, want PIN_SESSION_FORBIDDEN with the createApiKey message", w.Body.String())
	}
	if len(db.rows) != 0 {
		t.Error("a key was created from a PIN-switched session")
	}

	// A full sign-in still can.
	if w := akDo(h.CreateMine, &auth.Claims{UserID: uuid.New()}, http.MethodPost, "", `{"name":"ok"}`); w.Code != http.StatusCreated {
		t.Errorf("full sign-in: status %d", w.Code)
	}
}

func firstRunRegister(h *AuthHandler, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://192.168.1.10:7070/api/v1/auth/register",
		strings.NewReader(`{"username":"admin","password":"password12345"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.50:5000" // a LAN browser
	req.Host = "192.168.1.10:7070"
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.Register(rec, req)
	return rec
}

// S5: while no account exists, a request a browser marks as coming from
// another origin can't create the first admin — a sandboxed iframe's
// `Origin: null`, a file:// page, a foreign site, a same-site sibling — even
// from a LAN peer addressing the server by IP, and even with
// ALLOW_PUBLIC_SETUP. The server's own setup page, the desktop app and
// non-browser clients still can.
func TestRegister_FirstRunRefusesCrossOrigin(t *testing.T) {
	newSvc := func() *mockAuthService {
		return &mockAuthService{userCount: 0, createUserResult: &UserInfo{ID: uuid.New(), Username: "admin", IsAdmin: true}}
	}
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"sandboxed iframe (Origin null)", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"Origin null, browser says cross-site", map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"file:// page", map[string]string{"Origin": "file://"}, http.StatusForbidden},
		{"foreign site", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"foreign site via Sec-Fetch-Site only", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site sibling", map[string]string{"Origin": "http://192.168.1.10:8080", "Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"same port, other host", map[string]string{"Origin": "http://192.168.1.99:7070"}, http.StatusForbidden},
		{"the server's own setup page", map[string]string{"Origin": "http://192.168.1.10:7070", "Sec-Fetch-Site": "same-origin"}, http.StatusCreated},
		{"own page, older browser (Origin only)", map[string]string{"Origin": "http://192.168.1.10:7070"}, http.StatusCreated},
		{"desktop app (Windows)", map[string]string{"Origin": "http://tauri.localhost", "Sec-Fetch-Site": "cross-site"}, http.StatusCreated},
		{"desktop app (macOS / Linux)", map[string]string{"Origin": "tauri://localhost"}, http.StatusCreated},
		{"non-browser client (no headers)", nil, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, public := range []bool{false, true} {
				h := NewAuthHandler(newSvc(), slog.New(slog.NewTextHandler(io.Discard, nil))).WithSetupPolicy(public)
				rec := firstRunRegister(h, func(r *http.Request) {
					for k, v := range tc.headers {
						r.Header.Set(k, v)
					}
				})
				if rec.Code != tc.want {
					t.Errorf("ALLOW_PUBLIC_SETUP=%v: status %d, want %d (body %s)", public, rec.Code, tc.want, rec.Body.String())
				}
				if tc.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "SETUP_CROSS_ORIGIN") {
					t.Errorf("ALLOW_PUBLIC_SETUP=%v: body %s, want SETUP_CROSS_ORIGIN", public, rec.Body.String())
				}
			}
		})
	}
}

// Once setup is done the origin rule doesn't apply: register is then an
// admin action, guarded by the admin's credential and the CSRF guard.
func TestRegister_CrossOriginRuleOnlyWhileSetupOpen(t *testing.T) {
	svc := &mockAuthService{userCount: 1, createUserResult: &UserInfo{ID: uuid.New(), Username: "bob"}}
	h := newAuthHandler(svc)
	rec := firstRunRegister(h, func(r *http.Request) {
		r.Header.Set("Origin", "http://tauri.localhost.evil.com")
		r.Header.Set("Authorization", "Bearer x")
		*r = *adminAuthedRequest(r, uuid.New())
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("admin-created user from a configured cross-origin client: status %d, want 201", rec.Code)
	}
}

// Item 9: a sign-in's username is bounded before any work, and what a failed
// sign-in writes to the audit log is cut short.
func TestLogin_UsernameBounded(t *testing.T) {
	svc := &mockAuthService{loginResult: &TokenPair{UserID: uuid.New(), Username: "x"}}
	h := newAuthHandler(svc)

	long := strings.Repeat("a", maxLoginUsernameBytes+1)
	rec := httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+long+`","password":"password12345"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-long username: status %d, want 400", rec.Code)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Message != "username is too long" {
		t.Errorf("message = %q", env.Error.Message)
	}

	// At the bound it still reaches the service.
	rec = httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+long[:maxLoginUsernameBytes]+`","password":"password12345"}`)))
	if rec.Code != http.StatusOK {
		t.Errorf("username at the bound: status %d, want 200", rec.Code)
	}
}

func TestLogin_FailedAuditTargetIsBounded(t *testing.T) {
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	h := newAuthHandler(&mockAuthService{loginErr: errors.New("bad password")}).
		WithAudit(audit.New(capture, slog.New(slog.NewTextHandler(io.Discard, nil))))

	name := strings.Repeat("ü", 100) // 200 bytes: under the request bound, over the audit one
	rec := httptest.NewRecorder()
	h.Login(rec, httptest.NewRequest("POST", "/api/v1/auth/login",
		strings.NewReader(`{"username":"  `+name+`  ","password":"wrong-password"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	p, _ := awaitAudit(t, capture.ch)
	if p.Action != audit.ActionLoginFailed {
		t.Fatalf("action %q", p.Action)
	}
	if p.Target == nil || *p.Target != strings.Repeat("ü", maxAuditUsernameRunes) {
		t.Errorf("audit target = %v, want the trimmed name cut to %d characters", p.Target, maxAuditUsernameRunes)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("héllo wörld", 4); got != "héll" {
		t.Errorf("got %q", got)
	}
	if got := truncateRunes("短い名前です", 3); got != "短い名" {
		t.Errorf("got %q (must not split a character)", got)
	}
	if got := truncateRunes("abc", 64); got != "abc" {
		t.Errorf("got %q", got)
	}
}
