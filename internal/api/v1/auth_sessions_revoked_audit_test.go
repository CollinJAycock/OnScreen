package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// Both server-initiated "sign out everywhere" wipes — refresh-token reuse and
// an IdP-driven admin change — used to leave only a server-log line. These pin
// that each now writes exactly one auth.sessions_revoked audit entry, and that
// the routine failures next to them write none.

// awaitAudit returns the next audit entry and its decoded detail. audit.Log
// writes from a goroutine, hence the channel and the timeout.
func awaitAudit(t *testing.T, ch chan gen.InsertAuditLogParams) (gen.InsertAuditLogParams, map[string]any) {
	t.Helper()
	select {
	case p := <-ch:
		var detail map[string]any
		if len(p.Detail) > 0 {
			if err := json.Unmarshal(p.Detail, &detail); err != nil {
				t.Fatalf("audit detail is not JSON: %v (%s)", err, p.Detail)
			}
		}
		return p, detail
	case <-time.After(2 * time.Second):
		t.Fatal("no audit entry written")
	}
	return gen.InsertAuditLogParams{}, nil
}

// assertNoAudit fails if an audit entry shows up within a short grace period.
func assertNoAudit(t *testing.T, ch chan gen.InsertAuditLogParams) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("unexpected audit entry: action=%q detail=%s", p.Action, p.Detail)
	case <-time.After(200 * time.Millisecond):
	}
}

func assertSessionsRevokedEntry(t *testing.T, p gen.InsertAuditLogParams, uid uuid.UUID, wantIP string) {
	t.Helper()
	if p.Action != audit.ActionSessionsRevoked {
		t.Errorf("action: got %q, want %q", p.Action, audit.ActionSessionsRevoked)
	}
	if !p.UserID.Valid || uuid.UUID(p.UserID.Bytes) != uid {
		t.Errorf("user_id: got %v, want %s", p.UserID, uid)
	}
	if p.Target == nil || *p.Target != uid.String() {
		t.Errorf("target: got %v, want %s", p.Target, uid)
	}
	if p.IpAddr == nil || p.IpAddr.String() != wantIP {
		t.Errorf("ip: got %v, want %s", p.IpAddr, wantIP)
	}
}

// ── Refresh reuse ────────────────────────────────────────────────────────────

func TestRefresh_ReuseIsAudited(t *testing.T) {
	uid, sid := uuid.New(), uuid.New()
	idle := int64(4)
	svc := &mockAuthService{refreshErr: &RefreshReuseError{
		UserID: uid, SessionID: sid, Trigger: RefreshReuseSuperseded,
		SessionsDeleted: true, EpochBumped: true,
		ClientName: "OnScreen TV", Platform: "android_tv", SecondsSinceLastSeen: &idle,
	}}
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	h := newAuthHandler(svc).WithAudit(audit.New(capture, slog.Default()))

	const spent = "spent-refresh-token"
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"`+spent+`"}`))
	req.RemoteAddr = "203.0.113.7:51234"
	// A client-supplied header: it must be bounded before it is persisted.
	req.Header.Set("User-Agent", "OnScreenTV/1.4.0 "+strings.Repeat("x", 500))
	rec := httptest.NewRecorder()
	h.Refresh(rec, req)

	// Still an ordinary refresh failure to the client.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieRefreshToken && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("refresh cookie not cleared on reuse")
	}

	p, detail := awaitAudit(t, capture.ch)
	assertSessionsRevokedEntry(t, p, uid, "203.0.113.7")
	if detail["reason"] != "refresh_token_reuse" {
		t.Errorf("detail.reason: got %v, want refresh_token_reuse", detail["reason"])
	}
	if detail["trigger"] != RefreshReuseSuperseded {
		t.Errorf("detail.trigger: got %v, want %s", detail["trigger"], RefreshReuseSuperseded)
	}
	if detail["session_id"] != sid.String() {
		t.Errorf("detail.session_id: got %v, want %s", detail["session_id"], sid)
	}
	if detail["sessions_deleted"] != true || detail["epoch_bumped"] != true {
		t.Errorf("detail.sessions_deleted / epoch_bumped: got %v / %v, want true / true",
			detail["sessions_deleted"], detail["epoch_bumped"])
	}
	// The benign-vs-theft clues: which client the session belonged to and how
	// long it had been idle when the spent token came back.
	if detail["client_name"] != "OnScreen TV" || detail["platform"] != "android_tv" {
		t.Errorf("detail.client_name / platform: got %v / %v", detail["client_name"], detail["platform"])
	}
	if detail["seconds_since_last_seen"] != float64(4) { // JSON numbers decode as float64
		t.Errorf("detail.seconds_since_last_seen: got %v, want 4", detail["seconds_since_last_seen"])
	}
	ua, _ := detail["user_agent"].(string)
	if !strings.HasPrefix(ua, "OnScreenTV/1.4.0 ") || len(ua) != 200 {
		t.Errorf("detail.user_agent: got %d chars %.40q…, want the first 200", len(ua), ua)
	}
	if bytes.Contains(p.Detail, []byte(spent)) {
		t.Error("the refresh token itself leaked into the audit detail")
	}
	assertNoAudit(t, capture.ch)
}

// The typed error may arrive wrapped; errors.As must still find it.
func TestRefresh_WrappedReuseIsAudited(t *testing.T) {
	uid := uuid.New()
	svc := &mockAuthService{refreshErr: errors.Join(errors.New("refresh"), &RefreshReuseError{
		UserID: uid, SessionID: uuid.New(), Trigger: RefreshReuseRotationRace,
	})}
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	h := newAuthHandler(svc).WithAudit(audit.New(capture, slog.Default()))

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"tok"}`))
	h.Refresh(httptest.NewRecorder(), req)

	p, detail := awaitAudit(t, capture.ch)
	assertSessionsRevokedEntry(t, p, uid, "192.0.2.1")
	if detail["trigger"] != RefreshReuseRotationRace {
		t.Errorf("detail.trigger: got %v, want %s", detail["trigger"], RefreshReuseRotationRace)
	}
}

