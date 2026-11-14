package v1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/i18n"
)

// systemSettingDTO is the flat, fully-populated shape the System settings page
// edits. GET fills each field with the stored override or the env-effective
// default; PUT stores all fields as explicit overrides. Restart-required — these
// are read once at startup.
type systemSettingDTO struct {
	ServerName              string `json:"server_name"`
	RetainMonths            int    `json:"retain_months"`
	TMDBRateLimit           int    `json:"tmdb_rate_limit"`
	PublicAssetCache        bool   `json:"public_asset_cache"`
	StaticABREnabled        bool   `json:"static_abr_enabled"`
	MissingFileGraceMinutes int    `json:"missing_file_grace_minutes"`
	ScanFileConcurrency     int    `json:"scan_file_concurrency"`
	ScanLibraryConcurrency  int    `json:"scan_library_concurrency"`
	DiscoveryEnabled        bool   `json:"discovery_enabled"`
	DiscoveryPort           int    `json:"discovery_port"`
	// TimeZone is the household IANA zone the parental watch limits run on,
	// "" = unset (the process zone, TimeZoneDefault). The one field here that
	// applies live. On PUT it is optional: absent keeps the stored zone, so a
	// client that predates the field can't clear it by saving the page.
	TimeZone *string `json:"time_zone"`
	// TimeZoneDefault is the zone used while TimeZone is unset (the TZ env,
	// else the host's; "" when it has no name). Read-only.
	TimeZoneDefault string `json:"time_zone_default"`
}

// pick returns the stored override when set, else the env-effective default.
func pickStr(set *string, def *string) string {
	if set != nil {
		return *set
	}
	if def != nil {
		return *def
	}
	return ""
}
func pickInt(set, def *int) int {
	if set != nil {
		return *set
	}
	if def != nil {
		return *def
	}
	return 0
}
func pickBool(set, def *bool) bool {
	if set != nil {
		return *set
	}
	return def != nil && *def
}

// GetSystem handles GET /settings/system — the effective running values.
// Uses respond.Success (the {"data": …} envelope every settings GET emits) so
// the frontend api.get, which unwraps .data, can read it.
func (h *SettingsHandler) GetSystem(w http.ResponseWriter, r *http.Request) {
	s := h.svc.System(r.Context())
	d := h.systemDefaults
	tz := pickStr(s.TimeZone, nil)
	respond.Success(w, r, systemSettingDTO{
		TimeZone:                &tz,
		TimeZoneDefault:         settings.ProcessTimeZoneName(),
		ServerName:              pickStr(s.ServerName, d.ServerName),
		RetainMonths:            pickInt(s.RetainMonths, d.RetainMonths),
		TMDBRateLimit:           pickInt(s.TMDBRateLimit, d.TMDBRateLimit),
		PublicAssetCache:        pickBool(s.PublicAssetCache, d.PublicAssetCache),
		StaticABREnabled:        pickBool(s.StaticABREnabled, d.StaticABREnabled),
		MissingFileGraceMinutes: pickInt(s.MissingFileGraceMinutes, d.MissingFileGraceMinutes),
		ScanFileConcurrency:     pickInt(s.ScanFileConcurrency, d.ScanFileConcurrency),
		ScanLibraryConcurrency:  pickInt(s.ScanLibraryConcurrency, d.ScanLibraryConcurrency),
		DiscoveryEnabled:        pickBool(s.DiscoveryEnabled, d.DiscoveryEnabled),
		DiscoveryPort:           pickInt(s.DiscoveryPort, d.DiscoveryPort),
	})
}

// UpdateSystem handles PUT /settings/system — stores all fields as explicit
// overrides. Takes effect on the next restart (the API surfaces that hint).
func (h *SettingsHandler) UpdateSystem(w http.ResponseWriter, r *http.Request) {
	var dto systemSettingDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	timeZone, ok := h.nextTimeZone(w, r, dto.TimeZone)
	if !ok {
		return
	}
	cfg := settings.SystemConfig{
		TimeZone:                timeZone,
		ServerName:              &dto.ServerName,
		RetainMonths:            &dto.RetainMonths,
		TMDBRateLimit:           &dto.TMDBRateLimit,
		PublicAssetCache:        &dto.PublicAssetCache,
		StaticABREnabled:        &dto.StaticABREnabled,
		MissingFileGraceMinutes: &dto.MissingFileGraceMinutes,
		ScanFileConcurrency:     &dto.ScanFileConcurrency,
		ScanLibraryConcurrency:  &dto.ScanLibraryConcurrency,
		DiscoveryEnabled:        &dto.DiscoveryEnabled,
		DiscoveryPort:           &dto.DiscoveryPort,
	}
	if err := h.svc.SetSystem(ctx, cfg); err != nil {
		h.logger.ErrorContext(ctx, "update system config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"system": "changed",
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}

// nextTimeZone resolves the time zone a System PUT stores: the stored one when
// the body has none, unset for "", else the name if it loads (a 400 is written
// and ok is false when it doesn't).
func (h *SettingsHandler) nextTimeZone(w http.ResponseWriter, r *http.Request, sent *string) (*string, bool) {
	if sent == nil {
		return h.svc.System(r.Context()).TimeZone, true
	}
	name := strings.TrimSpace(*sent)
	if name == "" {
		return nil, true
	}
	if err := settings.ValidateTimeZone(name); err != nil {
		respond.BadRequestT(w, r, "error.settings.timeZoneInvalid", i18n.Params{"zone": strconv.Quote(name)})
		return nil, false
	}
	return &name, true
}
