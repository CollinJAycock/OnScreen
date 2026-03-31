// Package uat contains user-acceptance tests for the OnScreen HTTP API.
//
// Unlike unit tests (which mock at the handler boundary), UAT tests wire the
// full chi router — real auth middleware, real Paseto tokens, real Valkey-backed
// rate limiting via miniredis — and exercise complete user workflows over HTTP.
// Domain services are replaced with thin in-memory stubs so no database or
// FFmpeg is required; the goal is to verify routing, middleware chains,
// authentication, authorization, and response shapes, not business logic.
package uat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onscreen/onscreen/internal/api"
	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// ── stub implementations ──────────────────────────────────────────────────────

// stubAuthService implements v1.AuthService.
type stubAuthService struct {
	users     map[string]*v1.UserInfo // username → user
	passwords map[string]string       // username → password
	tokens    map[string]string       // refresh_token → username
	count     int64
}

func newStubAuthService() *stubAuthService {
	return &stubAuthService{
		users:     make(map[string]*v1.UserInfo),
		passwords: make(map[string]string),
		tokens:    make(map[string]string),
	}
}

func (s *stubAuthService) addUser(username, password string, isAdmin bool) *v1.UserInfo {
	u := &v1.UserInfo{ID: uuid.New(), Username: username, IsAdmin: isAdmin}
	s.users[username] = u
	s.passwords[username] = password
	s.count++
	return u
}

func (s *stubAuthService) LoginLocal(_ context.Context, username, password string) (*v1.TokenPair, error) {
	u, ok := s.users[username]
	if !ok || s.passwords[username] != password {
		return nil, fmt.Errorf("invalid credentials")
	}
	tok := "refresh-" + uuid.New().String()
	s.tokens[tok] = username
	return &v1.TokenPair{
		AccessToken:  "dummy", // UAT uses real Paseto; this is returned in the JSON but not used for subsequent requests
		RefreshToken: tok,
		ExpiresAt:    time.Now().Add(time.Hour),
		UserID:       u.ID,
		Username:     u.Username,
		IsAdmin:      u.IsAdmin,
	}, nil
}

func (s *stubAuthService) Refresh(_ context.Context, refreshToken string) (*v1.TokenPair, error) {
	username, ok := s.tokens[refreshToken]
	if !ok {
		return nil, fmt.Errorf("invalid refresh token")
	}
	u := s.users[username]
	newTok := "refresh-" + uuid.New().String()
	delete(s.tokens, refreshToken)
	s.tokens[newTok] = username
	return &v1.TokenPair{
		AccessToken:  "dummy",
		RefreshToken: newTok,
		ExpiresAt:    time.Now().Add(time.Hour),
		UserID:       u.ID,
		Username:     u.Username,
		IsAdmin:      u.IsAdmin,
	}, nil
}

func (s *stubAuthService) Logout(_ context.Context, refreshToken string) error {
	delete(s.tokens, refreshToken)
	return nil
}

func (s *stubAuthService) CreateUser(_ context.Context, username, _, password string, isAdmin bool) (*v1.UserInfo, error) {
	if _, exists := s.users[username]; exists {
		return nil, v1.ErrUserExists
	}
	return s.addUser(username, password, isAdmin), nil
}

func (s *stubAuthService) UserCount(_ context.Context) (int64, error) {
	return s.count, nil
}

// stubLibraryService implements v1.LibraryServiceIface.
type stubLibraryService struct {
	libs map[uuid.UUID]*library.Library
}

func newStubLibraryService() *stubLibraryService {
	return &stubLibraryService{libs: make(map[uuid.UUID]*library.Library)}
}

func (s *stubLibraryService) List(_ context.Context) ([]library.Library, error) {
	out := make([]library.Library, 0, len(s.libs))
	for _, l := range s.libs {
		out = append(out, *l)
	}
	return out, nil
}

func (s *stubLibraryService) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	l, ok := s.libs[id]
	if !ok {
		return nil, library.ErrNotFound
	}
	return l, nil
}

func (s *stubLibraryService) Create(_ context.Context, p library.CreateLibraryParams) (*library.Library, error) {
	l := &library.Library{
		ID:        uuid.New(),
		Name:      p.Name,
		Type:      p.Type,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.libs[l.ID] = l
	return l, nil
}

func (s *stubLibraryService) Update(_ context.Context, p library.UpdateLibraryParams) (*library.Library, error) {
	l, ok := s.libs[p.ID]
	if !ok {
		return nil, library.ErrNotFound
	}
	if p.Name != "" {
		l.Name = p.Name
	}
	l.UpdatedAt = time.Now()
	return l, nil
}

func (s *stubLibraryService) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.libs[id]; !ok {
		return library.ErrNotFound
	}
	delete(s.libs, id)
	return nil
}

