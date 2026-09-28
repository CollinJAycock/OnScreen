package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// The mask round-trip defect: GET returns "****" in place of every stored
// secret, and the admin UI binds that string to a text input and sends it
// straight back on Save. Any PATCH branch that writes its value unconditionally
// therefore REPLACES the real secret with four asterisks.
//
// For the arr key that was remotely exploitable: POST /api/v1/arr/webhook
// authenticates by constant-time compare against the stored key and is mounted
// outside every auth group, so once the stored value was "****" any
// unauthenticated caller could send `X-Api-Key: ****` and be accepted. The
// precondition was weaker than "the operator uses Radarr" — the server
// auto-generates and persists an arr key on the first settings load.
//
// Every secret on this handler must ignore the sentinel. This test enumerates
// them so a new one cannot be added without the guard.
type maskSpySettings struct {
	SettingsServiceIface
	tmdb, tvdb, arr string
	opensubsPw      string
	oidcSecret      string
	ldapBindPw      string
	smtpPw          string
	lastfm          *settings.LastFMConfig
	trakt           *settings.TraktConfig
}

func (m *maskSpySettings) TMDBAPIKey(context.Context) string { return "real-tmdb" }
func (m *maskSpySettings) TVDBAPIKey(context.Context) string { return "real-tvdb" }
func (m *maskSpySettings) ArrAPIKey(context.Context) string  { return "real-arr" }

func (m *maskSpySettings) SetTMDBAPIKey(_ context.Context, v string) error { m.tmdb = v; return nil }
func (m *maskSpySettings) SetTVDBAPIKey(_ context.Context, v string) error { m.tvdb = v; return nil }
func (m *maskSpySettings) SetArrAPIKey(_ context.Context, v string) error  { m.arr = v; return nil }

func (m *maskSpySettings) OpenSubtitles(context.Context) settings.OpenSubtitlesConfig {
	return settings.OpenSubtitlesConfig{Password: "real-os-pw"}
}
func (m *maskSpySettings) SetOpenSubtitles(_ context.Context, c settings.OpenSubtitlesConfig) error {
	m.opensubsPw = c.Password
	return nil
}
func (m *maskSpySettings) OIDC(context.Context) settings.OIDCConfig {
	return settings.OIDCConfig{ClientSecret: "real-oidc"}
}
func (m *maskSpySettings) SetOIDC(_ context.Context, c settings.OIDCConfig) error {
	m.oidcSecret = c.ClientSecret
	return nil
}
func (m *maskSpySettings) LDAP(context.Context) settings.LDAPConfig {
	return settings.LDAPConfig{BindPassword: "real-ldap"}
}
func (m *maskSpySettings) SetLDAP(_ context.Context, c settings.LDAPConfig) error {
	m.ldapBindPw = c.BindPassword
	return nil
}
func (m *maskSpySettings) SMTP(context.Context) settings.SMTPConfig {
	return settings.SMTPConfig{Password: "real-smtp"}
}
func (m *maskSpySettings) SetSMTP(_ context.Context, c settings.SMTPConfig) error {
	m.smtpPw = c.Password
	return nil
}

func (m *maskSpySettings) LastFM(context.Context) settings.LastFMConfig {
	return settings.LastFMConfig{APIKey: "real-lfm-key", SharedSecret: "real-lfm-secret"}
}
func (m *maskSpySettings) SetLastFM(_ context.Context, c settings.LastFMConfig) error {
	m.lastfm = &c
	return nil
}
func (m *maskSpySettings) Trakt(context.Context) settings.TraktConfig {
	return settings.TraktConfig{ClientID: "real-trakt-id", ClientSecret: "real-trakt-secret"}
}
func (m *maskSpySettings) SetTrakt(_ context.Context, c settings.TraktConfig) error {
	m.trakt = &c
	return nil
}

