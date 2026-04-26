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
	"github.com/onscreen/onscreen/internal/lyrics"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
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

	createTokenErr error

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
	return s.createTokenErr
}
func (s *stubPasswordResetDB) GetResetToken(_ context.Context, _ string) (v1.PRToken, error) {
	return s.token, s.tokenErr
}
func (s *stubPasswordResetDB) MarkResetTokenUsed(_ context.Context, _ uuid.UUID) error { return nil }
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

func TestForgotPassword_RequiresSMTPToSubmit(t *testing.T) {
	// With no SMTP configured, POST /forgot-password should refuse rather
	// than appearing to send a (never-arriving) email.
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.PasswordReset = v1.NewPasswordResetHandler(
			&stubPasswordResetDB{}, email.NewSender(nil), "http://localhost", slog.Default())
	})

	resp := ts.do("POST", "/api/v1/auth/forgot-password", "", map[string]any{
		"email": "alice@example.com",
	})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("status = %d, want non-200 when SMTP off", resp.StatusCode)
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
