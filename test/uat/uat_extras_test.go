// uat_extras_test.go — additional UAT coverage beyond the original suite.
//
// These tests target API surfaces that uat_test.go doesn't reach: the
// recently-added handlers (lyrics, people, capabilities, trickplay
// hardening), the password-reset / invite flows, and the admin gates on
// settings / plugins / tasks / arr-services / audit / notifications.
//
// Same package as uat_test.go so we can reuse the existing stub implementations.
package uat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onscreen/onscreen/internal/api"
	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/people"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/livetv"
	"github.com/onscreen/onscreen/internal/lyrics"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/plugin"
	"github.com/onscreen/onscreen/internal/requests"
	"github.com/onscreen/onscreen/internal/scheduler"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/trickplay"
	"github.com/onscreen/onscreen/internal/valkey"
)

// ── extras-only stubs ────────────────────────────────────────────────────────

// stubPasswordResetDB satisfies v1.PasswordResetDB with no-ops. Tests that
// hit the reset flow override fields directly.
type stubPasswordResetDB struct {
	user    v1.PRUser
	userErr error

	createTokenErr    error
	createTokenCalled bool

	token    v1.PRToken
	tokenErr error

	updateErr error

	bumpEpochCalled    bool
	deleteSessionsCall bool
}

func (s *stubPasswordResetDB) GetUserByEmail(_ context.Context, _ *string) (v1.PRUser, error) {
	return s.user, s.userErr
}
func (s *stubPasswordResetDB) CreateResetToken(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	s.createTokenCalled = true
	return s.createTokenErr
}
func (s *stubPasswordResetDB) GetResetToken(_ context.Context, _ string) (v1.PRToken, error) {
	return s.token, s.tokenErr
}
func (s *stubPasswordResetDB) MarkResetTokenUsed(_ context.Context, _ uuid.UUID) (bool, error) {
	return true, nil
}
func (s *stubPasswordResetDB) UpdatePassword(_ context.Context, _ uuid.UUID, _ string) error {
	return s.updateErr
}
func (s *stubPasswordResetDB) BumpSessionEpoch(_ context.Context, _ uuid.UUID) error {
	s.bumpEpochCalled = true
	return nil
}
func (s *stubPasswordResetDB) DeleteSessionsForUser(_ context.Context, _ uuid.UUID) error {
	s.deleteSessionsCall = true
	return nil
}

// stubLyricsStore satisfies v1.LyricsStore.
type stubLyricsStore struct {
	plain, synced string
}

func (s *stubLyricsStore) GetLyrics(_ context.Context, _ uuid.UUID) (string, string, error) {
	return s.plain, s.synced, nil
}
func (s *stubLyricsStore) SetLyrics(_ context.Context, _ uuid.UUID, _, _ string) error { return nil }

// stubLyricsItems satisfies v1.LyricsItemSource.
type stubLyricsItems struct {
	item *media.Item
}

func (s *stubLyricsItems) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	if s.item == nil {
		return nil, media.ErrNotFound
	}
	return s.item, nil
}
func (s *stubLyricsItems) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return nil, nil
}
func (s *stubLyricsItems) GetTrackMetadata(_ context.Context, _ uuid.UUID) (string, string, error) {
	return "", "", nil
}

// stubLyricsFetcher satisfies lyrics.Fetcher.
type stubLyricsFetcher struct{}

func (stubLyricsFetcher) Lookup(_ context.Context, _ lyrics.LookupParams) (lyrics.Result, error) {
	return lyrics.Result{}, lyrics.ErrNotFound
}

// stubPeopleService satisfies v1.PeopleService.
type stubPeopleService struct {
	credits []people.Credit
	person  people.Person
	films   []people.FilmographyEntry
	results []people.Summary
}

func (s *stubPeopleService) GetCredits(_ context.Context, _ uuid.UUID, _ string, _ *int) ([]people.Credit, error) {
	return s.credits, nil
}
func (s *stubPeopleService) GetPerson(_ context.Context, _ uuid.UUID) (people.Person, error) {
	return s.person, nil
}
func (s *stubPeopleService) GetFilmography(_ context.Context, _ uuid.UUID) ([]people.FilmographyEntry, error) {
	return s.films, nil
}
func (s *stubPeopleService) Search(_ context.Context, _ string, _ int32) ([]people.Summary, error) {
	return s.results, nil
}

// stubPeopleItems satisfies v1.PeopleItemLookup.
type stubPeopleItems struct{}

func (s *stubPeopleItems) GetItemTypeAndTMDB(_ context.Context, _ uuid.UUID) (string, *int, error) {
	return "movie", nil, nil
}
func (s *stubPeopleItems) ResolveTMDBID(_ context.Context, _ uuid.UUID) (*int, error) {
	return nil, errors.New("not resolved")
}

// stubFavoritesDB satisfies v1.FavoritesDB.
type stubFavoritesDB struct{}

func (s *stubFavoritesDB) AddFavorite(_ context.Context, _ gen.AddFavoriteParams) error    { return nil }
func (s *stubFavoritesDB) RemoveFavorite(_ context.Context, _ gen.RemoveFavoriteParams) error {
	return nil
}
func (s *stubFavoritesDB) IsFavorite(_ context.Context, _ gen.IsFavoriteParams) (bool, error) {
	return false, nil
}
func (s *stubFavoritesDB) ListFavorites(_ context.Context, _ gen.ListFavoritesParams) ([]gen.ListFavoritesRow, error) {
	return nil, nil
}
func (s *stubFavoritesDB) CountFavorites(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}

// stubNotificationDB satisfies v1.NotificationDB.
type stubNotificationDB struct{}

