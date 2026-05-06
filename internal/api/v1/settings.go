package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/transcode"
)

// SettingsServiceIface is the subset of settings.Service used by the handler.
type SettingsServiceIface interface {
	TMDBAPIKey(ctx context.Context) string
	SetTMDBAPIKey(ctx context.Context, key string) error
	TVDBAPIKey(ctx context.Context) string
	SetTVDBAPIKey(ctx context.Context, key string) error
	ArrAPIKey(ctx context.Context) string
	SetArrAPIKey(ctx context.Context, key string) error
	ArrPathMappings(ctx context.Context) map[string]string
	SetArrPathMappings(ctx context.Context, mappings map[string]string) error
	TranscodeEncoders(ctx context.Context) string
	SetTranscodeEncoders(ctx context.Context, value string) error
	WorkerFleet(ctx context.Context) settings.WorkerFleetConfig
	SetWorkerFleet(ctx context.Context, cfg settings.WorkerFleetConfig) error
	TranscodeConfigGet(ctx context.Context) settings.TranscodeConfig
	SetTranscodeConfig(ctx context.Context, cfg settings.TranscodeConfig) error
	OpenSubtitles(ctx context.Context) settings.OpenSubtitlesConfig
	SetOpenSubtitles(ctx context.Context, cfg settings.OpenSubtitlesConfig) error
	OIDC(ctx context.Context) settings.OIDCConfig
	SetOIDC(ctx context.Context, cfg settings.OIDCConfig) error
	LDAP(ctx context.Context) settings.LDAPConfig
	SetLDAP(ctx context.Context, cfg settings.LDAPConfig) error
	SAML(ctx context.Context) settings.SAMLConfig
	SetSAML(ctx context.Context, cfg settings.SAMLConfig) error
	SMTP(ctx context.Context) settings.SMTPConfig
	SetSMTP(ctx context.Context, cfg settings.SMTPConfig) error
	OTel(ctx context.Context) settings.OTelConfig
	SetOTel(ctx context.Context, cfg settings.OTelConfig) error
	General(ctx context.Context) settings.GeneralConfig
	SetGeneral(ctx context.Context, cfg settings.GeneralConfig) error
}

// WorkerLister lists registered transcode workers from the session store.
type WorkerLister interface {
	ListWorkers(ctx context.Context) ([]transcode.WorkerRegistration, error)
}

// SettingsHandler handles GET/PATCH /api/v1/settings.
type SettingsHandler struct {
	svc              SettingsServiceIface
	logger           *slog.Logger
	audit            *audit.Logger
	detectedEncoders []transcode.EncoderEntry // populated at startup by DetectEncoders
	workerLister     WorkerLister             // set at startup to query registered workers
	embeddedDisabled bool                     // true when DISABLE_EMBEDDED_WORKER env is set
}

// NewSettingsHandler creates a SettingsHandler.
func NewSettingsHandler(svc SettingsServiceIface, logger *slog.Logger) *SettingsHandler {
	return &SettingsHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *SettingsHandler) WithAudit(a *audit.Logger) *SettingsHandler {
	h.audit = a
	return h
}

// SetDetectedEncoders stores the encoder entries discovered at startup.
func (h *SettingsHandler) SetDetectedEncoders(entries []transcode.EncoderEntry) {
	h.detectedEncoders = entries
}

// SetWorkerLister wires the session store for querying registered workers.
func (h *SettingsHandler) SetWorkerLister(wl WorkerLister) {
	h.workerLister = wl
}

// SetEmbeddedDisabled marks that the DISABLE_EMBEDDED_WORKER env var is set.
func (h *SettingsHandler) SetEmbeddedDisabled(disabled bool) {
	h.embeddedDisabled = disabled
}