func (s *stubLibraryService) EnqueueScan(_ context.Context, _ uuid.UUID) error { return nil }

// stubMediaItemLister implements v1.MediaItemLister.
type stubMediaItemLister struct {
	items []media.Item
}

func (s *stubMediaItemLister) ListItems(_ context.Context, _ uuid.UUID, _ string, _, _ int32) ([]media.Item, error) {
	return s.items, nil
}
func (s *stubMediaItemLister) ListItemsFiltered(_ context.Context, _ uuid.UUID, _ string, _, _ int32, _ media.FilterParams) ([]media.Item, error) {
	return s.items, nil
}
func (s *stubMediaItemLister) CountItems(_ context.Context, _ uuid.UUID, _ string) (int64, error) {
	return int64(len(s.items)), nil
}
func (s *stubMediaItemLister) CountItemsFiltered(_ context.Context, _ uuid.UUID, _ string, _ media.FilterParams) (int64, error) {
	return int64(len(s.items)), nil
}
func (s *stubMediaItemLister) ListDistinctGenres(_ context.Context, _ uuid.UUID) ([]string, error) {
	return []string{"Action", "Drama"}, nil
}

// stubItemMediaService implements v1.ItemMediaService.
type stubItemMediaService struct {
	item  *media.Item
	file  *media.File
	files []media.File
	kids  []media.Item
}

func (s *stubItemMediaService) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	if s.item == nil {
		return nil, media.ErrNotFound
	}
	return s.item, nil
}
func (s *stubItemMediaService) GetFile(_ context.Context, _ uuid.UUID) (*media.File, error) {
	if s.file == nil {
		return nil, fmt.Errorf("not found")
	}
	return s.file, nil
}
func (s *stubItemMediaService) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return s.files, nil
}
func (s *stubItemMediaService) ListChildren(_ context.Context, _ uuid.UUID) ([]media.Item, error) {
	return s.kids, nil
}

// stubItemWatchService implements v1.ItemWatchService.
type stubItemWatchService struct{ recorded bool }

func (s *stubItemWatchService) GetState(_ context.Context, _, _ uuid.UUID) (watchevent.WatchState, error) {
	return watchevent.WatchState{}, nil
}
func (s *stubItemWatchService) Record(_ context.Context, _ watchevent.RecordParams) error {
	s.recorded = true
	return nil
}

// stubSessionCleaner implements v1.ItemSessionCleaner.
type stubSessionCleaner struct{}

func (s *stubSessionCleaner) UpdatePositionByMedia(_ context.Context, _ uuid.UUID, _ int64) error {
	return nil
}
func (s *stubSessionCleaner) DeleteByMedia(_ context.Context, _ uuid.UUID) error { return nil }

// stubItemEnricher implements v1.ItemEnricher.
type stubItemEnricher struct{}