func (s *stubNotificationDB) ListNotifications(_ context.Context, _ gen.ListNotificationsParams) ([]gen.Notification, error) {
	return nil, nil
}
func (s *stubNotificationDB) CountUnreadNotifications(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (s *stubNotificationDB) MarkNotificationRead(_ context.Context, _ gen.MarkNotificationReadParams) error {
	return nil
}
func (s *stubNotificationDB) MarkAllNotificationsRead(_ context.Context, _ uuid.UUID) error {
	return nil
}

// stubCapabilities satisfies v1.CapabilitiesProvider.
type stubCapabilities struct {
	resp v1.CapabilitiesResponse
}

func (s *stubCapabilities) Capabilities() v1.CapabilitiesResponse { return s.resp }

// stubTrickplayService satisfies v1.TrickplayService.
type stubTrickplayService struct{}

func (s *stubTrickplayService) Status(_ context.Context, _ uuid.UUID) (trickplay.Spec, string, int, bool, error) {
	return trickplay.Spec{}, "not_started", 0, false, nil
}
func (s *stubTrickplayService) Generate(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubTrickplayService) ItemDir(_ uuid.UUID) string                    { return "/tmp/trickplay" }

// stubTrickplayMedia satisfies v1.TrickplayMediaLookup.
type stubTrickplayMedia struct{}

func (s *stubTrickplayMedia) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	return &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"}, nil
}

// stubSettingsService satisfies v1.SettingsServiceIface with zero-value
// reads. Sufficient for admin-gate tests; functional tests would override
// individual methods.
type stubSettingsService struct{}

func (s *stubSettingsService) TMDBAPIKey(_ context.Context) string                 { return "" }
func (s *stubSettingsService) SetTMDBAPIKey(_ context.Context, _ string) error    { return nil }
func (s *stubSettingsService) TVDBAPIKey(_ context.Context) string                 { return "" }
func (s *stubSettingsService) SetTVDBAPIKey(_ context.Context, _ string) error    { return nil }
func (s *stubSettingsService) ArrAPIKey(_ context.Context) string                  { return "" }
func (s *stubSettingsService) SetArrAPIKey(_ context.Context, _ string) error     { return nil }
func (s *stubSettingsService) ArrPathMappings(_ context.Context) map[string]string { return nil }
func (s *stubSettingsService) SetArrPathMappings(_ context.Context, _ map[string]string) error {
	return nil
}
func (s *stubSettingsService) TranscodeEncoders(_ context.Context) string             { return "" }
func (s *stubSettingsService) SetTranscodeEncoders(_ context.Context, _ string) error { return nil }
func (s *stubSettingsService) WorkerFleet(_ context.Context) settings.WorkerFleetConfig {
	return settings.WorkerFleetConfig{EmbeddedEnabled: true}
}
func (s *stubSettingsService) SetWorkerFleet(_ context.Context, _ settings.WorkerFleetConfig) error {
	return nil
}
func (s *stubSettingsService) TranscodeConfigGet(_ context.Context) settings.TranscodeConfig {
	return settings.TranscodeConfig{}
}
func (s *stubSettingsService) SetTranscodeConfig(_ context.Context, _ settings.TranscodeConfig) error {
	return nil
}
func (s *stubSettingsService) OpenSubtitles(_ context.Context) settings.OpenSubtitlesConfig {
	return settings.OpenSubtitlesConfig{}
}
func (s *stubSettingsService) SetOpenSubtitles(_ context.Context, _ settings.OpenSubtitlesConfig) error {
	return nil
}
func (s *stubSettingsService) OIDC(_ context.Context) settings.OIDCConfig         { return settings.OIDCConfig{} }
func (s *stubSettingsService) SetOIDC(_ context.Context, _ settings.OIDCConfig) error { return nil }
func (s *stubSettingsService) LDAP(_ context.Context) settings.LDAPConfig         { return settings.LDAPConfig{} }
func (s *stubSettingsService) SetLDAP(_ context.Context, _ settings.LDAPConfig) error { return nil }
func (s *stubSettingsService) SAML(_ context.Context) settings.SAMLConfig         { return settings.SAMLConfig{} }
func (s *stubSettingsService) SetSAML(_ context.Context, _ settings.SAMLConfig) error { return nil }
func (s *stubSettingsService) SMTP(_ context.Context) settings.SMTPConfig         { return settings.SMTPConfig{} }
func (s *stubSettingsService) SetSMTP(_ context.Context, _ settings.SMTPConfig) error { return nil }
func (s *stubSettingsService) OTel(_ context.Context) settings.OTelConfig         { return settings.OTelConfig{} }
func (s *stubSettingsService) SetOTel(_ context.Context, _ settings.OTelConfig) error { return nil }
func (s *stubSettingsService) General(_ context.Context) settings.GeneralConfig {
	return settings.GeneralConfig{}
}
func (s *stubSettingsService) SetGeneral(_ context.Context, _ settings.GeneralConfig) error {
	return nil
}

// stubInviteDB satisfies v1.InviteDB with the minimum the admin-gate test needs.
type stubInviteDB struct{}