// A wipe whose writes failed is still audited — reuse was detected — but the
// entry must say the revocation did not land instead of claiming it did. With
// nothing known about the session, the forensic keys are absent, not zero.
func TestRefresh_ReuseAuditRecordsFailedWipe(t *testing.T) {
	uid := uuid.New()
	svc := &mockAuthService{refreshErr: &RefreshReuseError{
		UserID: uid, SessionID: uuid.New(), Trigger: RefreshReuseSuperseded,
		SessionsDeleted: false, EpochBumped: true,
	}}
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	h := newAuthHandler(svc).WithAudit(audit.New(capture, slog.Default()))

	h.Refresh(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"tok"}`)))

	p, detail := awaitAudit(t, capture.ch)
	assertSessionsRevokedEntry(t, p, uid, "192.0.2.1")
	if detail["sessions_deleted"] != false {
		t.Errorf("detail.sessions_deleted: got %v, want false — the delete failed", detail["sessions_deleted"])
	}
	if detail["epoch_bumped"] != true {
		t.Errorf("detail.epoch_bumped: got %v, want true", detail["epoch_bumped"])
	}
	for _, k := range []string{"client_name", "platform", "seconds_since_last_seen"} {
		if v, ok := detail[k]; ok {
			t.Errorf("detail.%s = %v, want absent when the session row lacked it", k, v)
		}
	}
}

// An expired / unknown refresh token is routine — every idle client hits it —
// and wipes nothing, so it stays out of the audit log. So does a refresh the
// server could not check (503): nothing was revoked.
func TestRefresh_ExpiredIsNotAudited(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"expired", ErrRefreshInvalid, http.StatusUnauthorized},
		{"server error", errors.New("refresh: look up session: conn closed"), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &mockAuthService{refreshErr: c.err}
			capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
			h := newAuthHandler(svc).WithAudit(audit.New(capture, slog.Default()))

			rec := httptest.NewRecorder()
			h.Refresh(rec, httptest.NewRequest("POST", "/api/v1/auth/refresh",
				strings.NewReader(`{"refresh_token":"expired"}`)))

			if rec.Code != c.wantStatus {
				t.Fatalf("status: got %d, want %d", rec.Code, c.wantStatus)
			}
			assertNoAudit(t, capture.ch)
		})
	}
}

// No audit logger wired (tests, minimal setups): a reuse error must still be a
// plain 401, not a nil-pointer panic.
func TestRefresh_ReuseWithoutAuditLogger(t *testing.T) {
	svc := &mockAuthService{refreshErr: &RefreshReuseError{UserID: uuid.New(), Trigger: RefreshReuseSuperseded}}
	rec := httptest.NewRecorder()
	newAuthHandler(svc).Refresh(rec, httptest.NewRequest("POST", "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"tok"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want 401", rec.Code)
	}
}

// ── IdP admin sync ───────────────────────────────────────────────────────────

func ssoAuditCtx(capture *auditCapture) context.Context {
	req := httptest.NewRequest("POST", "/api/v1/auth/ldap/login", nil)
	req.RemoteAddr = "198.51.100.9:40000"
	return withSSOAudit(context.Background(), audit.New(capture, slog.Default()), req)
}

func TestSyncAdminFromIdP_AuditsSessionWipe(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: true, SessionEpoch: 4})
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}

	syncAdminFromIdP(ssoAuditCtx(capture), db, slog.Default(),
		gen.User{ID: id, IsAdmin: true, SessionEpoch: 4}, false, true, "oidc")

	p, detail := awaitAudit(t, capture.ch)
	assertSessionsRevokedEntry(t, p, id, "198.51.100.9")
	if detail["reason"] != "idp_admin_sync" {
		t.Errorf("detail.reason: got %v, want idp_admin_sync", detail["reason"])
	}
	if detail["provider"] != "oidc" {
		t.Errorf("detail.provider: got %v, want oidc", detail["provider"])
	}
	if detail["is_admin"] != false {
		t.Errorf("detail.is_admin: got %v, want false (the new value)", detail["is_admin"])
	}
	if detail["sessions_deleted"] != true || detail["epoch_bumped"] != true {
		t.Errorf("detail.sessions_deleted / epoch_bumped: got %v / %v, want true / true",
			detail["sessions_deleted"], detail["epoch_bumped"])
	}
	assertNoAudit(t, capture.ch)
}

// The admin flag changed but a revocation write failed: still audited (the
// role did change and part of the wipe may have landed), with the failed half
// recorded as false rather than claimed.
func TestSyncAdminFromIdP_AuditsFailedWipe(t *testing.T) {
	cases := []struct {
		name                  string
		bumpErr, deleteErr    error
		wantBumped, wantDeled bool
	}{
		{"epoch bump failed", errors.New("db down"), nil, false, true},
		{"session delete failed", nil, errors.New("db down"), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := uuid.New()
			db := newSyncDB(gen.User{ID: id, IsAdmin: false})
			db.bumpErr, db.deleteErr = c.bumpErr, c.deleteErr
			capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}

			syncAdminFromIdP(ssoAuditCtx(capture), db, slog.Default(),
				gen.User{ID: id, IsAdmin: false}, true, true, "saml")

			p, detail := awaitAudit(t, capture.ch)
			assertSessionsRevokedEntry(t, p, id, "198.51.100.9")
			if detail["epoch_bumped"] != c.wantBumped || detail["sessions_deleted"] != c.wantDeled {
				t.Errorf("detail.epoch_bumped / sessions_deleted: got %v / %v, want %v / %v",
					detail["epoch_bumped"], detail["sessions_deleted"], c.wantBumped, c.wantDeled)
			}
		})
	}
}

// No change, or a failed write: nothing was wiped, so nothing is audited.
func TestSyncAdminFromIdP_NoWipeNoAudit(t *testing.T) {
	cases := []struct {
		name      string
		wantAdmin bool
		setErr    error
	}{
		{"already correct", true, nil},
		{"admin write failed", false, errors.New("write failed")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := uuid.New()
			db := newSyncDB(gen.User{ID: id, IsAdmin: true})
			db.setErr = c.setErr
			capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}

			syncAdminFromIdP(ssoAuditCtx(capture), db, slog.Default(),
				gen.User{ID: id, IsAdmin: true}, c.wantAdmin, true, "saml")

			assertNoAudit(t, capture.ch)
		})
	}
}

// End to end through a real sign-in handler + service: the handler must hand
// its audit logger and the client IP down to the sync inside the service.
func TestLDAPLogin_AdminSyncIsAudited(t *testing.T) {
	cfg := validLDAPCfg()
	cfg.AdminGroupDN = "cn=admins,ou=groups,dc=ex,dc=com"
	src := &fakeLDAPSettings{cfg: cfg}
	dn := "uid=eve,ou=people,dc=ex,dc=com"
	conn := &fakeLDAPConn{searchOut: &ldap.SearchResult{Entries: []*ldap.Entry{
		userEntry(dn, "eve", "eve@ex.com", "cn=admins,ou=groups,dc=ex,dc=com"),
	}}}
	eve := gen.User{ID: uuid.New(), Username: "eve", IsAdmin: false}
	db := &mockLDAPDB{dnUser: eve, users: map[uuid.UUID]gen.User{eve.ID: eve}}
	svc := NewLDAPAuthService(src, &fakeLDAPDialer{conn: conn}, db, fakeIssueTokens, slog.Default())
	capture := &auditCapture{ch: make(chan gen.InsertAuditLogParams, 4)}
	h := NewLDAPHandler(src, svc, slog.Default()).WithAudit(audit.New(capture, slog.Default()))

	rec := postLDAPLogin(h, map[string]string{"username": "eve", "password": "pw"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}

	// Two entries — the wipe and the login itself — written from separate
	// goroutines, so match by action rather than order.
	got := map[string]map[string]any{}
	for i := 0; i < 2; i++ {
		p, detail := awaitAudit(t, capture.ch)
		if p.Action == audit.ActionSessionsRevoked {
			assertSessionsRevokedEntry(t, p, eve.ID, "192.0.2.1")
		}
		got[p.Action] = detail
	}
	wipe, ok := got[audit.ActionSessionsRevoked]
	if !ok {
		t.Fatalf("no %s entry; got %v", audit.ActionSessionsRevoked, got)
	}
	if wipe["reason"] != "idp_admin_sync" || wipe["provider"] != "ldap" || wipe["is_admin"] != true {
		t.Errorf("wipe detail = %v, want reason=idp_admin_sync provider=ldap is_admin=true", wipe)
	}
	if _, ok := got[audit.ActionLoginSuccess]; !ok {
		t.Errorf("no %s entry; got %v", audit.ActionLoginSuccess, got)
	}
	assertNoAudit(t, capture.ch)
}