// GetEncoders handles GET /api/v1/settings/encoders — returns available hw encoders.
// Merges server-detected encoders with capabilities reported by live workers.
func (h *SettingsHandler) GetEncoders(w http.ResponseWriter, r *http.Request) {
	current := h.svc.TranscodeEncoders(r.Context())

	// Start with server-detected encoders.
	seen := make(map[transcode.Encoder]bool, len(h.detectedEncoders))
	merged := make([]transcode.EncoderEntry, len(h.detectedEncoders))
	copy(merged, h.detectedEncoders)
	for _, e := range h.detectedEncoders {
		seen[e.Encoder] = true
	}

	// Add encoders reported by live workers that the server doesn't have,
	// using the worker's own GPU-detected labels (e.g. "NVIDIA GeForce RTX 5080").
	if h.workerLister != nil {
		workers, err := h.workerLister.ListWorkers(r.Context())
		if err == nil {
			for _, w := range workers {
				for _, cap := range w.Capabilities {
					enc := transcode.Encoder(cap)
					if !seen[enc] {
						seen[enc] = true
						label := w.EncoderLabels[cap]
						if label == "" {
							label = transcode.EncoderLabel(enc)
						}
						merged = append(merged, transcode.EncoderEntry{
							Encoder: enc,
							Label:   label,
						})
					}
				}
			}
		}
	}

	respond.Success(w, r, struct {
		Detected []transcode.EncoderEntry `json:"detected"`
		Current  string                   `json:"current"`
	}{
		Detected: merged,
		Current:  current,
	})
}

// GetWorkers handles GET /api/v1/settings/workers — returns registered transcode workers.
func (h *SettingsHandler) GetWorkers(w http.ResponseWriter, r *http.Request) {
	if h.workerLister == nil {
		respond.Success(w, r, []transcode.WorkerRegistration{})
		return
	}
	workers, err := h.workerLister.ListWorkers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list workers", "err", err)
		respond.Success(w, r, []transcode.WorkerRegistration{})
		return
	}
	if workers == nil {
		workers = []transcode.WorkerRegistration{}
	}
	respond.Success(w, r, workers)
}

// GetFleet handles GET /api/v1/settings/fleet — returns the worker fleet configuration.
// Workers self-register via Valkey; the fleet config stores admin overrides (name, encoder).
// The response merges live worker data with saved overrides.
func (h *SettingsHandler) GetFleet(w http.ResponseWriter, r *http.Request) {
	fleet := h.svc.WorkerFleet(r.Context())

	// Fetch live workers from Valkey.
	var liveWorkers []transcode.WorkerRegistration
	if h.workerLister != nil {
		liveWorkers, _ = h.workerLister.ListWorkers(r.Context())
	}

	// Index saved overrides by worker addr.
	overrides := make(map[string]settings.WorkerSlotConfig, len(fleet.Workers))
	for _, slot := range fleet.Workers {
		overrides[slot.Addr] = slot
	}

	type workerStatus struct {
		ID             string   `json:"id"`
		Addr           string   `json:"addr"`
		Name           string   `json:"name"`
		Encoder        string   `json:"encoder"`
		Online         bool     `json:"online"`
		ActiveSessions int      `json:"active_sessions"`
		MaxSessions    int      `json:"max_sessions"`
		Capabilities   []string `json:"capabilities"`
	}

	// Check embedded worker status.
	embeddedOnline := false
	embeddedActive, embeddedMax := 0, 0
	var embeddedCaps []string

	seen := make(map[string]bool, len(liveWorkers))
	var workers []workerStatus
	for _, live := range liveWorkers {
		// The embedded worker listens on 127.0.0.1:7073 — show it in the
		// embedded section, not as a local worker.
		if live.Addr == "127.0.0.1:7073" {
			embeddedOnline = true
			embeddedActive = live.ActiveSessions
			embeddedMax = live.MaxSessions
			embeddedCaps = live.Capabilities
			continue
		}

		seen[live.Addr] = true
		ws := workerStatus{
			ID:             live.Addr,
			Addr:           live.Addr,
			Online:         true,
			ActiveSessions: live.ActiveSessions,
			MaxSessions:    live.MaxSessions,
			Capabilities:   live.Capabilities,
		}
		if override, ok := overrides[live.Addr]; ok {
			ws.Name = override.Name
			ws.Encoder = override.Encoder
			if override.MaxSessions > 0 {
				ws.MaxSessions = override.MaxSessions
			}
		}
		workers = append(workers, ws)
	}

	// Append saved (manually-added) workers that aren't currently live.
	manualIdx := 0
	for _, slot := range fleet.Workers {
		if slot.Addr == "127.0.0.1:7073" {
			continue
		}
		if slot.Addr != "" && seen[slot.Addr] {
			continue
		}
		id := slot.Addr
		if id == "" {
			manualIdx++
			id = fmt.Sprintf("manual-%d", manualIdx)
		}
		workers = append(workers, workerStatus{
			ID:          id,
			Addr:        slot.Addr,
			Name:        slot.Name,
			Encoder:     slot.Encoder,
			MaxSessions: slot.MaxSessions,
			Online:      false,
		})
	}

	if workers == nil {
		workers = []workerStatus{}
	}

	// If the env var forces embedded off, override the DB value.
	embeddedEnabled := fleet.EmbeddedEnabled
	if h.embeddedDisabled {
		embeddedEnabled = false
	}

	respond.Success(w, r, struct {
		EmbeddedEnabled  bool           `json:"embedded_enabled"`
		EmbeddedDisabled bool           `json:"embedded_disabled_by_env"`
		EmbeddedEncoder  string         `json:"embedded_encoder"`
		EmbeddedOnline   bool           `json:"embedded_online"`
		EmbeddedActive   int            `json:"embedded_active_sessions"`
		EmbeddedMax      int            `json:"embedded_max_sessions"`
		EmbeddedCaps     []string       `json:"embedded_capabilities"`
		Workers          []workerStatus `json:"workers"`
	}{
		EmbeddedEnabled:  embeddedEnabled,
		EmbeddedDisabled: h.embeddedDisabled,
		EmbeddedEncoder:  fleet.EmbeddedEncoder,
		EmbeddedOnline:   embeddedOnline,
		EmbeddedActive:   embeddedActive,
		EmbeddedMax:      embeddedMax,
		EmbeddedCaps:     embeddedCaps,
		Workers:          workers,
	})
}