func (s *stubInviteDB) CreateInviteToken(_ context.Context, _ uuid.UUID, _ string, _ *string, _ time.Time) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (s *stubInviteDB) GetInviteToken(_ context.Context, _ string) (v1.InviteTokenRow, error) {
	return v1.InviteTokenRow{}, errors.New("not found")
}
func (s *stubInviteDB) MarkInviteTokenUsed(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}
func (s *stubInviteDB) ListInviteTokens(_ context.Context) ([]v1.InviteTokenSummaryRow, error) {
	return nil, nil
}
func (s *stubInviteDB) DeleteInviteToken(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubInviteDB) CreateUser(_ context.Context, _ string, _ *string, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (s *stubInviteDB) GrantAutoLibrariesToUser(_ context.Context, _ uuid.UUID) error {
	return nil
}

// stubAuditDBExtras satisfies v1.auditQuerier (the admin-gate test only needs
// the route to be registered; the handler returns whatever this returns).
// We share with the existing stubAuditDB defined in uat_test.go.

// stubArrServicesDB satisfies v1.ArrServicesDB.
type stubArrServicesDB struct{}

func (s *stubArrServicesDB) ListArrServices(_ context.Context) ([]gen.ArrService, error) {
	return nil, nil
}
func (s *stubArrServicesDB) GetArrService(_ context.Context, _ uuid.UUID) (gen.ArrService, error) {
	return gen.ArrService{}, errors.New("not found")
}
func (s *stubArrServicesDB) CreateArrService(_ context.Context, _ gen.CreateArrServiceParams) (gen.ArrService, error) {
	return gen.ArrService{}, nil
}
func (s *stubArrServicesDB) UpdateArrService(_ context.Context, _ gen.UpdateArrServiceParams) (gen.ArrService, error) {
	return gen.ArrService{}, nil
}
func (s *stubArrServicesDB) DeleteArrService(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubArrServicesDB) SetArrServiceDefault(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (s *stubArrServicesDB) ClearArrServiceDefault(_ context.Context, _ string) error {
	return nil
}

// stubTasksQuerier satisfies v1.TasksQuerier.
type stubTasksQuerier struct{}

func (s *stubTasksQuerier) ListScheduledTasks(_ context.Context) ([]gen.ScheduledTask, error) {
	return nil, nil
}
func (s *stubTasksQuerier) GetScheduledTask(_ context.Context, _ uuid.UUID) (gen.ScheduledTask, error) {
	return gen.ScheduledTask{}, errors.New("not found")
}
func (s *stubTasksQuerier) CreateScheduledTask(_ context.Context, _ gen.CreateScheduledTaskParams) (gen.ScheduledTask, error) {
	return gen.ScheduledTask{}, nil
}
func (s *stubTasksQuerier) UpdateScheduledTask(_ context.Context, _ gen.UpdateScheduledTaskParams) (gen.ScheduledTask, error) {
	return gen.ScheduledTask{}, nil
}
func (s *stubTasksQuerier) DeleteScheduledTask(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubTasksQuerier) SetScheduledTaskNextRun(_ context.Context, _ gen.SetScheduledTaskNextRunParams) error {
	return nil
}
func (s *stubTasksQuerier) ListTaskRuns(_ context.Context, _ gen.ListTaskRunsParams) ([]gen.TaskRun, error) {
	return nil, nil
}

// ── server builder (extras) ──────────────────────────────────────────────────

// newExtrasServer wires a router with the extras-tested handlers attached.
// Not all extras need it (auth-only routes can use the original newTestServer
// since the route is already registered there); this exists for the routes
// that aren't yet wired in newTestServer.
func newExtrasServer(t *testing.T, mutate func(*api.Handlers)) *testServer {
	t.Helper()

	v := testvalkey.New(t)
	secretKey := auth.DeriveKey32("uat-test-secret-key-32bytes!!!!!")
	tm, err := auth.NewTokenMaker(secretKey)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	authMW := middleware.NewAuthenticator(tm)

	log := slog.Default()
	handlers := &api.Handlers{
		Auth_mw:     authMW,
		RateLimiter: valkey.NewRateLimiter(v, nil, func() {}),
		Metrics:     observability.NewMetrics(prometheus.NewRegistry()),
		Logger:      log,
	}
	if mutate != nil {
		mutate(handlers)
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

// ── Capabilities — public, no auth ───────────────────────────────────────────

func TestCapabilities_PublicEndpointReturnsShape(t *testing.T) {
	want := v1.CapabilitiesResponse{
		Server: v1.CapabilitiesServer{
			Name: "OnScreen", MachineID: "abc-123",
			Version: "v1.2.3", APIVersion: "v1",
		},
		Features: v1.CapabilitiesFeatures{
			Transcode: true, Trickplay: true, Music: true,
		},
	}
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Capabilities = v1.NewCapabilitiesHandler(&stubCapabilities{resp: want})
	})

	resp := ts.do("GET", "/api/v1/system/capabilities", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data v1.CapabilitiesResponse `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Server.Version != "v1.2.3" {
		t.Errorf("version: got %q, want v1.2.3", env.Data.Server.Version)
	}
	if !env.Data.Features.Transcode {
		t.Error("Transcode feature flag should be true in the response")
	}
}

// ── Trickplay — H1 regression: unauthenticated request must NOT hit the handler ─

func TestTrickplay_ServeFile_RequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Trickplay = v1.NewTrickplayHandler(&stubTrickplayService{}, &stubTrickplayMedia{}, slog.Default())
	})

	resp := ts.do("GET", "/trickplay/"+uuid.New().String()+"/index.vtt", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — H1 regression: trickplay must require auth", resp.StatusCode)
	}
}

func TestTrickplay_ServeFile_AcceptsBearer(t *testing.T) {
	// Auth wired and route reachable. The handler returns 404 because the
	// stub item dir doesn't actually contain index.vtt — that's fine; the
	// point is the auth gate let the request through.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Trickplay = v1.NewTrickplayHandler(&stubTrickplayService{}, &stubTrickplayMedia{}, slog.Default())
	})

	tok := ts.userToken()
	resp := ts.do("GET", "/trickplay/"+uuid.New().String()+"/index.vtt", tok, nil)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("Bearer-authenticated request still got 401 — auth wiring broken")
	}
}

// ── Forgot password — public flow, anti-enumeration ──────────────────────────

func TestForgotPassword_EnabledFlagPublicWithoutSMTP(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		// nil/disabled sender — the handler still has to publish an
		// /enabled endpoint that just returns false rather than 500ing.
		h.PasswordReset = v1.NewPasswordResetHandler(
			&stubPasswordResetDB{}, email.NewSender(nil), "http://localhost", slog.Default())
	})

	resp := ts.do("GET", "/api/v1/auth/forgot-password/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data map[string]bool `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data["enabled"] {
		t.Error("expected enabled=false when SMTP is not configured")
	}
}

func TestForgotPassword_NoSMTP_StillReturns200(t *testing.T) {
	// /forgot-password must always return 200 regardless of SMTP state —
	// otherwise an attacker probing for valid emails sees an obvious
	// differential between "configured-and-sent" and "not-configured".
	// The handler short-circuits before creating a reset token when SMTP
	// is off, so we assert createTokenCalled stays false too: response
	// is generic, side effects are zero.
	db := &stubPasswordResetDB{}
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.PasswordReset = v1.NewPasswordResetHandler(
			db, email.NewSender(nil), "http://localhost", slog.Default())
	})

	resp := ts.do("POST", "/api/v1/auth/forgot-password", "", map[string]any{
		"email": "alice@example.com",
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (must not differentiate on SMTP state)", resp.StatusCode)
	}
	if db.createTokenCalled {
		t.Error("CreateResetToken must not be called when SMTP is off")
	}
}

func TestPasswordReset_RejectsShortPassword(t *testing.T) {
	// Password policy floor (12 chars) must be enforced at the API layer
	// regardless of which path lands here. Locks down the M1 fix.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.PasswordReset = v1.NewPasswordResetHandler(
			&stubPasswordResetDB{}, email.NewSender(nil), "http://localhost", slog.Default())
	})

	resp := ts.do("POST", "/api/v1/auth/reset-password", "", map[string]any{
		"token":    "some-token",
		"password": "shortpw", // 7 chars — below the 12-char floor
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for short password", resp.StatusCode)
	}
}

