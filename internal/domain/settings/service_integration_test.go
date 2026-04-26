package settings

import (
	"context"
	"log/slog"
	"reflect"
	"testing"

	"github.com/onscreen/onscreen/internal/testdb"
)

// newServiceForTest spins up a Postgres container, runs migrations, and
// returns a Service ready for round-trip tests. Each call gets its own
// container — container startup is the dominant cost so tests in this
// file group their assertions under t.Run subtests to amortize it.
func newServiceForTest(t *testing.T) *Service {
	t.Helper()
	pool := testdb.New(t)
	return New(pool, slog.Default())
}

// TestService_AllConfigsRoundTrip is the workhorse: every typed config
// gets persisted and read back, asserting equality. A drift between the
// JSON tags on the struct and the Set/Get pair would fail here.
//
// Grouped under one TestMain-style parent so the testcontainer starts
// once for ~15 subtests rather than 15 times.
func TestService_AllConfigsRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainer)")
	}
	svc := newServiceForTest(t)
	ctx := context.Background()

	t.Run("TMDBAPIKey", func(t *testing.T) {
		if got := svc.TMDBAPIKey(ctx); got != "" {
			t.Errorf("initial: got %q, want empty", got)
		}
		if err := svc.SetTMDBAPIKey(ctx, "tmdb-key-xyz"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := svc.TMDBAPIKey(ctx); got != "tmdb-key-xyz" {
			t.Errorf("after set: got %q, want tmdb-key-xyz", got)
		}
		// Empty clears.
		if err := svc.SetTMDBAPIKey(ctx, ""); err != nil {
			t.Fatalf("clear: %v", err)
		}
		if got := svc.TMDBAPIKey(ctx); got != "" {
			t.Errorf("after clear: got %q, want empty", got)
		}
	})

	t.Run("TVDBAPIKey", func(t *testing.T) {
		if err := svc.SetTVDBAPIKey(ctx, "tvdb-pid"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := svc.TVDBAPIKey(ctx); got != "tvdb-pid" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ArrAPIKey", func(t *testing.T) {
		if err := svc.SetArrAPIKey(ctx, "arr-secret"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := svc.ArrAPIKey(ctx); got != "arr-secret" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("ArrPathMappings", func(t *testing.T) {
		if got := svc.ArrPathMappings(ctx); got != nil {
			t.Errorf("initial: got %v, want nil", got)
		}
		want := map[string]string{"/Media/TV Shows": "C:\\TV", "/Media/Movies": "C:\\Movies"}
		if err := svc.SetArrPathMappings(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.ArrPathMappings(ctx)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("TranscodeEncoders", func(t *testing.T) {
		if err := svc.SetTranscodeEncoders(ctx, "nvenc,software"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if got := svc.TranscodeEncoders(ctx); got != "nvenc,software" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("TranscodeConfig", func(t *testing.T) {
		// Zero value when unset.
		if got := svc.TranscodeConfigGet(ctx); got != (TranscodeConfig{}) {
			t.Errorf("initial: got %+v, want zero value", got)
		}
		want := TranscodeConfig{NVENCPreset: "p7", NVENCTune: "hq", NVENCRC: "vbr", MaxrateRatio: 2.0}
		if err := svc.SetTranscodeConfig(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.TranscodeConfigGet(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("IntroDetectionMode_DefaultsToOnScan", func(t *testing.T) {
		// No row → default to OnScan (matches migration seed).
		if got := svc.IntroDetectionMode(ctx); got != IntroDetectionOnScan {
			t.Errorf("default: got %q, want %q", got, IntroDetectionOnScan)
		}
	})

	t.Run("IntroDetectionMode_RoundTrip", func(t *testing.T) {
		for _, mode := range []IntroDetectionMode{IntroDetectionOff, IntroDetectionOnScan, IntroDetectionManual} {
			if err := svc.SetIntroDetectionMode(ctx, mode); err != nil {
				t.Errorf("Set %q: %v", mode, err)
			}
			if got := svc.IntroDetectionMode(ctx); got != mode {
				t.Errorf("after set %q: got %q", mode, got)
			}
		}
	})

	t.Run("IntroDetectionMode_RejectsInvalid", func(t *testing.T) {
		err := svc.SetIntroDetectionMode(ctx, IntroDetectionMode("bogus"))
		if err == nil {
			t.Fatal("expected ErrInvalidSetting for unknown mode")
		}
		if err != ErrInvalidSetting {
			t.Errorf("got %v, want ErrInvalidSetting", err)
		}
	})

	t.Run("OpenSubtitles", func(t *testing.T) {
		want := OpenSubtitlesConfig{
			APIKey: "os-key", Username: "alice", Password: "p",
			Languages: "en,es", Enabled: true,
		}
		if err := svc.SetOpenSubtitles(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.OpenSubtitles(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("OIDC", func(t *testing.T) {
		want := OIDCConfig{
			Enabled: true, DisplayName: "Authentik",
			IssuerURL: "https://idp.example.com", ClientID: "abc",
			ClientSecret: "secret-xyz", Scopes: "openid profile",
			UsernameClaim: "preferred_username", AdminGroup: "admins",
		}
		if err := svc.SetOIDC(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.OIDC(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("SAML", func(t *testing.T) {
		want := SAMLConfig{
			Enabled: true, DisplayName: "Okta",
			IdPMetadataURL: "https://idp.example.com/metadata",
			EntityID:       "https://onscreen.example.com",
			AdminGroup:     "onscreen-admins",
		}
		if err := svc.SetSAML(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.SAML(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("LDAP", func(t *testing.T) {
		want := LDAPConfig{
			Enabled: true, DisplayName: "Corp AD",
			Host: "ldap.corp.local:636", UseLDAPS: true,
			BindDN: "cn=svc,dc=corp,dc=local", BindPassword: "svcpw",
			UserSearchBase: "ou=people,dc=corp,dc=local",
			UserFilter:     "(sAMAccountName={username})",
		}
		if err := svc.SetLDAP(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.LDAP(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("SMTP", func(t *testing.T) {
		want := SMTPConfig{
			Enabled: true, Host: "smtp.example.com", Port: 587,
			Username: "noreply", Password: "p", From: "OnScreen <noreply@example.com>",
		}
		if err := svc.SetSMTP(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.SMTP(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("OTel", func(t *testing.T) {
		want := OTelConfig{
			Enabled: true, Endpoint: "https://otel.example.com:4317",
			SampleRatio: 0.1, DeploymentEnv: "production",
		}
		if err := svc.SetOTel(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.OTel(ctx)
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("General", func(t *testing.T) {
		want := GeneralConfig{
			BaseURL:            "https://media.example.com",
			LogLevel:           "debug",
			CORSAllowedOrigins: []string{"https://tv.example.com", "https://app.example.com"},
		}
		if err := svc.SetGeneral(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.General(ctx)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("WorkerFleet", func(t *testing.T) {
		// The detailed JSON shape is exercised in fleet_test.go; here we
		// only confirm the round-trip persists into the DB and returns the
		// same struct on read.
		want := WorkerFleetConfig{
			EmbeddedEnabled: true,
			EmbeddedEncoder: "h264_nvenc",
			Workers: []WorkerSlotConfig{
				{Addr: "http://worker1:7073", Name: "worker-1", Encoder: "nvenc", MaxSessions: 4},
			},
		}
		if err := svc.SetWorkerFleet(ctx, want); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got := svc.WorkerFleet(ctx)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("Update_OverwritesExistingValue", func(t *testing.T) {
		// The set() helper uses INSERT ... ON CONFLICT DO UPDATE; this
		// asserts the second write actually replaces the first rather
		// than failing or appending.
		_ = svc.SetTMDBAPIKey(ctx, "first")
		_ = svc.SetTMDBAPIKey(ctx, "second")
		if got := svc.TMDBAPIKey(ctx); got != "second" {
			t.Errorf("got %q, want \"second\" — UPSERT broken?", got)
		}
	})
}

// TestService_GetUnsetReturnsZeroValues exercises the "no row in
// server_settings" branch for every typed config, ensuring readers
// degrade to the documented zero value rather than 500ing.
func TestService_GetUnsetReturnsZeroValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainer)")
	}
	svc := newServiceForTest(t)
	ctx := context.Background()

	if got := svc.OpenSubtitles(ctx); got != (OpenSubtitlesConfig{}) {
		t.Errorf("OpenSubtitles unset: got %+v, want zero", got)
	}
	if got := svc.OIDC(ctx); got != (OIDCConfig{}) {
		t.Errorf("OIDC unset: got %+v", got)
	}
	if got := svc.SAML(ctx); got != (SAMLConfig{}) {
		t.Errorf("SAML unset: got %+v", got)
	}
	if got := svc.LDAP(ctx); got != (LDAPConfig{}) {
		t.Errorf("LDAP unset: got %+v", got)
	}
	if got := svc.SMTP(ctx); got != (SMTPConfig{}) {
		t.Errorf("SMTP unset: got %+v", got)
	}
	if got := svc.OTel(ctx); got != (OTelConfig{}) {
		t.Errorf("OTel unset: got %+v", got)
	}
	if got := svc.General(ctx); !reflect.DeepEqual(got, GeneralConfig{}) {
		t.Errorf("General unset: got %+v", got)
	}
	if got := svc.TranscodeConfigGet(ctx); got != (TranscodeConfig{}) {
		t.Errorf("TranscodeConfig unset: got %+v", got)
	}
}