// UpdateFleet handles PUT /api/v1/settings/fleet — saves the worker fleet configuration.
func (h *SettingsHandler) UpdateFleet(w http.ResponseWriter, r *http.Request) {
	var body settings.WorkerFleetConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	if err := h.svc.SetWorkerFleet(ctx, body); err != nil {
		h.logger.ErrorContext(ctx, "update fleet config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		claims := middleware.ClaimsFromContext(ctx)
		if claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"worker_fleet": "changed",
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}

type settingsResponse struct {
	TMDBAPIKey        string                  `json:"tmdb_api_key"`
	TVDBAPIKey        string                  `json:"tvdb_api_key"`
	ArrAPIKey         string                  `json:"arr_api_key"`
	ArrWebhookURL     string                  `json:"arr_webhook_url"`
	ArrPathMappings   map[string]string       `json:"arr_path_mappings,omitempty"`
	TranscodeEncoders string                  `json:"transcode_encoders"`
	OpenSubtitles     openSubtitlesSettingDTO `json:"opensubtitles"`
	OIDC              oidcSettingDTO          `json:"oidc"`
	LDAP              ldapSettingDTO          `json:"ldap"`
	SAML              samlSettingDTO          `json:"saml"`
	SMTP              smtpSettingDTO          `json:"smtp"`
	OTel              otelSettingDTO          `json:"otel"`
	General           generalSettingDTO       `json:"general"`
}

// generalSettingDTO mirrors settings.GeneralConfig — no secrets, surfaces the
// public URL, log level and CORS allow-list. All restart-required.
type generalSettingDTO struct {
	BaseURL            string   `json:"base_url"`
	LogLevel           string   `json:"log_level"`
	CORSAllowedOrigins []string `json:"cors_allowed_origins"`
}

func toGeneralDTO(cfg settings.GeneralConfig) generalSettingDTO {
	origins := cfg.CORSAllowedOrigins
	if origins == nil {
		origins = []string{}
	}
	return generalSettingDTO{
		BaseURL:            cfg.BaseURL,
		LogLevel:           cfg.LogLevel,
		CORSAllowedOrigins: origins,
	}
}

// otelSettingDTO mirrors settings.OTelConfig — no secrets to mask, but kept
// as a typed DTO so the frontend has a stable shape and we can add the
// "restart required" hint alongside if it ever becomes data-dependent.
type otelSettingDTO struct {
	Enabled       bool    `json:"enabled"`
	Endpoint      string  `json:"endpoint"`
	SampleRatio   float64 `json:"sample_ratio"`
	DeploymentEnv string  `json:"deployment_env"`
}

func toOTelDTO(cfg settings.OTelConfig) otelSettingDTO {
	return otelSettingDTO{
		Enabled:       cfg.Enabled,
		Endpoint:      cfg.Endpoint,
		SampleRatio:   cfg.SampleRatio,
		DeploymentEnv: cfg.DeploymentEnv,
	}
}

// smtpSettingDTO masks the SMTP password before returning to the client.
type smtpSettingDTO struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"` // "****" if set, "" if empty
	From     string `json:"from"`
}

func toSMTPDTO(cfg settings.SMTPConfig) smtpSettingDTO {
	pw := ""
	if cfg.Password != "" {
		pw = "****"
	}
	return smtpSettingDTO{
		Enabled:  cfg.Enabled,
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: pw,
		From:     cfg.From,
	}
}

// oidcSettingDTO masks the client secret before returning to the client.
type oidcSettingDTO struct {
	Enabled       bool   `json:"enabled"`
	DisplayName   string `json:"display_name"`
	IssuerURL     string `json:"issuer_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"` // "****" if set, "" if empty
	Scopes        string `json:"scopes"`
	UsernameClaim string `json:"username_claim"`
	GroupsClaim   string `json:"groups_claim"`
	AdminGroup    string `json:"admin_group"`
}

func toOIDCDTO(cfg settings.OIDCConfig) oidcSettingDTO {
	cs := ""
	if cfg.ClientSecret != "" {
		cs = "****"
	}
	return oidcSettingDTO{
		Enabled:       cfg.Enabled,
		DisplayName:   cfg.DisplayName,
		IssuerURL:     cfg.IssuerURL,
		ClientID:      cfg.ClientID,
		ClientSecret:  cs,
		Scopes:        cfg.Scopes,
		UsernameClaim: cfg.UsernameClaim,
		GroupsClaim:   cfg.GroupsClaim,
		AdminGroup:    cfg.AdminGroup,
	}
}

// ldapSettingDTO masks the bind password before returning to the client.
type ldapSettingDTO struct {
	Enabled        bool   `json:"enabled"`
	DisplayName    string `json:"display_name"`
	Host           string `json:"host"`
	StartTLS       bool   `json:"start_tls"`
	UseLDAPS       bool   `json:"use_ldaps"`
	SkipTLSVerify  bool   `json:"skip_tls_verify"`
	BindDN         string `json:"bind_dn"`
	BindPassword   string `json:"bind_password"` // "****" if set, "" if empty
	UserSearchBase string `json:"user_search_base"`
	UserFilter     string `json:"user_filter"`
	UsernameAttr   string `json:"username_attr"`
	EmailAttr      string `json:"email_attr"`
	AdminGroupDN   string `json:"admin_group_dn"`
}

func toLDAPDTO(cfg settings.LDAPConfig) ldapSettingDTO {
	pw := ""
	if cfg.BindPassword != "" {
		pw = "****"
	}
	return ldapSettingDTO{
		Enabled:        cfg.Enabled,
		DisplayName:    cfg.DisplayName,
		Host:           cfg.Host,
		StartTLS:       cfg.StartTLS,
		UseLDAPS:       cfg.UseLDAPS,
		SkipTLSVerify:  cfg.SkipTLSVerify,
		BindDN:         cfg.BindDN,
		BindPassword:   pw,
		UserSearchBase: cfg.UserSearchBase,
		UserFilter:     cfg.UserFilter,
		UsernameAttr:   cfg.UsernameAttr,
		EmailAttr:      cfg.EmailAttr,
		AdminGroupDN:   cfg.AdminGroupDN,
	}
}

// samlSettingDTO masks the SP private key (and IdP-supplied secret if any)
// before returning the config to the client. The certificate stays plain
// because admins copy-paste it into IdP registration UIs.
type samlSettingDTO struct {
	Enabled           bool   `json:"enabled"`
	DisplayName       string `json:"display_name"`
	IdPMetadataURL    string `json:"idp_metadata_url"`
	EntityID          string `json:"entity_id"`
	SPCertificatePEM  string `json:"sp_certificate_pem"`
	SPPrivateKeyPEM   string `json:"sp_private_key_pem"` // "****" if set, "" if empty
	EmailAttribute    string `json:"email_attribute"`
	UsernameAttribute string `json:"username_attribute"`
	GroupsAttribute   string `json:"groups_attribute"`
	AdminGroup        string `json:"admin_group"`
}

func toSAMLDTO(cfg settings.SAMLConfig) samlSettingDTO {
	pk := ""
	if cfg.SPPrivateKeyPEM != "" {
		pk = "****"
	}
	return samlSettingDTO{
		Enabled:           cfg.Enabled,
		DisplayName:       cfg.DisplayName,
		IdPMetadataURL:    cfg.IdPMetadataURL,
		EntityID:          cfg.EntityID,
		SPCertificatePEM:  cfg.SPCertificatePEM,
		SPPrivateKeyPEM:   pk,
		EmailAttribute:    cfg.EmailAttribute,
		UsernameAttribute: cfg.UsernameAttribute,
		GroupsAttribute:   cfg.GroupsAttribute,
		AdminGroup:        cfg.AdminGroup,
	}
}

// openSubtitlesSettingDTO masks the API key and password before returning the
// config to the client — secrets should never leave the server in plaintext.
type openSubtitlesSettingDTO struct {
	APIKey    string `json:"api_key"`
	Username  string `json:"username"`
	Password  string `json:"password"` // "****" if set, "" if empty
	Languages string `json:"languages"`
	Enabled   bool   `json:"enabled"`
}

func toOpenSubtitlesDTO(cfg settings.OpenSubtitlesConfig) openSubtitlesSettingDTO {
	pw := ""
	if cfg.Password != "" {
		pw = "****"
	}
	return openSubtitlesSettingDTO{
		APIKey:    maskAPIKey(cfg.APIKey),
		Username:  cfg.Username,
		Password:  pw,
		Languages: cfg.Languages,
		Enabled:   cfg.Enabled,
	}
}

// maskAPIKey returns "****" when a key is set, "" when not.
//
// Earlier versions returned `key[:4] + "****"` to surface a recognisable
// prefix, but that's meaningful entropy disclosure once paired with a
// log line, screenshot shared with support, or a backup snapshot —
// 4 hex chars of a 32-hex TMDB v3 key narrows a brute-force search
// space. Other secret fields in this same handler (SMTP password,
// OIDC client secret, LDAP bind password, SAML SP private key) all
// return bare `****`; align the API keys with that posture.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	return "****"
}