func patchSettings(t *testing.T, h *SettingsHandler, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code >= 500 {
		t.Fatalf("PATCH failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsPatch_MaskSentinelDoesNotOverwriteAPIKeys(t *testing.T) {
	spy := &maskSpySettings{}
	h := NewSettingsHandler(spy, slog.Default())

	// Exactly what the admin UI sends when the operator opens Settings and
	// presses Save without touching the key fields.
	patchSettings(t, h, `{"tmdb_api_key":"****","tvdb_api_key":"****","arr_api_key":"****"}`)

	if spy.arr != "" {
		t.Errorf("arr key was written with the mask sentinel (%q) — an unauthenticated caller could then authenticate with `X-Api-Key: ****`", spy.arr)
	}
	if spy.tmdb != "" {
		t.Errorf("tmdb key overwritten with mask: %q", spy.tmdb)
	}
	if spy.tvdb != "" {
		t.Errorf("tvdb key overwritten with mask: %q", spy.tvdb)
	}
}

func TestSettingsPatch_RealValuesStillWrite(t *testing.T) {
	spy := &maskSpySettings{}
	h := NewSettingsHandler(spy, slog.Default())

	patchSettings(t, h, `{"tmdb_api_key":"new-tmdb","tvdb_api_key":"new-tvdb","arr_api_key":"new-arr"}`)

	if spy.tmdb != "new-tmdb" || spy.tvdb != "new-tvdb" || spy.arr != "new-arr" {
		t.Errorf("real values must still be written: tmdb=%q tvdb=%q arr=%q", spy.tmdb, spy.tvdb, spy.arr)
	}
}

func TestSettingsPatch_EmptyStringStillClears(t *testing.T) {
	// "" is a real intent — the operator removing a key — and must not be
	// confused with the mask.
	spy := &maskSpySettings{tmdb: "sentinel"}
	h := NewSettingsHandler(spy, slog.Default())
	patchSettings(t, h, `{"tmdb_api_key":""}`)
	if spy.tmdb != "" {
		t.Errorf("empty string must clear the key, got %q", spy.tmdb)
	}
}

// The scrobble apps' secrets follow the same rule: the round-tripped mask
// keeps the stored secret, while the (unmasked) identifiers still update.
func TestSettingsPatch_ScrobbleAppSecretsSurviveTheMask(t *testing.T) {
	spy := &maskSpySettings{}
	h := NewSettingsHandler(spy, slog.Default())

	patchSettings(t, h, `{"lastfm":{"api_key":" new-lfm-key ","shared_secret":"****"},`+
		`"trakt":{"client_id":"new-trakt-id","client_secret":"****"}}`)

	if spy.lastfm == nil || spy.lastfm.SharedSecret != "real-lfm-secret" || spy.lastfm.APIKey != "new-lfm-key" {
		t.Errorf("last.fm: %+v", spy.lastfm)
	}
	if spy.trakt == nil || spy.trakt.ClientSecret != "real-trakt-secret" || spy.trakt.ClientID != "new-trakt-id" {
		t.Errorf("trakt: %+v", spy.trakt)
	}

	patchSettings(t, h, `{"lastfm":{"shared_secret":"new-secret"},"trakt":{"client_secret":""}}`)
	if spy.lastfm.SharedSecret != "new-secret" || spy.lastfm.APIKey != "real-lfm-key" {
		t.Errorf("a real secret must write, leaving the key: %+v", spy.lastfm)
	}
	if spy.trakt.ClientSecret != "" {
		t.Errorf("an empty secret must clear: %+v", spy.trakt)
	}
}

func TestScrobbleAppDTOs_MaskOnlyTheSecrets(t *testing.T) {
	lfm := toLastFMDTO(settings.LastFMConfig{APIKey: "key", SharedSecret: "secret"})
	trakt := toTraktDTO(settings.TraktConfig{ClientID: "id", ClientSecret: "secret"})
	if lfm.APIKey != "key" || lfm.SharedSecret != maskedSecret || trakt.ClientID != "id" || trakt.ClientSecret != maskedSecret {
		t.Errorf("dtos: %+v %+v", lfm, trakt)
	}
	if toLastFMDTO(settings.LastFMConfig{}).SharedSecret != "" {
		t.Error("an unset secret must read as empty, not the mask")
	}
}