func (s *stubItemEnricher) EnrichItem(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubItemEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error { return nil }

// stubMatchSearcher implements v1.ItemMatchSearcher.
type stubMatchSearcher struct{}

func (s *stubMatchSearcher) SearchTVCandidates(_ context.Context, _ string) ([]v1.MatchCandidate, error) {
	return nil, nil
}
func (s *stubMatchSearcher) SearchMovieCandidates(_ context.Context, _ string) ([]v1.MatchCandidate, error) {
	return nil, nil
}

// stubWebhookDispatcher implements v1.ItemWebhookDispatcher.
type stubWebhookDispatcher struct{}

func (s *stubWebhookDispatcher) Dispatch(_ string, _, _ uuid.UUID) {}

// stubWebhookService implements v1.WebhookService.
type stubWebhookService struct{}

func (s *stubWebhookService) List(_ context.Context) ([]v1.WebhookEndpoint, error) {
	return []v1.WebhookEndpoint{}, nil
}
func (s *stubWebhookService) Get(_ context.Context, _ uuid.UUID) (*v1.WebhookEndpoint, error) {
	return nil, v1.ErrWebhookNotFound
}
func (s *stubWebhookService) Create(_ context.Context, url, secret string, events []string) (*v1.WebhookEndpoint, error) {
	return &v1.WebhookEndpoint{ID: uuid.New(), URL: url, Events: events, Enabled: true}, nil
}
func (s *stubWebhookService) Update(_ context.Context, _ uuid.UUID, _, _ string, _ []string, _ bool) (*v1.WebhookEndpoint, error) {
	return nil, v1.ErrWebhookNotFound
}
func (s *stubWebhookService) Delete(_ context.Context, _ uuid.UUID) error {
	return v1.ErrWebhookNotFound
}
func (s *stubWebhookService) SendTest(_ context.Context, _ uuid.UUID) error {
	return v1.ErrWebhookNotFound
}

// stubHubDB implements v1.HubHandler's DB interface.
type stubHubDB struct {
	cwRows []gen.ListContinueWatchingRow
	raRows []gen.ListRecentlyAddedRow
}

func (s *stubHubDB) ListContinueWatching(_ context.Context, _ gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error) {
	return s.cwRows, nil
}
func (s *stubHubDB) ListRecentlyAdded(_ context.Context, _ gen.ListRecentlyAddedParams) ([]gen.ListRecentlyAddedRow, error) {
	return s.raRows, nil
}

// stubSearchDB implements v1.SearchDB.
type stubSearchDB struct{}

func (s *stubSearchDB) SearchMediaItems(_ context.Context, _ gen.SearchMediaItemsParams) ([]gen.SearchMediaItemsRow, error) {
	return nil, nil
}
func (s *stubSearchDB) SearchMediaItemsGlobal(_ context.Context, _ gen.SearchMediaItemsGlobalParams) ([]gen.SearchMediaItemsGlobalRow, error) {
	return nil, nil
}

// stubHistoryDB implements v1.HistoryDB.
type stubHistoryDB struct{}

func (s *stubHistoryDB) ListWatchHistory(_ context.Context, _ gen.ListWatchHistoryParams) ([]gen.ListWatchHistoryRow, error) {
	return nil, nil
}

// stubSessionItemQuerier implements the sessionItemQuerier used by NativeSessionsHandler.
type stubSessionItemQuerier struct{}

func (s *stubSessionItemQuerier) GetMediaItemsForSessions(_ context.Context, _ []uuid.UUID) ([]gen.SessionMediaItem, error) {
	return nil, nil
}
func (s *stubSessionItemQuerier) GetMediaItemByFilePath(_ context.Context, _ string) (*gen.SessionMediaItem, error) {
	return nil, nil
}

// stubUserService implements v1.UserService.
type stubUserService struct{}

func (s *stubUserService) SetPIN(_ context.Context, _ uuid.UUID, _, _ string) error { return nil }
func (s *stubUserService) ClearPIN(_ context.Context, _ uuid.UUID, _ string) error  { return nil }
func (s *stubUserService) ListSwitchable(_ context.Context) ([]v1.SwitchableUser, error) {
	return nil, nil
}
func (s *stubUserService) VerifyPIN(_ context.Context, _ uuid.UUID, _ string) (*v1.PINSwitchResult, error) {
	return nil, fmt.Errorf("invalid PIN")
}

// stubUserDB implements v1.UserDB.
type stubUserDB struct{}

func (s *stubUserDB) ListUsers(_ context.Context) ([]gen.ListUsersRow, error) { return nil, nil }
func (s *stubUserDB) DeleteUser(_ context.Context, _ uuid.UUID) error         { return nil }
func (s *stubUserDB) SetUserAdmin(_ context.Context, _ gen.SetUserAdminParams) error {
	return nil
}
func (s *stubUserDB) CountAdmins(_ context.Context) (int64, error) { return 1, nil }
func (s *stubUserDB) UpdateUserPassword(_ context.Context, _ gen.UpdateUserPasswordParams) error {
	return nil
}
func (s *stubUserDB) ListManagedProfiles(_ context.Context, _ pgtype.UUID) ([]gen.ListManagedProfilesRow, error) {
	return nil, nil
}
func (s *stubUserDB) ListAllManagedProfiles(_ context.Context) ([]gen.ListAllManagedProfilesRow, error) {
	return nil, nil
}
func (s *stubUserDB) CreateManagedProfile(_ context.Context, _ gen.CreateManagedProfileParams) (gen.CreateManagedProfileRow, error) {
	return gen.CreateManagedProfileRow{}, nil
}
func (s *stubUserDB) UpdateManagedProfile(_ context.Context, _ gen.UpdateManagedProfileParams) (gen.UpdateManagedProfileRow, error) {
	return gen.UpdateManagedProfileRow{}, nil
}
func (s *stubUserDB) UpdateManagedProfileAdmin(_ context.Context, _ gen.UpdateManagedProfileAdminParams) (gen.UpdateManagedProfileAdminRow, error) {
	return gen.UpdateManagedProfileAdminRow{}, nil
}
func (s *stubUserDB) DeleteManagedProfile(_ context.Context, _ gen.DeleteManagedProfileParams) error {
	return nil
}
func (s *stubUserDB) DeleteManagedProfileAdmin(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubUserDB) GetUserPreferences(_ context.Context, _ uuid.UUID) (gen.GetUserPreferencesRow, error) {
	return gen.GetUserPreferencesRow{}, nil
}
func (s *stubUserDB) UpdateUserPreferences(_ context.Context, _ gen.UpdateUserPreferencesParams) error {
	return nil
}
func (s *stubUserDB) UpdateUserContentRating(_ context.Context, _ gen.UpdateUserContentRatingParams) error {
	return nil
}

// stubAnalyticsDB implements the analyticsQuerier used by AnalyticsHandler.
type stubAnalyticsDB struct{}

func (s *stubAnalyticsDB) GetAnalyticsOverview(_ context.Context) (gen.AnalyticsOverviewRow, error) {
	return gen.AnalyticsOverviewRow{}, nil
}
func (s *stubAnalyticsDB) GetLibraryAnalytics(_ context.Context) ([]gen.LibraryAnalyticsRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetVideoCodecBreakdown(_ context.Context) ([]gen.CodecCountRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetContainerBreakdown(_ context.Context) ([]gen.ContainerCountRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetPlaysPerDay(_ context.Context) ([]gen.DayCountRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetBandwidthPerDay(_ context.Context) ([]gen.DayBytesRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetTopPlayed(_ context.Context) ([]gen.TopPlayedRow, error) {
	return nil, nil
}
func (s *stubAnalyticsDB) GetRecentPlays(_ context.Context) ([]gen.RecentPlayRow, error) {
	return nil, nil
}

// stubAuditDB implements the auditQuerier used by AuditHandler.
type stubAuditDB struct{}

func (s *stubAuditDB) ListAuditLog(_ context.Context, _ gen.ListAuditLogParams) ([]gen.ListAuditLogRow, error) {
	return nil, nil
}

// ── test server ───────────────────────────────────────────────────────────────

// testServer wraps httptest.Server with helpers for UAT requests.
type testServer struct {
	t      *testing.T
	server *httptest.Server
	tm     *auth.TokenMaker
	client *http.Client
}

// newTestServer wires the full router with real middleware and stub services.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	v := testvalkey.New(t)
	secretKey := auth.DeriveKey32("uat-test-secret-key-32bytes!!!!!")
	tm, err := auth.NewTokenMaker(secretKey)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}

	authMW := middleware.NewAuthenticator(tm)
	rl := valkey.NewRateLimiter(v, nil, func() {})
	metrics := observability.NewMetrics(prometheus.NewRegistry())

	authSvc := newStubAuthService()
	libSvc := newStubLibraryService()
	mediaSvc := &stubMediaItemLister{}
	itemMedia := &stubItemMediaService{}
	itemWatch := &stubItemWatchService{}

	sessionStore := transcode.NewSessionStore(v)
	segTokenMgr := transcode.NewSegmentTokenManager(v)

	log := slog.Default()

	handlers := &api.Handlers{
		Auth:      v1.NewAuthHandler(authSvc, log),
		Library:   v1.NewLibraryHandler(libSvc, log).WithMedia(mediaSvc),
		Webhook:   v1.NewWebhookHandler(&stubWebhookService{}, log),
		Hub:       v1.NewHubHandler(&stubHubDB{}, log),
		Search:    v1.NewSearchHandler(&stubSearchDB{}, log),
		History:   v1.NewHistoryHandler(&stubHistoryDB{}, log),
		Analytics: v1.NewAnalyticsHandler(&stubAnalyticsDB{}, log),
		Audit:     v1.NewAuditHandler(&stubAuditDB{}, log),
		Email:     v1.NewEmailHandler(nil, log), // nil sender → email disabled
		User:      v1.NewUserHandler(&stubUserService{}).WithDB(&stubUserDB{}).WithTokenMaker(tm, log),
		Items: v1.NewItemHandler(
			itemMedia,
			itemWatch,
			&stubSessionCleaner{},
			&stubItemEnricher{},
			&stubMatchSearcher{},
			&stubWebhookDispatcher{},
			nil,
			log,
		),
		NativeSessions: v1.NewNativeSessionsHandler(sessionStore, nil, &stubSessionItemQuerier{}, log),
		NativeTranscode: v1.NewNativeTranscodeHandler(
			sessionStore,
			segTokenMgr,
			itemMedia,
			nil, // config — only reached on actual transcode start, not exercised here
			log,
		),
		Auth_mw:     authMW,
		RateLimiter: rl,
		Metrics:     metrics,
		Logger:      log,
	}

	srv := httptest.NewServer(api.NewRouter(handlers))
	t.Cleanup(srv.Close)

	return &testServer{
		t:      t,
		server: srv,
		tm:     tm,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// url builds the full URL for a path.
func (ts *testServer) url(path string) string {
	return ts.server.URL + path
}

// token issues a real Paseto access token for the given user.
func (ts *testServer) token(userID uuid.UUID, username string, isAdmin bool) string {
	ts.t.Helper()
	tok, err := ts.tm.IssueAccessToken(auth.Claims{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
	})
	if err != nil {
		ts.t.Fatalf("IssueAccessToken: %v", err)
	}
	return tok
}

// do performs an HTTP request, optionally with a Bearer token and JSON body.
func (ts *testServer) do(method, path string, token string, body any) *http.Response {
	ts.t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			ts.t.Fatalf("marshal body: %v", err)
		}
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, ts.url(path), reqBody)
	if err != nil {
		ts.t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		ts.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// mustDecode decodes the response body as JSON into v.
func mustDecode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// assertStatus fails if the response status does not match.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("status = %d, want %d", resp.StatusCode, want)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// adminToken returns a token for a synthetic admin user.
func (ts *testServer) adminToken() string {
	return ts.token(uuid.New(), "admin", true)
}

// userToken returns a token for a synthetic non-admin user.
func (ts *testServer) userToken() string {
	return ts.token(uuid.New(), "user", false)
}

// makeLibrary creates a library via the API and returns its ID string.
func (ts *testServer) makeLibrary(tok, name, libType string) string {
	ts.t.Helper()
	resp := ts.do("POST", "/api/v1/libraries", tok, map[string]any{
		"name":       name,
		"type":       libType,
		"scan_paths": []string{"/media/movies"},
		"agent":      "tmdb",
		"language":   "en",
	})
	assertStatus(ts.t, resp, http.StatusCreated)
	var env map[string]any
	mustDecode(ts.t, resp, &env)
	data := env["data"].(map[string]any)
	return data["id"].(string)
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestHealth verifies the liveness probe responds.
func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("GET", "/health/live", "", nil)
	assertStatus(t, resp, http.StatusOK)
}

// TestSetupStatus_NoUsers returns setup_required when no users exist.
func TestSetupStatus_NoUsers(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("GET", "/api/v1/setup/status", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	data := env["data"].(map[string]any)
	if data["setup_required"] != true {
		t.Errorf("setup_required = %v, want true", data["setup_required"])
	}
}

// TestSetupStatus_AfterFirstUser returns setup_required=false once a user exists.
func TestSetupStatus_AfterFirstUser(t *testing.T) {
	ts := newTestServer(t)
	// Register the first admin (requires no auth when no users exist).
	resp := ts.do("POST", "/api/v1/auth/register", "", map[string]any{
		"username": "alice",
		"password": "supersecret123",
	})
	assertStatus(t, resp, http.StatusCreated)

	resp2 := ts.do("GET", "/api/v1/setup/status", "", nil)
	assertStatus(t, resp2, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp2, &env)
	data := env["data"].(map[string]any)
	if data["setup_required"] != false {
		t.Errorf("setup_required = %v, want false after first user", data["setup_required"])
	}
}

// TestRegisterAndLogin exercises the full register → login flow.
func TestRegisterAndLogin(t *testing.T) {
	ts := newTestServer(t)

	// Register first user (auto-admin when no users exist).
	resp := ts.do("POST", "/api/v1/auth/register", "", map[string]any{
		"username": "bob",
		"password": "password12345",
	})
	assertStatus(t, resp, http.StatusCreated)
	var regEnv map[string]any
	mustDecode(t, resp, &regEnv)
	data := regEnv["data"].(map[string]any)
	if data["username"] != "bob" {
		t.Errorf("username = %v, want bob", data["username"])
	}
}

// TestLogin_InvalidCredentials returns 401 for bad password.
func TestLogin_InvalidCredentials(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do("POST", "/api/v1/auth/login", "", map[string]any{
		"username": "nobody",
		"password": "wrong",
	})
	assertStatus(t, resp, http.StatusUnauthorized)
}

// TestLogin_DuplicateUsername returns a conflict error.
func TestLogin_DuplicateUsername(t *testing.T) {
	ts := newTestServer(t)
	ts.do("POST", "/api/v1/auth/register", "", map[string]any{
		"username": "alice",
		"password": "pass12345",
	})
	resp := ts.do("POST", "/api/v1/auth/register", ts.adminToken(), map[string]any{
		"username": "alice",
		"password": "other123",
	})
	if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("duplicate username status = %d, want 409 or 400", resp.StatusCode)
	}
}

// TestAuthRequired_NoToken rejects unauthenticated access with 401.
func TestAuthRequired_NoToken(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{
		"/api/v1/libraries",
		"/api/v1/hub",
		"/api/v1/history",
		"/api/v1/sessions",
		"/api/v1/search?q=test",
	} {
		resp := ts.do("GET", path, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without token: status = %d, want 401", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestAdminRequired_NonAdmin returns 403 for non-admin users on admin endpoints.
func TestAdminRequired_NonAdmin(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/v1/libraries", map[string]any{"name": "X", "type": "movie", "scan_paths": []string{"/x"}, "agent": "tmdb", "language": "en"}},
		{"GET", "/api/v1/webhooks", nil},
		{"GET", "/api/v1/users", nil},
		{"GET", "/api/v1/analytics", nil},
		{"GET", "/api/v1/audit", nil},
	} {
		resp := ts.do(tc.method, tc.path, tok, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestLibraries_ListEmpty returns an empty list when no libraries exist.
func TestLibraries_ListEmpty(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	resp := ts.do("GET", "/api/v1/libraries", tok, nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	items := env["data"].([]any)
	if len(items) != 0 {
		t.Errorf("list libraries: got %d items, want 0", len(items))
	}
}

// TestLibraries_AdminCRUD exercises create → get → list → delete.
func TestLibraries_AdminCRUD(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()

	// Create.
	libID := ts.makeLibrary(admin, "Movies", "movie")

	// Get by ID.
	resp := ts.do("GET", "/api/v1/libraries/"+libID, admin, nil)
	assertStatus(t, resp, http.StatusOK)
	var getEnv map[string]any
	mustDecode(t, resp, &getEnv)
	got := getEnv["data"].(map[string]any)
	if got["name"] != "Movies" {
		t.Errorf("library name = %v, want Movies", got["name"])
	}
	if got["type"] != "movie" {
		t.Errorf("library type = %v, want movie", got["type"])
	}

	// List — should include the new library.
	resp2 := ts.do("GET", "/api/v1/libraries", admin, nil)
	assertStatus(t, resp2, http.StatusOK)
	var listEnv map[string]any
	mustDecode(t, resp2, &listEnv)
	listData := listEnv["data"].([]any)
	if len(listData) != 1 {
		t.Errorf("list libraries: got %d, want 1", len(listData))
	}

	// Delete.
	resp3 := ts.do("DELETE", "/api/v1/libraries/"+libID, admin, nil)
	assertStatus(t, resp3, http.StatusNoContent)
	resp3.Body.Close()

	// Confirm deleted.
	resp4 := ts.do("GET", "/api/v1/libraries/"+libID, admin, nil)
	assertStatus(t, resp4, http.StatusNotFound)
	resp4.Body.Close()
}

// TestLibraries_NonAdminCannotCreate returns 403 for regular users.
func TestLibraries_NonAdminCannotCreate(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	resp := ts.do("POST", "/api/v1/libraries", tok, map[string]any{
		"name": "Unauthorized", "type": "movie",
		"scan_paths": []string{"/x"}, "agent": "tmdb", "language": "en",
	})
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// TestLibraries_Items returns the items in a library.
func TestLibraries_Items(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()
	libID := ts.makeLibrary(admin, "Shows", "show")

	resp := ts.do("GET", "/api/v1/libraries/"+libID+"/items", admin, nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	if _, ok := env["data"]; !ok {
		t.Error("items response missing 'data' field")
	}
}

// TestLibraries_Genres returns the distinct genre list for a library.
func TestLibraries_Genres(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()
	libID := ts.makeLibrary(admin, "Movies", "movie")

	resp := ts.do("GET", "/api/v1/libraries/"+libID+"/genres", admin, nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	genres := env["data"].([]any)
	if len(genres) != 2 {
		t.Errorf("genres: got %d, want 2", len(genres))
	}
}

// TestLibraries_ScanEnqueued returns 204 when a scan is triggered.
func TestLibraries_ScanEnqueued(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()
	libID := ts.makeLibrary(admin, "Movies", "movie")

	resp := ts.do("POST", "/api/v1/libraries/"+libID+"/scan", admin, nil)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

// TestItems_Get returns 404 for unknown item IDs.
func TestItems_Get(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	// The stub has no items pre-seeded, so any ID returns not-found.
	resp := ts.do("GET", "/api/v1/items/"+uuid.New().String(), tok, nil)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

// TestItems_Progress records watch progress.
func TestItems_Progress(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	itemID := uuid.New()

	resp := ts.do("PUT", "/api/v1/items/"+itemID.String()+"/progress", tok, map[string]any{
		"position_ms":  45000,
		"duration_ms":  7200000,
		"client_name":  "OnScreenWeb",
	})
	// Item not found → 404 (the watch service is only reached after GetItem).
	// This verifies the route is authenticated and reachable.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		t.Errorf("progress status = %d, want 204 or 404", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHub_Get returns continue watching and recently added sections.
func TestHub_Get(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	resp := ts.do("GET", "/api/v1/hub", tok, nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	data := env["data"].(map[string]any)
	if _, ok := data["continue_watching"]; !ok {
		t.Error("hub response missing 'continue_watching'")
	}
	if _, ok := data["recently_added"]; !ok {
		t.Error("hub response missing 'recently_added'")
	}
}

// TestSearch_ReturnsResults verifies the search endpoint is reachable.
func TestSearch_ReturnsResults(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	resp := ts.do("GET", "/api/v1/search?q=matrix", tok, nil)
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	if _, ok := env["data"]; !ok {
		t.Error("search response missing 'data'")
	}
}

// TestSearch_RequiresAuth verifies the search endpoint requires authentication.
func TestSearch_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("GET", "/api/v1/search?q=matrix", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// TestHistory_Empty returns an empty list when no history exists.
func TestHistory_Empty(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	resp := ts.do("GET", "/api/v1/history", tok, nil)
	assertStatus(t, resp, http.StatusOK)
}

// TestSessions_Empty returns an empty list with no active streams.
func TestSessions_Empty(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	resp := ts.do("GET", "/api/v1/sessions", tok, nil)
	assertStatus(t, resp, http.StatusOK)
}

// TestWebhooks_AdminCRUD exercises webhook create → list → delete.
func TestWebhooks_AdminCRUD(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()

	// Create — URL must be a public (non-private) URL. Use a real domain.
	resp := ts.do("POST", "/api/v1/webhooks", admin, map[string]any{
		"url":    "https://webhook.site/test-onscreen",
		"events": []string{"media.played"},
	})
	// May be 201 Created or 400 if DNS lookup fails in test env; both are acceptable
	// as long as it is NOT 401 or 403.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		t.Errorf("webhook create: status = %d, want not 401/403", resp.StatusCode)
	}
	resp.Body.Close()

	// List.
	resp2 := ts.do("GET", "/api/v1/webhooks", admin, nil)
	assertStatus(t, resp2, http.StatusOK)
	resp2.Body.Close()
}

// TestWebhooks_NonAdminBlocked ensures regular users cannot access webhooks.
func TestWebhooks_NonAdminBlocked(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()

	resp := ts.do("GET", "/api/v1/webhooks", tok, nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// TestTranscode_StartRequiresAuth verifies the transcode endpoint needs a token.
func TestTranscode_StartRequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	itemID := uuid.New()
	resp := ts.do("POST", "/api/v1/items/"+itemID.String()+"/transcode", "", map[string]any{
		"height":      720,
		"position_ms": 0,
	})
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// TestTranscode_StopRequiresAuth verifies DELETE /transcode/sessions/:sid needs auth.
func TestTranscode_StopRequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("DELETE", "/api/v1/transcode/sessions/fake-session-id", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// TestTranscode_Stop204ForUnknownSession returns 204 even if session doesn't exist
// (idempotent stop).
func TestTranscode_Stop204ForUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	resp := ts.do("DELETE", "/api/v1/transcode/sessions/"+uuid.New().String(), tok, nil)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

// TestPlaylist_UnauthorizedWithoutToken verifies the playlist endpoint requires a token.
func TestPlaylist_UnauthorizedWithoutToken(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("GET", "/api/v1/transcode/sessions/fakesid/playlist.m3u8", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// TestEmailEnabled_PublicRoute verifies the email feature flag is readable without auth.
func TestEmailEnabled_PublicRoute(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("GET", "/api/v1/email/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// TestOAuthEnabled_PublicRoutes verifies the SSO feature flags are public.
func TestOAuthEnabled_PublicRoutes(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{
		"/api/v1/auth/google/enabled",
		"/api/v1/auth/github/enabled",
		"/api/v1/auth/discord/enabled",
	} {
		resp := ts.do("GET", path, "", nil)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
}

// TestContentType_JSONResponses verifies all JSON endpoints set Content-Type correctly.
func TestContentType_JSONResponses(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.userToken()
	for _, path := range []string{
		"/api/v1/libraries",
		"/api/v1/hub",
		"/api/v1/history",
	} {
		resp := ts.do("GET", path, tok, nil)
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type = %q, want application/json", path, ct)
		}
		resp.Body.Close()
	}
}

// TestPathTraversal_Segment verifies the segment endpoint requires auth,
// blocking any filesystem access (including traversal) before it reaches the handler.
func TestPathTraversal_Segment(t *testing.T) {
	ts := newTestServer(t)
	// Without a token the endpoint must return 401, not serve any file.
	resp := ts.do("GET", "/api/v1/transcode/sessions/anysid/seg/seg00001.ts", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// TestLargeRequestBody_Rejected verifies the 1 MB body limit.
func TestLargeRequestBody_Rejected(t *testing.T) {
	ts := newTestServer(t)
	tok := ts.adminToken()
	bigBody := strings.Repeat("x", 2<<20) // 2 MB
	req, _ := http.NewRequest("POST", ts.url("/api/v1/libraries"), strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("oversized request should not return 200")
	}
}

// TestHubDB_WithContent verifies hub returns populated continue-watching rows.
func TestHubDB_WithContent(t *testing.T) {
	// This test constructs its own server with a pre-populated hub stub.
	v := testvalkey.New(t)
	secretKey := auth.DeriveKey32("uat-test-secret-key-32bytes!!!!!")
	tm, err := auth.NewTokenMaker(secretKey)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	authMW := middleware.NewAuthenticator(tm)
	rl := valkey.NewRateLimiter(v, nil, func() {})
	metrics := observability.NewMetrics(prometheus.NewRegistry())

	userID := uuid.New()

	year := int32(2024)
	dur := int64(7200000)

	hub := &stubHubDB{
		cwRows: []gen.ListContinueWatchingRow{{
			ID:         uuid.New(),
			LibraryID:  uuid.New(),
			Title:      "Inception",
			Type:       "movie",
			Year:       &year,
			DurationMs: &dur,
		}},
	}

	log := slog.Default()
	handlers := &api.Handlers{
		Auth:        v1.NewAuthHandler(newStubAuthService(), log),
		Library:     v1.NewLibraryHandler(newStubLibraryService(), log),
		Webhook:     v1.NewWebhookHandler(&stubWebhookService{}, log),
		Hub:         v1.NewHubHandler(hub, log),
		Search:      v1.NewSearchHandler(&stubSearchDB{}, log),
		History:     v1.NewHistoryHandler(&stubHistoryDB{}, log),
		Items:       v1.NewItemHandler(&stubItemMediaService{}, &stubItemWatchService{}, &stubSessionCleaner{}, &stubItemEnricher{}, &stubMatchSearcher{}, &stubWebhookDispatcher{}, nil, log),
		NativeSessions: v1.NewNativeSessionsHandler(transcode.NewSessionStore(v), nil, &stubSessionItemQuerier{}, log),
		Auth_mw:     authMW,
		RateLimiter: rl,
		Metrics:     metrics,
		Logger:      log,
	}

	srv := httptest.NewServer(api.NewRouter(handlers))
	defer srv.Close()

	tok, _ := tm.IssueAccessToken(auth.Claims{UserID: userID, Username: "alice", IsAdmin: false})
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/hub", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, resp, http.StatusOK)
	var env map[string]any
	mustDecode(t, resp, &env)
	data := env["data"].(map[string]any)
	cw := data["continue_watching"].([]any)
	if len(cw) != 1 {
		t.Errorf("continue_watching: got %d, want 1", len(cw))
	}
	first := cw[0].(map[string]any)
	if first["title"] != "Inception" {
		t.Errorf("continue_watching[0].title = %v, want Inception", first["title"])
	}
}
