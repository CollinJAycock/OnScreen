package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/scrobble"
)

// mockScrobbleStore is an in-memory Scrobbler for handler tests.
type mockScrobbleStore struct {
	status    scrobble.Status
	statusErr error

	setErr     error
	setCalled  bool
	setUserID  uuid.UUID
	setToken   string
	setEnabled bool

	linkErr     error // returned by every Start / Complete
	lastfmStart scrobble.LastFMLinkStart
	traktStart  scrobble.TraktLinkStart
	result      scrobble.LinkResult
	gotPending  string
	gotUser     uuid.UUID
	unlinked    []string
}

var _ Scrobbler = (*mockScrobbleStore)(nil)

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

func (m *mockScrobbleStore) StartLastFMLink(_ context.Context, userID uuid.UUID) (scrobble.LastFMLinkStart, error) {
	m.gotUser = userID
	return m.lastfmStart, m.linkErr
}

func (m *mockScrobbleStore) CompleteLastFMLink(_ context.Context, userID uuid.UUID, pending string) (scrobble.LinkResult, error) {
	m.gotUser, m.gotPending = userID, pending
	return m.result, m.linkErr
}

func (m *mockScrobbleStore) UnlinkLastFM(_ context.Context, userID uuid.UUID) error {
	m.gotUser = userID
	m.unlinked = append(m.unlinked, "lastfm")
	return nil
}

func (m *mockScrobbleStore) StartTraktLink(_ context.Context, userID uuid.UUID) (scrobble.TraktLinkStart, error) {
	m.gotUser = userID
	return m.traktStart, m.linkErr
}

func (m *mockScrobbleStore) CompleteTraktLink(_ context.Context, userID uuid.UUID, pending string) (scrobble.LinkResult, error) {
	m.gotUser, m.gotPending = userID, pending
	return m.result, m.linkErr
}

func (m *mockScrobbleStore) UnlinkTrakt(_ context.Context, userID uuid.UUID) error {
	m.gotUser = userID
	m.unlinked = append(m.unlinked, "trakt")
	return nil
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
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.Nil, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestScrobble_GetStatus_ReturnsStatus(t *testing.T) {
	store := &mockScrobbleStore{status: scrobble.Status{ListenBrainzLinked: true, ListenBrainzEnabled: true}}
	h := NewScrobbleHandler(store, nil)

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
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	if strings.Contains(strings.ToLower(rec.Body.String()), "token") {
		t.Errorf("status body must not mention a token: %s", rec.Body.String())
	}
}

func TestScrobble_GetStatus_StoreError(t *testing.T) {
	store := &mockScrobbleStore{statusErr: errors.New("db down")}
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestScrobble_SetListenBrainz_RequiresAuth(t *testing.T) {
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store, nil)

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
	h := NewScrobbleHandler(store, nil)

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
	h := NewScrobbleHandler(store, nil)

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
	h := NewScrobbleHandler(store, nil)

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
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.SetListenBrainz(rec, scrobbleReq(http.MethodPut, "/api/v1/users/me/scrobble/listenbrainz", uuid.New(), `{"token":"x","enabled":true}`))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// scrobbleData unwraps the {"data": ...} envelope into a map.
func scrobbleData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body.String())
	}
	return resp.Data
}

func TestScrobble_GetStatus_ReportsLastFMAndTrakt(t *testing.T) {
	store := &mockScrobbleStore{status: scrobble.Status{
		LastFMAvailable: true, LastFMLinked: true, LastFMUsername: "rj",
		TraktAvailable: true,
	}}
	rec := httptest.NewRecorder()
	NewScrobbleHandler(store, nil).GetStatus(rec, scrobbleReq(http.MethodGet, "/api/v1/users/me/scrobble", uuid.New(), ""))

	d := scrobbleData(t, rec)
	if d["lastfm_available"] != true || d["lastfm_linked"] != true || d["lastfm_username"] != "rj" ||
		d["trakt_available"] != true || d["trakt_linked"] != false {
		t.Errorf("status: %+v", d)
	}
}

func TestScrobble_StartLinks(t *testing.T) {
	uid := uuid.New()
	store := &mockScrobbleStore{
		lastfmStart: scrobble.LastFMLinkStart{AuthURL: "https://www.last.fm/api/auth/?token=t", Pending: "sealed-1"},
		traktStart: scrobble.TraktLinkStart{
			UserCode: "ABCD", VerificationURL: "https://trakt.tv/activate", ExpiresIn: 600, Interval: 5, Pending: "sealed-2",
		},
	}
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.StartLastFMLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/lastfm/link", uid, ""))
	if d := scrobbleData(t, rec); rec.Code != http.StatusOK || d["auth_url"] != store.lastfmStart.AuthURL || d["pending"] != "sealed-1" {
		t.Errorf("last.fm start: %d %+v", rec.Code, d)
	}
	if store.gotUser != uid {
		t.Error("the link must start for the caller")
	}

	rec = httptest.NewRecorder()
	h.StartTraktLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/trakt/link", uid, ""))
	d := scrobbleData(t, rec)
	if d["user_code"] != "ABCD" || d["verification_url"] != "https://trakt.tv/activate" ||
		d["expires_in"] != float64(600) || d["interval"] != float64(5) || d["pending"] != "sealed-2" {
		t.Errorf("trakt start: %+v", d)
	}
}

