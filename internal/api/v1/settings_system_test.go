package v1

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

func sysIntPtr(i int) *int    { return &i }
func sysBoolPtr(b bool) *bool { return &b }

func sysHandler(svc *mockSettingsService, defaults settings.SystemConfig) *SettingsHandler {
	return NewSettingsHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).SetSystemDefaults(defaults)
}

func getSystemDTO(t *testing.T, h *SettingsHandler) systemSettingDTO {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetSystem(rec, httptest.NewRequest(http.MethodGet, "/settings/system", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetSystem status = %d, want 200", rec.Code)
	}
	// GetSystem uses respond.Success → {"data": …}, matching api.get's unwrap.
	var env struct {
		Data systemSettingDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestGetSystem_FallsBackToEnvDefaults(t *testing.T) {
	// Nothing stored → the env-effective defaults are returned, so the page shows
	// the running config rather than blanks.
	h := sysHandler(&mockSettingsService{}, settings.SystemConfig{
		ServerName:              strPtr("Env Name"),
		RetainMonths:            sysIntPtr(12),
		TranscodeABR:            sysBoolPtr(true),
		TMDBRateLimit:           sysIntPtr(5),
		MissingFileGraceMinutes: sysIntPtr(30),
		ScanFileConcurrency:     sysIntPtr(8),
		ScanLibraryConcurrency:  sysIntPtr(3),
		DiscoveryEnabled:        sysBoolPtr(true),
		DiscoveryPort:           sysIntPtr(7368),
	})
	dto := getSystemDTO(t, h)
	if dto.ServerName != "Env Name" || dto.RetainMonths != 12 || !dto.TranscodeABR || dto.TMDBRateLimit != 5 {
		t.Errorf("defaults not surfaced: %+v", dto)
	}
	if dto.MissingFileGraceMinutes != 30 || dto.ScanFileConcurrency != 8 || dto.ScanLibraryConcurrency != 3 {
		t.Errorf("scanner defaults not surfaced: %+v", dto)
	}
	if !dto.DiscoveryEnabled || dto.DiscoveryPort != 7368 {
		t.Errorf("discovery defaults not surfaced: %+v", dto)
	}
}

func TestGetSystem_StoredOverridesDefault(t *testing.T) {
	h := sysHandler(
		&mockSettingsService{system: settings.SystemConfig{ServerName: strPtr("DB Name"), TranscodeABR: sysBoolPtr(false)}},
		settings.SystemConfig{ServerName: strPtr("Env Name"), TranscodeABR: sysBoolPtr(true)},
	)
	dto := getSystemDTO(t, h)
	if dto.ServerName != "DB Name" {
		t.Errorf("stored ServerName should win: %q", dto.ServerName)
	}
	if dto.TranscodeABR {
		t.Error("stored TranscodeABR=false should win over env true")
	}
}

func TestGetTranscodeConfig_FillsUnsetCapsFromDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Nothing stored (all caps 0) → the effective running ceilings are returned.
	h := NewSettingsHandler(&mockSettingsService{}, logger).
		SetTranscodeDefaults(settings.TranscodeConfig{MaxBitrateKbps: 40000, MaxWidth: 3840, MaxHeight: 2160})
	rec := httptest.NewRecorder()
	h.GetTranscodeConfig(rec, httptest.NewRequest(http.MethodGet, "/settings/transcode-config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeTranscodeConfig(t, rec)
	if got.MaxBitrateKbps != 40000 || got.MaxWidth != 3840 || got.MaxHeight != 2160 {
		t.Errorf("unset caps not filled from defaults: %+v", got)
	}

	// A stored cap wins over the default.
	h2 := NewSettingsHandler(&mockSettingsService{transcodeCfg: settings.TranscodeConfig{MaxHeight: 1080}}, logger).
		SetTranscodeDefaults(settings.TranscodeConfig{MaxBitrateKbps: 40000, MaxWidth: 3840, MaxHeight: 2160})
	rec2 := httptest.NewRecorder()
	h2.GetTranscodeConfig(rec2, httptest.NewRequest(http.MethodGet, "/settings/transcode-config", nil))
	got2 := decodeTranscodeConfig(t, rec2)
	if got2.MaxHeight != 1080 {
		t.Errorf("stored MaxHeight should win: got %d", got2.MaxHeight)
	}
	if got2.MaxWidth != 3840 { // still filled from default
		t.Errorf("unset MaxWidth should fall back to default: got %d", got2.MaxWidth)
	}
}

// decodeTranscodeConfig unwraps the {"data": …} envelope respond.Success emits.
func decodeTranscodeConfig(t *testing.T, rec *httptest.ResponseRecorder) settings.TranscodeConfig {
	t.Helper()
	var env struct {
		Data settings.TranscodeConfig `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestUpdateSystem_StoresAllFieldsAsOverrides(t *testing.T) {
	svc := &mockSettingsService{}
	h := sysHandler(svc, settings.SystemConfig{})
	body := `{"server_name":"New","retain_months":36,"transcode_abr":true,"public_asset_cache":true,"static_abr_enabled":true,"missing_file_grace_minutes":45,"scan_file_concurrency":16,"scan_library_concurrency":4,"discovery_enabled":false,"discovery_port":9999}`
	rec := httptest.NewRecorder()
	h.UpdateSystem(rec, httptest.NewRequest(http.MethodPut, "/settings/system", strings.NewReader(body)))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	got := svc.system
	if got.ServerName == nil || *got.ServerName != "New" {
		t.Errorf("ServerName not stored: %+v", got.ServerName)
	}
	if got.RetainMonths == nil || *got.RetainMonths != 36 {
		t.Errorf("RetainMonths not stored: %+v", got.RetainMonths)
	}
	if got.TranscodeABR == nil || !*got.TranscodeABR {
		t.Error("TranscodeABR not stored")
	}
	if got.StaticABREnabled == nil || !*got.StaticABREnabled {
		t.Error("StaticABREnabled not stored")
	}
	if got.MissingFileGraceMinutes == nil || *got.MissingFileGraceMinutes != 45 {
		t.Errorf("MissingFileGraceMinutes not stored: %+v", got.MissingFileGraceMinutes)
	}
	if got.ScanFileConcurrency == nil || *got.ScanFileConcurrency != 16 {
		t.Errorf("ScanFileConcurrency not stored: %+v", got.ScanFileConcurrency)
	}
	if got.ScanLibraryConcurrency == nil || *got.ScanLibraryConcurrency != 4 {
		t.Errorf("ScanLibraryConcurrency not stored: %+v", got.ScanLibraryConcurrency)
	}
	if got.DiscoveryEnabled == nil || *got.DiscoveryEnabled {
		t.Errorf("DiscoveryEnabled=false not stored: %+v", got.DiscoveryEnabled)
	}
	if got.DiscoveryPort == nil || *got.DiscoveryPort != 9999 {
		t.Errorf("DiscoveryPort not stored: %+v", got.DiscoveryPort)
	}
}