// generateAPIKey creates a random 32-character hex API key.
func generateAPIKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get handles GET /api/v1/settings.
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	arrKey := h.svc.ArrAPIKey(ctx)

	// Auto-generate an arr API key if none exists.
	if arrKey == "" {
		arrKey = generateAPIKey()
		if err := h.svc.SetArrAPIKey(ctx, arrKey); err != nil {
			h.logger.ErrorContext(ctx, "auto-generate arr api key", "err", err)
		}
	}

	// Build the webhook URL from the request so the admin can copy it
	// into arr apps. The API key is sent via the X-Api-Key header (configure
	// it under the connection's "Custom Headers" in Sonarr/Radarr/Lidarr) —
	// query-string keys leak into access logs / browser history / referer.
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	webhookURL := scheme + "://" + r.Host + "/api/v1/arr/webhook"
	_ = arrKey // returned in the response below; the admin pastes it into the X-Api-Key custom header

	respond.Success(w, r, settingsResponse{
		TMDBAPIKey:        maskAPIKey(h.svc.TMDBAPIKey(ctx)),
		TVDBAPIKey:        maskAPIKey(h.svc.TVDBAPIKey(ctx)),
		ArrAPIKey:         maskAPIKey(arrKey),
		ArrWebhookURL:     webhookURL,
		ArrPathMappings:   h.svc.ArrPathMappings(ctx),
		TranscodeEncoders: h.svc.TranscodeEncoders(ctx),
		OpenSubtitles:     toOpenSubtitlesDTO(h.svc.OpenSubtitles(ctx)),
		OIDC:              toOIDCDTO(h.svc.OIDC(ctx)),
		LDAP:              toLDAPDTO(h.svc.LDAP(ctx)),
		SAML:              toSAMLDTO(h.svc.SAML(ctx)),
		SMTP:              toSMTPDTO(h.svc.SMTP(ctx)),
		OTel:              toOTelDTO(h.svc.OTel(ctx)),
		General:           toGeneralDTO(h.svc.General(ctx)),
	})
}