func TestScrobble_LinkErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{scrobble.ErrNotConfigured, http.StatusConflict},
		{scrobble.ErrInvalidPending, http.StatusBadRequest},
		{fmt.Errorf("last.fm: %w", scrobble.ErrAppRejected), http.StatusBadGateway},
		{errors.New("last.fm is down"), http.StatusBadGateway},
	} {
		h := NewScrobbleHandler(&mockScrobbleStore{linkErr: tc.err}, nil)
		rec := httptest.NewRecorder()
		h.StartLastFMLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/lastfm/link", uuid.New(), ""))
		if rec.Code != tc.want {
			t.Errorf("%v: got %d, want %d", tc.err, rec.Code, tc.want)
		}
		rec = httptest.NewRecorder()
		h.CompleteTraktLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/trakt/link/complete", uuid.New(), `{"pending":"p"}`))
		if rec.Code != tc.want {
			t.Errorf("complete %v: got %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}

func TestScrobble_CompleteLink(t *testing.T) {
	uid := uuid.New()
	store := &mockScrobbleStore{result: scrobble.LinkResult{Status: scrobble.LinkLinked, Username: "rj"}}
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.CompleteLastFMLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/lastfm/link/complete", uid, `{"pending":"sealed"}`))
	if d := scrobbleData(t, rec); d["status"] != "linked" || d["username"] != "rj" {
		t.Errorf("complete: %+v", d)
	}
	if store.gotPending != "sealed" || store.gotUser != uid {
		t.Errorf("forwarded pending=%q user=%s", store.gotPending, store.gotUser)
	}

	store.result = scrobble.LinkResult{Status: scrobble.LinkSlowDown}
	rec = httptest.NewRecorder()
	h.CompleteTraktLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/trakt/link/complete", uid, `{"pending":"sealed"}`))
	if d := scrobbleData(t, rec); d["status"] != "slow_down" || d["username"] != nil {
		t.Errorf("slow down: %+v", d)
	}

	for _, body := range []string{`{}`, `{not json`} {
		rec = httptest.NewRecorder()
		h.CompleteLastFMLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/lastfm/link/complete", uid, body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: got %d, want 400", body, rec.Code)
		}
	}
}

func TestScrobble_Unlink(t *testing.T) {
	store := &mockScrobbleStore{}
	h := NewScrobbleHandler(store, nil)

	rec := httptest.NewRecorder()
	h.UnlinkTrakt(rec, scrobbleReq(http.MethodDelete, "/api/v1/users/me/scrobble/trakt", uuid.Nil, ""))
	if rec.Code != http.StatusUnauthorized || len(store.unlinked) != 0 {
		t.Fatalf("unauthenticated unlink: %d %v", rec.Code, store.unlinked)
	}

	for _, fn := range []http.HandlerFunc{h.UnlinkLastFM, h.UnlinkTrakt} {
		rec = httptest.NewRecorder()
		fn(rec, scrobbleReq(http.MethodDelete, "/api/v1/users/me/scrobble/x", uuid.New(), ""))
		if rec.Code != http.StatusNoContent {
			t.Errorf("unlink: got %d, want 204", rec.Code)
		}
	}
	if strings.Join(store.unlinked, ",") != "lastfm,trakt" {
		t.Errorf("unlinked: %v", store.unlinked)
	}
}

// Rejected app credentials get their own code, so the page can point at the
// admin's settings instead of the network.
func TestScrobble_RejectedAppCredentialsHaveTheirOwnCode(t *testing.T) {
	for err, code := range map[error]string{
		fmt.Errorf("x: %w", scrobble.ErrAppRejected): "SCROBBLE_APP_REJECTED",
		errors.New("timeout"):                        "SCROBBLE_UPSTREAM",
	} {
		rec := httptest.NewRecorder()
		NewScrobbleHandler(&mockScrobbleStore{linkErr: err}, nil).
			StartTraktLink(rec, scrobbleReq(http.MethodPost, "/api/v1/users/me/scrobble/trakt/link", uuid.New(), ""))
		if !strings.Contains(rec.Body.String(), `"`+code+`"`) {
			t.Errorf("%v: body %s, want code %s", err, rec.Body.String(), code)
		}
	}
}