func TestPasswordReset_BadTokenIs400(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.PasswordReset = v1.NewPasswordResetHandler(
			&stubPasswordResetDB{tokenErr: errors.New("not found")},
			email.NewSender(nil), "http://localhost", slog.Default(),
		)
	})

	resp := ts.do("POST", "/api/v1/auth/reset-password", "", map[string]any{
		"token":    "bogus",
		"password": "twelvechars1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown token", resp.StatusCode)
	}
}

func TestPasswordReset_SuccessRevokesEverySession(t *testing.T) {
	// H2/H3 regression: a successful reset must invoke both
	// BumpSessionEpoch (revokes PASETO access tokens) AND
	// DeleteSessionsForUser (revokes refresh tokens). Without these,
	// a stolen-credential reset leaves the attacker's session live.
	db := &stubPasswordResetDB{
		token:     v1.PRToken{ID: uuid.New(), UserID: uuid.New()},
		updateErr: nil,
	}
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.PasswordReset = v1.NewPasswordResetHandler(
			db, email.NewSender(nil), "http://localhost", slog.Default(),
		).WithSegmentTokenRevoker(nil)
	})

	resp := ts.do("POST", "/api/v1/auth/reset-password", "", map[string]any{
		"token":    "a-real-token",
		"password": "twelvechars1",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
	if !db.bumpEpochCalled {
		t.Error("BumpSessionEpoch was NOT called on successful reset (H2 regression)")
	}
	if !db.deleteSessionsCall {
		t.Error("DeleteSessionsForUser was NOT called on successful reset (H3 regression)")
	}
}

// ── Lyrics ───────────────────────────────────────────────────────────────────

func TestLyrics_RequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Lyrics = v1.NewLyricsHandler(&stubLyricsStore{}, &stubLyricsItems{}, stubLyricsFetcher{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/items/"+uuid.New().String()+"/lyrics", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestLyrics_ReturnsCachedSyncedLyrics(t *testing.T) {
	itemID := uuid.New()
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Lyrics = v1.NewLyricsHandler(
			&stubLyricsStore{plain: "I am here", synced: "[00:00.00]I am here"},
			&stubLyricsItems{item: &media.Item{
				ID: itemID, LibraryID: uuid.New(), Type: "track", Title: "Time",
			}},
			stubLyricsFetcher{},
			slog.Default(),
		)
	})

	resp := ts.do("GET", "/api/v1/items/"+itemID.String()+"/lyrics", ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data v1.LyricsResponse `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Synced != "[00:00.00]I am here" || env.Data.Plain != "I am here" {
		t.Errorf("lyrics not returned from cache: %+v", env.Data)
	}
}

func TestLyrics_NonTrackReturns404(t *testing.T) {
	itemID := uuid.New()
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Lyrics = v1.NewLyricsHandler(
			&stubLyricsStore{},
			&stubLyricsItems{item: &media.Item{
				ID: itemID, LibraryID: uuid.New(), Type: "movie", Title: "Not a track",
			}},
			stubLyricsFetcher{},
			slog.Default(),
		)
	})

	resp := ts.do("GET", "/api/v1/items/"+itemID.String()+"/lyrics", ts.userToken(), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-track lyrics: status = %d, want 404", resp.StatusCode)
	}
}

// ── People ───────────────────────────────────────────────────────────────────

func TestPeople_SearchRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.People = v1.NewPeopleHandler(&stubPeopleService{}, &stubPeopleItems{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/people?q=keanu", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPeople_SearchReturnsResults(t *testing.T) {
	tmdbID := 6384
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.People = v1.NewPeopleHandler(&stubPeopleService{
			results: []people.Summary{
				{ID: uuid.New(), TMDBID: &tmdbID, Name: "Keanu Reeves"},
			},
		}, &stubPeopleItems{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/people?q=keanu", ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data []map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 || env.Data[0]["name"] != "Keanu Reeves" {
		t.Errorf("unexpected results: %+v", env.Data)
	}
}

// ── Favorites ────────────────────────────────────────────────────────────────

func TestFavorites_ListRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Favorites = v1.NewFavoritesHandler(&stubFavoritesDB{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/favorites", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// ── Notifications ────────────────────────────────────────────────────────────

func TestNotifications_ListRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		brk := notification.NewBroker()
		h.Notifications = v1.NewNotificationHandler(&stubNotificationDB{}, brk, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/notifications", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestNotifications_UnreadCountRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		brk := notification.NewBroker()
		h.Notifications = v1.NewNotificationHandler(&stubNotificationDB{}, brk, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/notifications/unread-count", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// ── Settings — admin gate ────────────────────────────────────────────────────

func TestSettings_GetRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Settings = v1.NewSettingsHandler(&stubSettingsService{}, slog.Default())
	})

	// Non-admin → 403.
	resp := ts.do("GET", "/api/v1/settings", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin status = %d, want 403", resp.StatusCode)
	}
}

func TestSettings_GetSucceedsAsAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Settings = v1.NewSettingsHandler(&stubSettingsService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/settings", ts.adminToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

// ── ArrServices — admin gate ─────────────────────────────────────────────────

func TestArrServices_ListRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.ArrServices = v1.NewArrServicesHandler(&stubArrServicesDB{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/arr-services", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestArrServices_ListAcceptsAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.ArrServices = v1.NewArrServicesHandler(&stubArrServicesDB{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/arr-services", ts.adminToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

// ── Tasks — admin gate ───────────────────────────────────────────────────────

func TestTasks_ListRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Tasks = v1.NewTasksHandler(&stubTasksQuerier{}, scheduler.NewRegistry(), slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/tasks", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── Invite — admin gate ──────────────────────────────────────────────────────

func TestInvite_ListRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Invite = v1.NewInviteHandler(&stubInviteDB{}, email.NewSender(nil), "http://localhost", slog.Default())
	})

	resp := ts.do("GET", "/api/v1/invites", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── OIDC / SAML / LDAP enabled flags ─────────────────────────────────────────
// These are public probes the login UI uses to render the right buttons.
// They must NEVER 500 even when the relevant config isn't persisted.

type stubOIDCSettings struct{}

func (stubOIDCSettings) OIDC(_ context.Context) settings.OIDCConfig {
	return settings.OIDCConfig{}
}

type stubSAMLSettings struct{}

func (stubSAMLSettings) SAML(_ context.Context) settings.SAMLConfig {
	return settings.SAMLConfig{}
}

type stubLDAPSettings struct{}

func (stubLDAPSettings) LDAP(_ context.Context) settings.LDAPConfig {
	return settings.LDAPConfig{}
}

type stubOIDCSvc struct{}

func (stubOIDCSvc) LoginOrCreateOIDCUser(_ context.Context, _ v1.OIDCProfile) (*v1.TokenPair, error) {
	return nil, errors.New("not used in enabled-flag test")
}

type stubSAMLSvc struct{}

func (stubSAMLSvc) LoginOrCreateSAMLUser(_ context.Context, _ v1.SAMLProfile) (*v1.TokenPair, error) {
	return nil, errors.New("not used in enabled-flag test")
}

type stubLDAPSvc struct{}

func (stubLDAPSvc) LoginLDAP(_ context.Context, _, _ string) (*v1.TokenPair, error) {
	return nil, errors.New("not used in enabled-flag test")
}

func TestOIDCEnabled_PublicWithoutConfig(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.OIDCAuth = v1.NewOIDCHandler(stubOIDCSettings{}, stubOIDCSvc{}, "http://localhost", slog.Default())
	})

	resp := ts.do("GET", "/api/v1/auth/oidc/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data["enabled"] != false {
		t.Errorf("got %v, want enabled=false (no OIDC config persisted)", env.Data)
	}
}

func TestSAMLEnabled_PublicWithoutConfig(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.SAMLAuth = v1.NewSAMLHandler(stubSAMLSettings{}, stubSAMLSvc{}, "http://localhost", slog.Default())
	})

	resp := ts.do("GET", "/api/v1/auth/saml/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data["enabled"] != false {
		t.Errorf("got %v, want enabled=false", env.Data)
	}
}

func TestLDAPEnabled_PublicWithoutConfig(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LDAPAuth = v1.NewLDAPHandler(stubLDAPSettings{}, stubLDAPSvc{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/auth/ldap/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data["enabled"] != false {
		t.Errorf("got %v, want enabled=false", env.Data)
	}
}

// ── /artwork/* ACL — security regression guard ───────────────────────────────
// Documents that the previously-public artwork endpoint now requires auth,
// matching the trickplay fix.

func TestArtwork_RequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		// ArtworkRoots non-nil so the route registers; the test only
		// confirms the auth gate fires before the handler runs.
		h.ArtworkRoots = func() []api.ArtworkRoot { return nil }
	})

	resp := ts.do("GET", "/artwork/some/path/poster.jpg", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — artwork must require auth", resp.StatusCode)
	}
}

// ── chi context propagation guard ────────────────────────────────────────────
// Drift sentinel: ensures the chi route context propagates through every
// middleware in the stack so URLParam("id") works inside handlers. This
// caught a real regression once when a custom middleware wrapped the
// request without preserving the chi.RouteCtxKey value.

func TestChiContext_PropagatesThroughMiddleware(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Lyrics = v1.NewLyricsHandler(
			&stubLyricsStore{},
			&stubLyricsItems{item: &media.Item{
				ID: uuid.New(), LibraryID: uuid.New(), Type: "track", Title: "X",
			}},
			stubLyricsFetcher{}, slog.Default())
	})

	// The handler internally calls chi.URLParam(r, "id"); if the route
	// context isn't on the request anymore the parse fails with empty
	// string → 400. Use a real UUID so the parse succeeds.
	resp := ts.do("GET", "/api/v1/items/"+uuid.New().String()+"/lyrics", ts.userToken(), nil)
	if resp.StatusCode == http.StatusBadRequest {
		t.Errorf("400 from URL parse — chi route context lost in middleware chain")
	}
}

// silence unused-import warnings for packages used only in stub bodies above
var _ = chi.URLParam
var _ = pgtype.Bool{}

// ── Plugins ──────────────────────────────────────────────────────────────────
// plugin.Registry takes an unexported registryDB interface, so we satisfy it
// structurally with a stub that implements every method.

type stubPluginRegistryDB struct{}

func (s *stubPluginRegistryDB) CreatePlugin(_ context.Context, _ gen.CreatePluginParams) (gen.Plugin, error) {
	return gen.Plugin{}, nil
}
func (s *stubPluginRegistryDB) GetPlugin(_ context.Context, _ uuid.UUID) (gen.Plugin, error) {
	return gen.Plugin{}, errors.New("not found")
}
func (s *stubPluginRegistryDB) ListPlugins(_ context.Context) ([]gen.Plugin, error) {
	return nil, nil
}
func (s *stubPluginRegistryDB) ListEnabledPluginsByRole(_ context.Context, _ string) ([]gen.Plugin, error) {
	return nil, nil
}
func (s *stubPluginRegistryDB) UpdatePlugin(_ context.Context, _ gen.UpdatePluginParams) (gen.Plugin, error) {
	return gen.Plugin{}, nil
}
func (s *stubPluginRegistryDB) DeletePlugin(_ context.Context, _ uuid.UUID) error { return nil }

func TestPlugins_ListRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		reg := plugin.NewRegistry(&stubPluginRegistryDB{})
		h.Plugins = v1.NewPluginHandler(reg, nil, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/plugins", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestPlugins_ListAcceptsAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		reg := plugin.NewRegistry(&stubPluginRegistryDB{})
		h.Plugins = v1.NewPluginHandler(reg, nil, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/plugins", ts.adminToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

// ── LiveTV ───────────────────────────────────────────────────────────────────

// stubLiveTVService satisfies v1.LiveTVService with empty results. Sufficient
// for routing + auth gate tests; functional tests would override fields.
type stubLiveTVService struct{}

func (s *stubLiveTVService) ListTuners(_ context.Context) ([]livetv.TunerDevice, error) {
	return nil, nil
}
func (s *stubLiveTVService) GetTuner(_ context.Context, _ uuid.UUID) (livetv.TunerDevice, error) {
	return livetv.TunerDevice{}, errors.New("not found")
}
func (s *stubLiveTVService) CreateTuner(_ context.Context, _ livetv.CreateTunerDeviceParams) (livetv.TunerDevice, error) {
	return livetv.TunerDevice{}, nil
}
func (s *stubLiveTVService) UpdateTuner(_ context.Context, _ livetv.UpdateTunerDeviceParams) (livetv.TunerDevice, error) {
	return livetv.TunerDevice{}, nil
}
func (s *stubLiveTVService) SetTunerEnabled(_ context.Context, _ uuid.UUID, _ bool) error {
	return nil
}
func (s *stubLiveTVService) DeleteTuner(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubLiveTVService) RescanTuner(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (s *stubLiveTVService) DiscoverHDHomeRuns(_ context.Context) ([]livetv.DiscoveredDevice, error) {
	return nil, nil
}
func (s *stubLiveTVService) ListChannels(_ context.Context, _ bool) ([]livetv.ChannelWithTuner, error) {
	return nil, nil
}
func (s *stubLiveTVService) GetChannel(_ context.Context, _ uuid.UUID) (livetv.Channel, error) {
	return livetv.Channel{}, errors.New("not found")
}
func (s *stubLiveTVService) SetChannelEnabled(_ context.Context, _ uuid.UUID, _ bool) error {
	return nil
}
func (s *stubLiveTVService) NowAndNext(_ context.Context) ([]livetv.NowNextEntry, error) {
	return nil, nil
}
func (s *stubLiveTVService) Guide(_ context.Context, _, _ time.Time) ([]livetv.EPGProgram, error) {
	return nil, nil
}
func (s *stubLiveTVService) ListEPGSources(_ context.Context) ([]livetv.EPGSource, error) {
	return nil, nil
}
func (s *stubLiveTVService) CreateEPGSource(_ context.Context, _ livetv.CreateEPGSourceParams) (livetv.EPGSource, error) {
	return livetv.EPGSource{}, nil
}
func (s *stubLiveTVService) DeleteEPGSource(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubLiveTVService) SetEPGSourceEnabled(_ context.Context, _ uuid.UUID, _ bool) error {
	return nil
}
func (s *stubLiveTVService) RefreshEPGSource(_ context.Context, _ uuid.UUID) (livetv.RefreshResult, error) {
	return livetv.RefreshResult{}, nil
}
func (s *stubLiveTVService) SetChannelEPGID(_ context.Context, _ uuid.UUID, _ *string) error {
	return nil
}
func (s *stubLiveTVService) ListKnownEPGIDs(_ context.Context) ([]string, error) { return nil, nil }
func (s *stubLiveTVService) ListUnmappedChannels(_ context.Context) ([]livetv.Channel, error) {
	return nil, nil
}
func (s *stubLiveTVService) ReorderChannels(_ context.Context, _ []uuid.UUID) error { return nil }

func TestLiveTV_ListChannelsRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/tv/channels", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestLiveTV_ListChannelsAcceptsUser(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/tv/channels", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

func TestLiveTV_ListTunersRequiresAdmin(t *testing.T) {
	// Tuner CRUD is admin-only — exposes per-tuner network configuration
	// (HDHomeRun host, M3U URL) that a non-admin shouldn't see.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/tv/tuners", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestLiveTV_NowAndNextAcceptsUser(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/tv/channels/now-next", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

func TestLiveTV_GuideAcceptsUser(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/tv/guide?from=2026-04-26T00:00:00Z&to=2026-04-27T00:00:00Z", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

func TestLiveTV_DiscoverTunersRequiresAdmin(t *testing.T) {
	// The discover endpoint broadcasts on the LAN; only admin should
	// be able to trigger that and read the discovered hostnames.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LiveTV = v1.NewLiveTVHandler(&stubLiveTVService{}, slog.Default())
	})

	resp := ts.do("POST", "/api/v1/tv/tuners/discover", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── Discover (TMDB-backed) ───────────────────────────────────────────────────

type stubDiscoverDB struct{}

func (s *stubDiscoverDB) ListMediaItemsByTMDBIDs(_ context.Context, _ gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}

type stubDiscoverTMDB struct {
	results []tmdb.DiscoverResult
}

func (s *stubDiscoverTMDB) SearchMulti(_ context.Context, _ string, _ int) ([]tmdb.DiscoverResult, error) {
	return s.results, nil
}

type stubDiscoverRequests struct{}

func (s *stubDiscoverRequests) FindActiveForUser(_ context.Context, _ uuid.UUID, _ string, _ int) (*gen.MediaRequest, error) {
	return nil, nil
}

func TestDiscover_RequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Discover = v1.NewDiscoverHandler(
			&stubDiscoverDB{},
			&stubDiscoverTMDB{},
			&stubDiscoverRequests{},
			slog.Default(),
		)
	})

	resp := ts.do("GET", "/api/v1/discover/search?q=matrix", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDiscover_ReturnsResults(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Discover = v1.NewDiscoverHandler(
			&stubDiscoverDB{},
			&stubDiscoverTMDB{results: []tmdb.DiscoverResult{
				{MediaType: "movie", TMDBID: 603, Title: "The Matrix", Year: 1999, Rating: 8.2},
			}},
			&stubDiscoverRequests{},
			slog.Default(),
		)
	})

	resp := ts.do("GET", "/api/v1/discover/search?q=matrix", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, readBody(resp))
	}
}

func TestDiscover_TMDBNotConfiguredReturns503(t *testing.T) {
	// nil tmdb client — endpoint advertises this state via 503 + clear
	// error code so the UI can render the "configure TMDB" prompt.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Discover = v1.NewDiscoverHandler(
			&stubDiscoverDB{}, nil, &stubDiscoverRequests{}, slog.Default(),
		)
	})

	resp := ts.do("GET", "/api/v1/discover/search?q=anything", ts.userToken(), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (TMDB not configured)", resp.StatusCode)
	}
}

// ── Requests ─────────────────────────────────────────────────────────────────

// stubRequestsDB satisfies the unexported requests.DB interface
// structurally. Only the methods the gate-test exercises do real work;
// the rest return zero values / not-found errors.
type stubRequestsDB struct{}

func (s *stubRequestsDB) GetArrService(_ context.Context, _ uuid.UUID) (gen.ArrService, error) {
	return gen.ArrService{}, errors.New("not found")
}
func (s *stubRequestsDB) GetDefaultArrServiceByKind(_ context.Context, _ string) (gen.ArrService, error) {
	return gen.ArrService{}, errors.New("not found")
}
func (s *stubRequestsDB) CreateMediaRequest(_ context.Context, _ gen.CreateMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, nil
}
func (s *stubRequestsDB) GetMediaRequest(_ context.Context, _ uuid.UUID) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, errors.New("not found")
}
func (s *stubRequestsDB) FindActiveRequestForUser(_ context.Context, _ gen.FindActiveRequestForUserParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, errors.New("not found")
}
func (s *stubRequestsDB) ListMediaRequestsForUser(_ context.Context, _ gen.ListMediaRequestsForUserParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (s *stubRequestsDB) CountMediaRequestsForUser(_ context.Context, _ gen.CountMediaRequestsForUserParams) (int64, error) {
	return 0, nil
}
func (s *stubRequestsDB) ListAllMediaRequests(_ context.Context, _ gen.ListAllMediaRequestsParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (s *stubRequestsDB) CountAllMediaRequests(_ context.Context, _ *string) (int64, error) {
	return 0, nil
}
func (s *stubRequestsDB) ListActiveMediaRequestsForTMDB(_ context.Context, _ gen.ListActiveMediaRequestsForTMDBParams) ([]gen.MediaRequest, error) {
	return nil, nil
}
func (s *stubRequestsDB) ListMediaItemsByTMDBIDs(_ context.Context, _ gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}
func (s *stubRequestsDB) ApproveMediaRequest(_ context.Context, _ gen.ApproveMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, nil
}
func (s *stubRequestsDB) DeclineMediaRequest(_ context.Context, _ gen.DeclineMediaRequestParams) (gen.MediaRequest, error) {
	return gen.MediaRequest{}, nil
}
func (s *stubRequestsDB) MarkMediaRequestDownloading(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (s *stubRequestsDB) MarkMediaRequestAvailable(_ context.Context, _ gen.MarkMediaRequestAvailableParams) error {
	return nil
}
func (s *stubRequestsDB) MarkMediaRequestFailed(_ context.Context, _ uuid.UUID) error { return nil }
func (s *stubRequestsDB) CancelMediaRequest(_ context.Context, _ gen.CancelMediaRequestParams) error {
	return nil
}
func (s *stubRequestsDB) DeleteMediaRequest(_ context.Context, _ uuid.UUID) error { return nil }

func TestRequests_ListRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		svc := requests.NewService(&stubRequestsDB{}, nil, nil, slog.Default())
		h.Requests = v1.NewRequestHandler(svc, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/requests", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRequests_ListAcceptsUser(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		svc := requests.NewService(&stubRequestsDB{}, nil, nil, slog.Default())
		h.Requests = v1.NewRequestHandler(svc, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/requests", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body=%s", resp.StatusCode, readBody(resp))
	}
}

// ── Audit (admin-only) ───────────────────────────────────────────────────────

func TestAudit_ListRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Audit = v1.NewAuditHandler(&stubAuditDB{}, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/audit", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── Maintenance (admin-only) ─────────────────────────────────────────────────

type stubMaintenanceMedia struct{}

func (s *stubMaintenanceMedia) ListItemsMissingArt(_ context.Context, _ int32) ([]media.Item, error) {
	return nil, nil
}
func (s *stubMaintenanceMedia) DedupeTopLevelItems(_ context.Context, _ string, _ *uuid.UUID) (media.DedupeResult, error) {
	return media.DedupeResult{}, nil
}

func TestMaintenance_RefreshMissingArtRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Maintenance = v1.NewMaintenanceHandler(&stubMaintenanceMedia{}, &stubItemEnricher{}, slog.Default())
	})

	resp := ts.do("POST", "/api/v1/maintenance/refresh-missing-art", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── Backup (admin-only) ──────────────────────────────────────────────────────

func TestBackup_DownloadRequiresAdmin(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		// pg_dump may not be on PATH in CI — that's fine; the admin gate
		// fires before pg_dump is invoked.
		h.Backup = v1.NewBackupHandler("postgres://test", 1, nil, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/admin/backup", ts.userToken(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ── Notification SSE stream ──────────────────────────────────────────────────

func TestNotifications_StreamRequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		brk := notification.NewBroker()
		h.Notifications = v1.NewNotificationHandler(&stubNotificationDB{}, brk, slog.Default())
	})

	resp := ts.do("GET", "/api/v1/notifications/stream", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (SSE stream must require auth)", resp.StatusCode)
	}
}

// TestNotifications_StreamSetsSSEHeaders intentionally omitted.
//
// SSE testing through httptest's HTTP/1.1 transport is notoriously
// flaky — the chunked-stream response buffers headers until the first
// flush makes it across the wire, which races against the client's
// timeout no matter how it's tuned. The auth gate is exercised by
// TestNotifications_StreamRequiresAuth above; the SSE-headers shape
// is verified by the notification package's own unit tests where the
// handler is called directly with a httptest.Recorder.

// ── OIDC / SAML callback paths (negative cases — no real IdP needed) ──────────
// We can't simulate a full IdP in UAT, but the callback handlers have
// guard branches that fire BEFORE the IdP exchange and are worth locking
// down: missing state cookie, missing code, parse failures.

func TestOIDC_CallbackWithoutStateCookieIs400(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		// Stub OIDC config has Enabled=false → /callback fails its
		// resolve() step and 302s back to /login?error=oidc_disabled.
		// We assert the redirect (3xx), not the final 200 you'd get
		// after following it.
		h.OIDCAuth = v1.NewOIDCHandler(stubOIDCSettings{}, stubOIDCSvc{}, "http://localhost", slog.Default())
	})

	// Don't follow redirects — we want to see the immediate 307/302 from
	// the callback handler, not the rendered /login page after the chase.
	noFollow := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest("GET", ts.url("/api/v1/auth/oidc/callback?code=x&state=y"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Errorf("status = %d — callback should redirect (3xx) when OIDC isn't configured", resp.StatusCode)
	}
}

func TestSAML_ACSPostRejectedWhenSAMLDisabled(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.SAMLAuth = v1.NewSAMLHandler(stubSAMLSettings{}, stubSAMLSvc{}, "http://localhost", slog.Default())
	})

	resp := ts.do("POST", "/api/v1/auth/saml/acs", "", map[string]any{})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("status = %d — ACS should reject when SAML isn't configured", resp.StatusCode)
	}
}

func TestSAML_MetadataIsPublicEvenWhenDisabled(t *testing.T) {
	// /api/v1/auth/saml/metadata should still respond — returning either
	// 503 (we use this) or the metadata XML. Either way it's NOT 401:
	// this endpoint is used by IdP admins during initial registration
	// and they don't have OnScreen credentials yet.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.SAMLAuth = v1.NewSAMLHandler(stubSAMLSettings{}, stubSAMLSvc{}, "http://localhost", slog.Default())
	})

	resp := ts.do("GET", "/api/v1/auth/saml/metadata", "", nil)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("status = %d — SAML metadata must NOT require auth", resp.StatusCode)
	}
}

func TestLDAP_LoginEndpointPublic(t *testing.T) {
	// LDAP login is public (it IS the login). Test that the route is
	// registered and responds (the actual auth result depends on the
	// stubbed service; we just confirm the route isn't 401-gated).
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.LDAPAuth = v1.NewLDAPHandler(stubLDAPSettings{}, stubLDAPSvc{}, slog.Default())
	})

	resp := ts.do("POST", "/api/v1/auth/ldap/login", "", map[string]any{
		"username": "alice", "password": "p",
	})
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("status = %d — LDAP login route must be public", resp.StatusCode)
	}
}

// ── unused-import sentinels for new dependencies ─────────────────────────────
var _ = plugin.NewRegistry
var _ = livetv.TunerDevice{}
var _ = tmdb.DiscoverResult{}
var _ = requests.NewService