// Update handles PATCH /api/v1/settings.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TMDBAPIKey        *string            `json:"tmdb_api_key"`
		TVDBAPIKey        *string            `json:"tvdb_api_key"`
		ArrAPIKey         *string            `json:"arr_api_key"`
		ArrPathMappings   *map[string]string `json:"arr_path_mappings"`
		TranscodeEncoders *string            `json:"transcode_encoders"`
		OpenSubtitles     *struct {
			APIKey    *string `json:"api_key"`
			Username  *string `json:"username"`
			Password  *string `json:"password"`
			Languages *string `json:"languages"`
			Enabled   *bool   `json:"enabled"`
		} `json:"opensubtitles"`
		OIDC *struct {
			Enabled       *bool   `json:"enabled"`
			DisplayName   *string `json:"display_name"`
			IssuerURL     *string `json:"issuer_url"`
			ClientID      *string `json:"client_id"`
			ClientSecret  *string `json:"client_secret"`
			Scopes        *string `json:"scopes"`
			UsernameClaim *string `json:"username_claim"`
			GroupsClaim   *string `json:"groups_claim"`
			AdminGroup    *string `json:"admin_group"`
		} `json:"oidc"`
		LDAP *struct {
			Enabled        *bool   `json:"enabled"`
			DisplayName    *string `json:"display_name"`
			Host           *string `json:"host"`
			StartTLS       *bool   `json:"start_tls"`
			UseLDAPS       *bool   `json:"use_ldaps"`
			SkipTLSVerify  *bool   `json:"skip_tls_verify"`
			BindDN         *string `json:"bind_dn"`
			BindPassword   *string `json:"bind_password"`
			UserSearchBase *string `json:"user_search_base"`
			UserFilter     *string `json:"user_filter"`
			UsernameAttr   *string `json:"username_attr"`
			EmailAttr      *string `json:"email_attr"`
			AdminGroupDN   *string `json:"admin_group_dn"`
		} `json:"ldap"`
		SAML *struct {
			Enabled           *bool   `json:"enabled"`
			DisplayName       *string `json:"display_name"`
			IdPMetadataURL    *string `json:"idp_metadata_url"`
			EntityID          *string `json:"entity_id"`
			SPCertificatePEM  *string `json:"sp_certificate_pem"`
			SPPrivateKeyPEM   *string `json:"sp_private_key_pem"`
			EmailAttribute    *string `json:"email_attribute"`
			UsernameAttribute *string `json:"username_attribute"`
			GroupsAttribute   *string `json:"groups_attribute"`
			AdminGroup        *string `json:"admin_group"`
		} `json:"saml"`
		SMTP *struct {
			Enabled  *bool   `json:"enabled"`
			Host     *string `json:"host"`
			Port     *int    `json:"port"`
			Username *string `json:"username"`
			Password *string `json:"password"`
			From     *string `json:"from"`
		} `json:"smtp"`
		OTel *struct {
			Enabled       *bool    `json:"enabled"`
			Endpoint      *string  `json:"endpoint"`
			SampleRatio   *float64 `json:"sample_ratio"`
			DeploymentEnv *string  `json:"deployment_env"`
		} `json:"otel"`
		General *struct {
			BaseURL            *string   `json:"base_url"`
			LogLevel           *string   `json:"log_level"`
			CORSAllowedOrigins *[]string `json:"cors_allowed_origins"`
		} `json:"general"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	if body.TMDBAPIKey != nil {
		if err := h.svc.SetTMDBAPIKey(ctx, *body.TMDBAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "tmdb_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.TVDBAPIKey != nil {
		if err := h.svc.SetTVDBAPIKey(ctx, *body.TVDBAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "tvdb_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.ArrAPIKey != nil {
		if err := h.svc.SetArrAPIKey(ctx, *body.ArrAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "arr_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.ArrPathMappings != nil {
		if err := h.svc.SetArrPathMappings(ctx, *body.ArrPathMappings); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "arr_path_mappings", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.TranscodeEncoders != nil {
		if err := h.svc.SetTranscodeEncoders(ctx, *body.TranscodeEncoders); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "transcode_encoders", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.OpenSubtitles != nil {
		// Read-modify-write so partial updates (e.g. toggling Enabled without
		// re-sending the API key) don't clobber the stored credentials.
		cur := h.svc.OpenSubtitles(ctx)
		if body.OpenSubtitles.APIKey != nil {
			cur.APIKey = *body.OpenSubtitles.APIKey
		}
		if body.OpenSubtitles.Username != nil {
			cur.Username = *body.OpenSubtitles.Username
		}
		if body.OpenSubtitles.Password != nil && *body.OpenSubtitles.Password != "****" {
			cur.Password = *body.OpenSubtitles.Password
		}
		if body.OpenSubtitles.Languages != nil {
			cur.Languages = *body.OpenSubtitles.Languages
		}
		if body.OpenSubtitles.Enabled != nil {
			cur.Enabled = *body.OpenSubtitles.Enabled
		}
		if err := h.svc.SetOpenSubtitles(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "opensubtitles", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.OIDC != nil {
		cur := h.svc.OIDC(ctx)
		if body.OIDC.Enabled != nil {
			cur.Enabled = *body.OIDC.Enabled
		}
		if body.OIDC.DisplayName != nil {
			cur.DisplayName = *body.OIDC.DisplayName
		}
		if body.OIDC.IssuerURL != nil {
			cur.IssuerURL = *body.OIDC.IssuerURL
		}
		if body.OIDC.ClientID != nil {
			cur.ClientID = *body.OIDC.ClientID
		}
		if body.OIDC.ClientSecret != nil && *body.OIDC.ClientSecret != "****" {
			cur.ClientSecret = *body.OIDC.ClientSecret
		}
		if body.OIDC.Scopes != nil {
			cur.Scopes = *body.OIDC.Scopes
		}
		if body.OIDC.UsernameClaim != nil {
			cur.UsernameClaim = *body.OIDC.UsernameClaim
		}
		if body.OIDC.GroupsClaim != nil {
			cur.GroupsClaim = *body.OIDC.GroupsClaim
		}
		if body.OIDC.AdminGroup != nil {
			cur.AdminGroup = *body.OIDC.AdminGroup
		}
		if err := h.svc.SetOIDC(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "oidc", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.LDAP != nil {
		cur := h.svc.LDAP(ctx)
		if body.LDAP.Enabled != nil {
			cur.Enabled = *body.LDAP.Enabled
		}
		if body.LDAP.DisplayName != nil {
			cur.DisplayName = *body.LDAP.DisplayName
		}
		if body.LDAP.Host != nil {
			cur.Host = *body.LDAP.Host
		}
		if body.LDAP.StartTLS != nil {
			cur.StartTLS = *body.LDAP.StartTLS
		}
		if body.LDAP.UseLDAPS != nil {
			cur.UseLDAPS = *body.LDAP.UseLDAPS
		}
		if body.LDAP.SkipTLSVerify != nil {
			cur.SkipTLSVerify = *body.LDAP.SkipTLSVerify
		}
		if body.LDAP.BindDN != nil {
			cur.BindDN = *body.LDAP.BindDN
		}
		if body.LDAP.BindPassword != nil && *body.LDAP.BindPassword != "****" {
			cur.BindPassword = *body.LDAP.BindPassword
		}
		if body.LDAP.UserSearchBase != nil {
			cur.UserSearchBase = *body.LDAP.UserSearchBase
		}
		if body.LDAP.UserFilter != nil {
			cur.UserFilter = *body.LDAP.UserFilter
		}
		if body.LDAP.UsernameAttr != nil {
			cur.UsernameAttr = *body.LDAP.UsernameAttr
		}
		if body.LDAP.EmailAttr != nil {
			cur.EmailAttr = *body.LDAP.EmailAttr
		}
		if body.LDAP.AdminGroupDN != nil {
			cur.AdminGroupDN = *body.LDAP.AdminGroupDN
		}
		if err := h.svc.SetLDAP(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "ldap", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.SAML != nil {
		// Read-modify-write so toggling enabled or rotating one field
		// doesn't clobber the SP keypair (which the IdP has registered
		// against and that an admin would have to re-publish if lost).
		cur := h.svc.SAML(ctx)
		if body.SAML.Enabled != nil {
			cur.Enabled = *body.SAML.Enabled
		}
		if body.SAML.DisplayName != nil {
			cur.DisplayName = *body.SAML.DisplayName
		}
		if body.SAML.IdPMetadataURL != nil {
			cur.IdPMetadataURL = *body.SAML.IdPMetadataURL
		}
		if body.SAML.EntityID != nil {
			cur.EntityID = *body.SAML.EntityID
		}
		if body.SAML.SPCertificatePEM != nil {
			cur.SPCertificatePEM = *body.SAML.SPCertificatePEM
		}
		if body.SAML.SPPrivateKeyPEM != nil && *body.SAML.SPPrivateKeyPEM != "****" {
			cur.SPPrivateKeyPEM = *body.SAML.SPPrivateKeyPEM
		}
		if body.SAML.EmailAttribute != nil {
			cur.EmailAttribute = *body.SAML.EmailAttribute
		}
		if body.SAML.UsernameAttribute != nil {
			cur.UsernameAttribute = *body.SAML.UsernameAttribute
		}
		if body.SAML.GroupsAttribute != nil {
			cur.GroupsAttribute = *body.SAML.GroupsAttribute
		}
		if body.SAML.AdminGroup != nil {
			cur.AdminGroup = *body.SAML.AdminGroup
		}
		// Auto-generate SP keypair on first enable so the admin doesn't
		// have to know how to invoke openssl. Skip when the admin has
		// already supplied one (cert OR key non-empty), and skip when
		// disabling — generating a keypair just to immediately set
		// Enabled=false would surprise.
		if cur.Enabled && cur.SPCertificatePEM == "" && cur.SPPrivateKeyPEM == "" {
			seed := cur.EntityID
			if seed == "" {
				seed = "onscreen"
			}
			if cert, key, err := GenerateSPKeyPair(seed); err == nil {
				cur.SPCertificatePEM = cert
				cur.SPPrivateKeyPEM = key
			} else {
				h.logger.ErrorContext(ctx, "saml: auto-generate SP keypair", "err", err)
				respond.InternalError(w, r)
				return
			}
		}
		if err := h.svc.SetSAML(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "saml", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.SMTP != nil {
		cur := h.svc.SMTP(ctx)
		if body.SMTP.Enabled != nil {
			cur.Enabled = *body.SMTP.Enabled
		}
		if body.SMTP.Host != nil {
			cur.Host = *body.SMTP.Host
		}
		if body.SMTP.Port != nil {
			cur.Port = *body.SMTP.Port
		}
		if body.SMTP.Username != nil {
			cur.Username = *body.SMTP.Username
		}
		if body.SMTP.Password != nil && *body.SMTP.Password != "****" {
			cur.Password = *body.SMTP.Password
		}
		if body.SMTP.From != nil {
			cur.From = *body.SMTP.From
		}
		if err := h.svc.SetSMTP(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "smtp", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.OTel != nil {
		cur := h.svc.OTel(ctx)
		if body.OTel.Enabled != nil {
			cur.Enabled = *body.OTel.Enabled
		}
		if body.OTel.Endpoint != nil {
			cur.Endpoint = *body.OTel.Endpoint
		}
		if body.OTel.SampleRatio != nil {
			cur.SampleRatio = *body.OTel.SampleRatio
		}
		if body.OTel.DeploymentEnv != nil {
			cur.DeploymentEnv = *body.OTel.DeploymentEnv
		}
		if err := h.svc.SetOTel(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "otel", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.General != nil {
		cur := h.svc.General(ctx)
		if body.General.BaseURL != nil {
			cur.BaseURL = *body.General.BaseURL
		}
		if body.General.LogLevel != nil {
			cur.LogLevel = *body.General.LogLevel
		}
		if body.General.CORSAllowedOrigins != nil {
			cur.CORSAllowedOrigins = *body.General.CORSAllowedOrigins
		}
		if err := h.svc.SetGeneral(ctx, cur); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "general", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if h.audit != nil {
		detail := map[string]any{}
		if body.TMDBAPIKey != nil {
			detail["tmdb_api_key"] = "changed"
		}
		if body.TVDBAPIKey != nil {
			detail["tvdb_api_key"] = "changed"
		}
		if body.ArrAPIKey != nil {
			detail["arr_api_key"] = "changed"
		}
		if body.ArrPathMappings != nil {
			detail["arr_path_mappings"] = "changed"
		}
		if body.TranscodeEncoders != nil {
			detail["transcode_encoders"] = *body.TranscodeEncoders
		}
		if body.OpenSubtitles != nil {
			detail["opensubtitles"] = "changed"
		}
		if body.OIDC != nil {
			detail["oidc"] = "changed"
		}
		if body.LDAP != nil {
			detail["ldap"] = "changed"
		}
		if body.SAML != nil {
			detail["saml"] = "changed"
		}
		if body.SMTP != nil {
			detail["smtp"] = "changed"
		}
		if body.OTel != nil {
			detail["otel"] = "changed"
		}
		if body.General != nil {
			detail["general"] = "changed"
		}
		// Always log, even if claims are somehow nil — the route is admin-gated,
		// so a missing actor here means an invariant break worth recording.
		var actor *uuid.UUID
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			actor = &claims.UserID
		}
		h.audit.Log(r.Context(), actor, audit.ActionSettingsUpdate, "", detail, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// GetTranscodeConfig handles GET /api/v1/settings/transcode-config.
func (h *SettingsHandler) GetTranscodeConfig(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, h.svc.TranscodeConfigGet(r.Context()))
}

// UpdateTranscodeConfig handles PUT /api/v1/settings/transcode-config.
func (h *SettingsHandler) UpdateTranscodeConfig(w http.ResponseWriter, r *http.Request) {
	var body settings.TranscodeConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	if err := h.svc.SetTranscodeConfig(ctx, body); err != nil {
		h.logger.ErrorContext(ctx, "update transcode config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		claims := middleware.ClaimsFromContext(ctx)
		if claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"transcode_config": "changed",
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
