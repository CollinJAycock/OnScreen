package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/scrobble"
)

// mockScrobbleStore is an in-memory ScrobbleStore for handler tests.
type mockScrobbleStore struct {
	status    scrobble.Status
	statusErr error

	setErr     error
	setCalled  bool
	setUserID  uuid.UUID
	setToken   string
	setEnabled bool
}

var _ ScrobbleStore = (*mockScrobbleStore)(nil)

func (m *mockScrobbleStore) Status(_ context.Context, _ uuid.UUID) (scrobble.Status, error) {
	return m.status, m.statusErr
}

func (m *mockScrobbleStore) SetListenBrainz(_ context.Context, userID uuid.UUID, token string, enabled bool) error {
	m.setCalled = true
	m.setUserID = userID
	m.setToken = token
	m.setEnabled = enabled
	return m.setErr
}

// scrobbleReq builds a request, attaching auth claims when uid is non-nil so
// the same helper exercises both the authed and unauthenticated paths.
func scrobbleReq(method, url string, uid uuid.UUID, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
	}
	if uid != uuid.Nil {
		r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uid}))
	}
	return r
}

func TestScrobble_GetStatus_RequiresAuth(t *testing.T) {
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.Nil, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestScrobble_GetStatus_ReturnsStatus(t *testing.T) {
	store := &mockScrobbleStore{status: scrobble.Status{ListenBrainzLinked: true, ListenBrainzEnabled: true}}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Linked  bool `json:"listenbrainz_linked"`
			Enabled bool `json:"listenbrainz_enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !resp.Data.Linked || !resp.Data.Enabled {
		t.Errorf("body: got %+v, want linked+enabled", resp.Data)
	}
}

// The token is write-only — GetStatus must never echo it back in any field.
func TestScrobble_GetStatus_NeverLeaksToken(t *testing.T) {
	store := &mockScrobbleStore{status: scrobble.Status{ListenBrainzLinked: true, ListenBrainzEnabled: true}}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	if strings.Contains(strings.ToLower(rec.Body.String()), "token") {
		t.Errorf("status body must not mention a token: %s", rec.Body.String())
	}
}

func TestScrobble_GetStatus_StoreError(t *testing.T) {
	store := &mockScrobbleStore{statusErr: errors.New("db down")}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestScrobble_SetListenBrainz_RequiresAuth(t *testing.T) {
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uuid.Nil, `{"token":"x","enabled":true}`))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if store.setCalled {
		t.Error("SetListenBrainz must not be called without claims")
	}
}

func TestScrobble_SetListenBrainz_BadBody(t *testing.T) {
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uuid.New(), `{not valid json`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if store.setCalled {
		t.Error("SetListenBrainz must not be called for an invalid body")
	}
}

// A linked token arrives padded; the handler must trim before storing so the
// "Token <t>" auth header isn't built with stray whitespace.
func TestScrobble_SetListenBrainz_Success_TrimsToken(t *testing.T) {
	uid := uuid.New()
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uid, `{"token":"  tok123  ","enabled":true}`))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !store.setCalled {
		t.Fatal("expected SetListenBrainz to be called")
	}
	if store.setUserID != uid {
		t.Errorf("user id: got %s, want %s", store.setUserID, uid)
	}
	if store.setToken != "tok123" {
		t.Errorf("token must be trimmed: got %q, want %q", store.setToken, "tok123")
	}
	if !store.setEnabled {
		t.Error("enabled: got false, want true")
	}
}

func TestScrobble_SetListenBrainz_EmptyTokenUnlinks(t *testing.T) {
	uid := uuid.New()
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uid, `{"token":"","enabled":false}`))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}
	if !store.setCalled || store.setToken != "" || store.setEnabled {
		t.Errorf("expected unlink (empty token, disabled): called=%v token=%q enabled=%v",
			store.setCalled, store.setToken, store.setEnabled)
	}
}

func TestScrobble_SetListenBrainz_StoreError(t *testing.T) {
	store := &mockScrobbleStore{setErr: errors.New("db down")}
	h := NewScrobbleHandler(store)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uuid.New(), `{"token":"x","enabled":true}`))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}
