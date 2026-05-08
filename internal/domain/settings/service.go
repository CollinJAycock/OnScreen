// Package settings manages application-wide server settings stored in the DB.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/auth"
)

// ErrInvalidSetting is returned when a caller tries to persist a value that
// isn't in the allowed set (e.g. a bogus IntroDetectionMode).
var ErrInvalidSetting = errors.New("invalid setting value")

const keyTMDBAPIKey = "tmdb_api_key"
const keyTVDBAPIKey = "tvdb_api_key"
const keyArrAPIKey = "arr_api_key"
const keyArrPathMappings = "arr_path_mappings"
const keyTranscodeEncoders = "transcode_encoders"
const keyWorkerFleet = "worker_fleet"
const keyTranscodeConfig = "transcode_config"
const keyIntroDetectionMode = "intro_detection_mode"
const keyOpenSubtitlesConfig = "opensubtitles_config"
const keyOIDCConfig = "oidc_config"
const keySAMLConfig = "saml_config"
const keyLDAPConfig = "ldap_config"
const keySMTPConfig = "smtp_config"
const keyOTelConfig = "otel_config"
const keyGeneralConfig = "general_config"

// IntroDetectionMode controls whether the worker auto-detects intro and
// credits markers on each scan.
type IntroDetectionMode string

const (
	IntroDetectionOff    IntroDetectionMode = "off"
	IntroDetectionOnScan IntroDetectionMode = "on_scan"
	IntroDetectionManual IntroDetectionMode = "manual"
)

// Service reads and writes server settings to the server_settings table.
//
// When an Encryptor is wired via WithEncryptor, values for keys in the
// secretKeys allowlist are AES-256-GCM-encrypted at rest with the
// `encv1:` sentinel prefix. Reads transparently decrypt; reads of
// unencrypted (legacy) rows for allowlisted keys return the value
// as-is and log a one-shot warning so the operator can re-save the
// setting to migrate it to the encrypted form.
type Service struct {
	db     *pgxpool.Pool
	logger *slog.Logger
	enc    *auth.Encryptor // optional; nil disables at-rest encryption
}

// New creates a Service.
func New(db *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{db: db, logger: logger}
}

// WithEncryptor enables at-rest encryption for the secret-bearing
// settings keys. Without it, values land in server_settings as
// plaintext (back-compat for installs predating this change). Returns
// the service for chaining.
func (s *Service) WithEncryptor(enc *auth.Encryptor) *Service {
	s.enc = enc
	return s
}

// encPrefix marks a value as AES-GCM-encrypted-with-the-server-key. The
// version suffix (`v1`) lets a future cipher swap migrate cleanly:
// new writes get the new prefix, old reads dispatch on the prefix
// string to pick the right Decrypt path.
const encPrefix = "encv1:"

// secretKeys lists the settings keys whose stored values must be
// encrypted at rest. Keys not in this set stay as plaintext (paths,
// config blobs, encoder lists — operational data, not credentials).
var secretKeys = map[string]struct{}{
	keyTMDBAPIKey:          {},
	keyTVDBAPIKey:          {},
	keyArrAPIKey:           {},
	keyOpenSubtitlesConfig: {}, // contains user/password
	keyOIDCConfig:          {}, // contains client_secret
	keySAMLConfig:          {}, // contains SP private key PEM
	keyLDAPConfig:          {}, // contains bind password
	keySMTPConfig:          {}, // contains password
}

func (s *Service) isSecretKey(key string) bool {
	_, ok := secretKeys[key]
	return ok
}

// TMDBAPIKey returns the stored TMDB API key, or "" if not set.
func (s *Service) TMDBAPIKey(ctx context.Context) string {
	return s.get(ctx, keyTMDBAPIKey)
}

// SetTMDBAPIKey persists the TMDB API key (empty string clears it).
func (s *Service) SetTMDBAPIKey(ctx context.Context, key string) error {
	return s.set(ctx, keyTMDBAPIKey, key)
}

// TVDBAPIKey returns the stored TheTVDB API key, or "" if not set.
func (s *Service) TVDBAPIKey(ctx context.Context) string {
	return s.get(ctx, keyTVDBAPIKey)
}

// SetTVDBAPIKey persists the TheTVDB API key (empty string clears it).
func (s *Service) SetTVDBAPIKey(ctx context.Context, key string) error {
	return s.set(ctx, keyTVDBAPIKey, key)
}

// ArrAPIKey returns the stored API key for arr app notifications, or "" if not set.
func (s *Service) ArrAPIKey(ctx context.Context) string {
	return s.get(ctx, keyArrAPIKey)
}

// SetArrAPIKey persists the arr notification API key.
func (s *Service) SetArrAPIKey(ctx context.Context, key string) error {
	return s.set(ctx, keyArrAPIKey, key)
}

// ArrPathMappings returns path prefix mappings (remote → local) for arr webhooks.
// Returns an empty map if not configured.
func (s *Service) ArrPathMappings(ctx context.Context) map[string]string {
	raw := s.get(ctx, keyArrPathMappings)
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		s.logger.ErrorContext(ctx, "parse arr_path_mappings", "err", err)
		return nil
	}
	return m
}

// SetArrPathMappings persists the arr path prefix mappings as JSON.
func (s *Service) SetArrPathMappings(ctx context.Context, mappings map[string]string) error {
	b, err := json.Marshal(mappings)
	if err != nil {
		return err
	}
	return s.set(ctx, keyArrPathMappings, string(b))
}

// TranscodeEncoders returns the encoder override string (e.g. "nvenc,software"), or "" for auto-detect.
func (s *Service) TranscodeEncoders(ctx context.Context) string {
	return s.get(ctx, keyTranscodeEncoders)
}

// SetTranscodeEncoders persists the encoder override (empty string = auto-detect).
func (s *Service) SetTranscodeEncoders(ctx context.Context, value string) error {
	return s.set(ctx, keyTranscodeEncoders, value)
}

// WorkerFleetConfig is the admin-managed fleet of transcode workers.
type WorkerFleetConfig struct {
	EmbeddedEnabled bool               `json:"embedded_enabled"`
	EmbeddedEncoder string             `json:"embedded_encoder"` // e.g. "h264_nvenc", "" = auto
	Workers         []WorkerSlotConfig `json:"workers"`
}

// WorkerSlotConfig stores admin overrides for a discovered worker.
// Workers self-register via Valkey; the admin only assigns a name and encoder.
// Addr is the stable key (from the worker's WORKER_ADDR env var) and is
// auto-populated from discovery — the admin never types it.
type WorkerSlotConfig struct {
	Addr        string `json:"addr"`                   // stable key — from worker's WORKER_ADDR env
	Name        string `json:"name,omitempty"`         // admin-assigned friendly label
	Encoder     string `json:"encoder,omitempty"`      // admin encoder override, "" = auto-detect
	MaxSessions int    `json:"max_sessions,omitempty"` // admin override for max concurrent sessions, 0 = use worker default
}

// WorkerFleet returns the fleet configuration, or a default (embedded enabled, no remotes).
func (s *Service) WorkerFleet(ctx context.Context) WorkerFleetConfig {
	raw := s.get(ctx, keyWorkerFleet)
	if raw == "" {
		return WorkerFleetConfig{EmbeddedEnabled: true}
	}
	var cfg WorkerFleetConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse worker_fleet", "err", err)
		return WorkerFleetConfig{EmbeddedEnabled: true}
	}
	return cfg
}

// SetWorkerFleet persists the fleet configuration.
func (s *Service) SetWorkerFleet(ctx context.Context, cfg WorkerFleetConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyWorkerFleet, string(b))
}

// TranscodeConfig holds per-deployment encoder tuning knobs that are
// adjustable from the admin UI. Zero values mean "use server default".
type TranscodeConfig struct {
	NVENCPreset  string  `json:"nvenc_preset,omitempty"`  // p1–p7
	NVENCTune    string  `json:"nvenc_tune,omitempty"`    // hq, ll, ull
	NVENCRC      string  `json:"nvenc_rc,omitempty"`      // vbr, cbr, constqp
	MaxrateRatio float64 `json:"maxrate_ratio,omitempty"` // e.g. 1.5
}

// TranscodeConfigGet returns the transcode encoder tuning config.
// Returns zero-value TranscodeConfig if not stored (all defaults).
func (s *Service) TranscodeConfigGet(ctx context.Context) TranscodeConfig {
	raw := s.get(ctx, keyTranscodeConfig)
	if raw == "" {
		return TranscodeConfig{}
	}
	var cfg TranscodeConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse transcode_config", "err", err)
		return TranscodeConfig{}
	}
	return cfg
}

// SetTranscodeConfig persists the transcode encoder tuning config.
func (s *Service) SetTranscodeConfig(ctx context.Context, cfg TranscodeConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyTranscodeConfig, string(b))
}

// IntroDetectionMode returns the current detection mode. Defaults to on_scan
// if nothing is persisted (matches the migration seed).
func (s *Service) IntroDetectionMode(ctx context.Context) IntroDetectionMode {
	v := s.get(ctx, keyIntroDetectionMode)
	switch IntroDetectionMode(v) {
	case IntroDetectionOff, IntroDetectionManual:
		return IntroDetectionMode(v)
	default:
		return IntroDetectionOnScan
	}
}

// SetIntroDetectionMode persists the detection mode. Invalid values are rejected.
func (s *Service) SetIntroDetectionMode(ctx context.Context, mode IntroDetectionMode) error {
	switch mode {
	case IntroDetectionOff, IntroDetectionOnScan, IntroDetectionManual:
		return s.set(ctx, keyIntroDetectionMode, string(mode))
	default:
		return ErrInvalidSetting
	}
}

// OpenSubtitlesConfig stores credentials and defaults for the OpenSubtitles
// integration. APIKey is required; Username/Password are optional but bump the
// per-day download quota. Languages is a comma-separated ISO-639-1 list used
// as the default when callers don't override it.
type OpenSubtitlesConfig struct {
	APIKey    string `json:"api_key"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Languages string `json:"languages,omitempty"` // e.g. "en,es"
	Enabled   bool   `json:"enabled"`
}

// OpenSubtitles returns the stored OpenSubtitles configuration. Returns the
// zero value if nothing is persisted yet.
func (s *Service) OpenSubtitles(ctx context.Context) OpenSubtitlesConfig {
	raw := s.get(ctx, keyOpenSubtitlesConfig)
	if raw == "" {
		return OpenSubtitlesConfig{}
	}
	var cfg OpenSubtitlesConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse opensubtitles_config", "err", err)
		return OpenSubtitlesConfig{}
	}
	return cfg
}

// SetOpenSubtitles persists the OpenSubtitles configuration as JSON.
func (s *Service) SetOpenSubtitles(ctx context.Context, cfg OpenSubtitlesConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyOpenSubtitlesConfig, string(b))
}

// OIDCConfig holds the configuration for a single generic OIDC identity
// provider (Authentik, Keycloak, Auth0, Google Workspace, etc.).
//
// IssuerURL is the discovery base URL — the handler appends
// /.well-known/openid-configuration to find the auth/token/jwks endpoints.
//
// AdminGroup is matched against the configured GroupsClaim from the ID token;
// users present in that group are auto-promoted to admin on each login. Empty
// disables admin sync (only the first-ever user becomes admin, as elsewhere).
type OIDCConfig struct {
	Enabled       bool   `json:"enabled"`
	DisplayName   string `json:"display_name,omitempty"` // shown on the "Sign in with X" button
	IssuerURL     string `json:"issuer_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret,omitempty"`
	Scopes        string `json:"scopes,omitempty"`        // space-separated; default "openid profile email"
	UsernameClaim string `json:"username_claim,omitempty"` // default "preferred_username", falls back to email-prefix
	GroupsClaim   string `json:"groups_claim,omitempty"`   // default "groups"
	AdminGroup    string `json:"admin_group,omitempty"`
}

// OIDC returns the stored OIDC config or the zero value if not persisted.
func (s *Service) OIDC(ctx context.Context) OIDCConfig {
	raw := s.get(ctx, keyOIDCConfig)
	if raw == "" {
		return OIDCConfig{}
	}
	var cfg OIDCConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse oidc_config", "err", err)
		return OIDCConfig{}
	}
	return cfg
}

// SetOIDC persists the OIDC config as JSON.
func (s *Service) SetOIDC(ctx context.Context, cfg OIDCConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyOIDCConfig, string(b))
}

// SAMLConfig holds the configuration for SAML 2.0 SP-initiated SSO.
//
// IdPMetadataURL points at the IdP's published metadata XML (Okta,
// Azure AD, Auth0, OneLogin, ADFS all expose one). The metadata
// carries the IdP's signing cert + SSO endpoint URL — refreshed on
// each login attempt so a cert rotation upstream doesn't require
// an OnScreen restart.
//
// SP cert + key sign the SAMLRequest we send to the IdP and decrypt
// any encrypted assertions in the SAMLResponse. Auto-generated on
// first enable (RSA 2048 + self-signed); the metadata our SP
// publishes at /api/v1/auth/saml/metadata exposes the public cert
// so the IdP admin can register us.
//
// EmailAttribute / UsernameAttribute / GroupsAttribute name the
// SAML AttributeStatement keys carrying the user's identity. Most
// IdPs default to URN names like
// "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
// — leaving these empty falls back to the SAML Subject NameID for
// email and the email-prefix for username.
type SAMLConfig struct {
	Enabled            bool   `json:"enabled"`
	DisplayName        string `json:"display_name,omitempty"` // shown on the "Sign in with X" button
	IdPMetadataURL     string `json:"idp_metadata_url"`
	EntityID           string `json:"entity_id,omitempty"` // SP entity ID; defaults to baseURL+/api/v1/auth/saml/metadata
	SPCertificatePEM   string `json:"sp_certificate_pem,omitempty"`
	SPPrivateKeyPEM    string `json:"sp_private_key_pem,omitempty"`
	EmailAttribute     string `json:"email_attribute,omitempty"`
	UsernameAttribute  string `json:"username_attribute,omitempty"`
	GroupsAttribute    string `json:"groups_attribute,omitempty"`
	AdminGroup         string `json:"admin_group,omitempty"`
}

// SAML returns the stored SAML config or the zero value if not persisted.
func (s *Service) SAML(ctx context.Context) SAMLConfig {
	raw := s.get(ctx, keySAMLConfig)
	if raw == "" {
		return SAMLConfig{}
	}
	var cfg SAMLConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse saml_config", "err", err)
		return SAMLConfig{}
	}
	return cfg
}

// SetSAML persists the SAML config as JSON.
func (s *Service) SetSAML(ctx context.Context, cfg SAMLConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keySAMLConfig, string(b))
}

// LDAPConfig holds the configuration for an LDAP/Active Directory login flow.
//
// BindDN/BindPassword are the service-account credentials used to bind for
// the user search. UserSearchBase + UserFilter locate the user record; the
// "{username}" placeholder in UserFilter is replaced with the form value
// (LDAP-escaped). Once located, the handler bind-as-user with the supplied
// password to authenticate.
//
// AdminGroupDN: when set, users that are members of this group (group's
// "member" or "uniqueMember" attribute contains the user's DN) are
// auto-promoted to admin on each login.
type LDAPConfig struct {
	Enabled        bool   `json:"enabled"`
	DisplayName    string `json:"display_name,omitempty"` // e.g. "Company SSO"
	Host           string `json:"host"`                   // host:port, e.g. "ldap.example.com:636"
	StartTLS       bool   `json:"start_tls"`              // upgrade plain LDAP to TLS
	UseLDAPS       bool   `json:"use_ldaps"`              // use ldaps:// (implicit TLS)
	SkipTLSVerify  bool   `json:"skip_tls_verify"`        // dev/self-signed; do not enable in prod
	BindDN         string `json:"bind_dn"`
	BindPassword   string `json:"bind_password,omitempty"`
	UserSearchBase string `json:"user_search_base"` // e.g. "ou=people,dc=example,dc=com"
	UserFilter     string `json:"user_filter"`      // e.g. "(uid={username})" or "(sAMAccountName={username})"
	UsernameAttr   string `json:"username_attr,omitempty"`
	EmailAttr      string `json:"email_attr,omitempty"`
	AdminGroupDN   string `json:"admin_group_dn,omitempty"`
}

// LDAP returns the stored LDAP config or the zero value if not persisted.
func (s *Service) LDAP(ctx context.Context) LDAPConfig {
	raw := s.get(ctx, keyLDAPConfig)
	if raw == "" {
		return LDAPConfig{}
	}
	var cfg LDAPConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse ldap_config", "err", err)
		return LDAPConfig{}
	}
	return cfg
}

// SetLDAP persists the LDAP config as JSON.
func (s *Service) SetLDAP(ctx context.Context, cfg LDAPConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyLDAPConfig, string(b))
}

// SMTPConfig holds the SMTP credentials used to send password-reset and
// invite emails. Disabled or incomplete configs are treated as "email off"
// — the API exposes the disabled state to the UI so admins know which
// flows (password reset, invites) won't work yet.
type SMTPConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	From     string `json:"from"` // e.g. "OnScreen <noreply@example.com>"
}

// SMTP returns the stored SMTP config or the zero value if not persisted.
func (s *Service) SMTP(ctx context.Context) SMTPConfig {
	raw := s.get(ctx, keySMTPConfig)
	if raw == "" {
		return SMTPConfig{}
	}
	var cfg SMTPConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse smtp_config", "err", err)
		return SMTPConfig{}
	}
	return cfg
}

// SetSMTP persists the SMTP config as JSON.
func (s *Service) SetSMTP(ctx context.Context, cfg SMTPConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keySMTPConfig, string(b))
}

// OTelConfig holds the OpenTelemetry OTLP/gRPC tracing configuration.
// Tracing is wired at process startup, so changes here require a restart
// before they take effect — the API surface flags this in its restart_required
// hint.
//
// Endpoint must include a scheme (http:// or https://); TLS is enabled
// automatically for https. SampleRatio is in [0,1]; values outside that range
// are clamped at startup. DeploymentEnv is surfaced as the
// deployment.environment resource attribute on every span.
type OTelConfig struct {
	Enabled       bool    `json:"enabled"`
	Endpoint      string  `json:"endpoint"`
	SampleRatio   float64 `json:"sample_ratio"`
	DeploymentEnv string  `json:"deployment_env,omitempty"`
}

// OTel returns the stored OTel config or the zero value if not persisted.
func (s *Service) OTel(ctx context.Context) OTelConfig {
	raw := s.get(ctx, keyOTelConfig)
	if raw == "" {
		return OTelConfig{}
	}
	var cfg OTelConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse otel_config", "err", err)
		return OTelConfig{}
	}
	return cfg
}

// SetOTel persists the OTel config as JSON.
func (s *Service) SetOTel(ctx context.Context, cfg OTelConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyOTelConfig, string(b))
}

// GeneralConfig groups the general server settings that used to live in
// optional environment variables. All three fields are read-once at startup
// (the API surface flags this as restart-required), with the exception of
// BaseURL which is consumed per-request and could be made dynamic later
// without a schema change.
//
// BaseURL is the public URL of the server (used in OAuth/OIDC redirects,
// password-reset emails, and capability discovery). Empty falls back to the
// process-computed default of "http://localhost" + LISTEN_ADDR at startup.
//
// LogLevel maps to slog: debug | info | warn | error. Empty defaults to info.
//
// CORSAllowedOrigins is a list of origins permitted for cross-origin XHR.
// Use ["*"] to allow any origin — safe because the API authenticates via
// Authorization: Bearer headers, not cookies. Empty disables CORS entirely.
type GeneralConfig struct {
	BaseURL            string   `json:"base_url,omitempty"`
	LogLevel           string   `json:"log_level,omitempty"`
	CORSAllowedOrigins []string `json:"cors_allowed_origins,omitempty"`
}

// General returns the stored general server config or the zero value.
func (s *Service) General(ctx context.Context) GeneralConfig {
	raw := s.get(ctx, keyGeneralConfig)
	if raw == "" {
		return GeneralConfig{}
	}
	var cfg GeneralConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		s.logger.ErrorContext(ctx, "parse general_config", "err", err)
		return GeneralConfig{}
	}
	return cfg
}

// SetGeneral persists the general server config as JSON.
func (s *Service) SetGeneral(ctx context.Context, cfg GeneralConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.set(ctx, keyGeneralConfig, string(b))
}

func (s *Service) get(ctx context.Context, key string) string {
	var val string
	err := s.db.QueryRow(ctx,
		`SELECT value FROM server_settings WHERE key = $1`, key,
	).Scan(&val)
	if err != nil {
		// pgx.ErrNoRows is the legitimate "key not set yet" case and
		// callers handle the empty return. Anything else (pool exhausted,
		// transient connection drop, query timeout) was previously
		// swallowed silently — the agent factory then cached `nil` for
		// hours of "secret unset" while the row was actually present.
		// Surface it so the same hang can't happen invisibly again.
		if !errors.Is(err, pgx.ErrNoRows) {
			s.logger.ErrorContext(ctx, "settings get: db query failed",
				"key", key, "err", err)
		}
		return ""
	}
	// Encrypted-at-rest path: anything with the encv1: sentinel passes
	// through Decrypt regardless of allowlist, so removing a key from
	// secretKeys later doesn't strand previously-encrypted rows. Decrypt
	// failure (wrong key, corrupted ciphertext) returns "" + a logged
	// error rather than handing the cipher back to the caller.
	if strings.HasPrefix(val, encPrefix) {
		if s.enc == nil {
			s.logger.ErrorContext(ctx, "settings get: encrypted value but no encryptor wired",
				"key", key)
			return ""
		}
		plain, err := s.enc.Decrypt(strings.TrimPrefix(val, encPrefix))
		if err != nil {
			s.logger.ErrorContext(ctx, "settings get: decrypt failed",
				"key", key, "err", err)
			return ""
		}
		return plain
	}
	// Legacy plaintext on a secret-bearing key: keep working, but flag
	// it so an operator who re-saves the setting migrates the row to
	// encrypted form.
	if s.isSecretKey(key) && val != "" && s.enc != nil {
		s.logger.WarnContext(ctx, "settings get: legacy plaintext for secret key; re-save in admin UI to encrypt",
			"key", key)
	}
	return val
}

func (s *Service) set(ctx context.Context, key, value string) error {
	stored := value
	// Encrypt at write time for secret-bearing keys when an Encryptor is
	// wired. Empty value → store empty (lets the admin clear a secret
	// without leaving a ciphertext blob).
	if value != "" && s.isSecretKey(key) && s.enc != nil {
		ct, err := s.enc.Encrypt(value)
		if err != nil {
			s.logger.ErrorContext(ctx, "settings set: encrypt failed",
				"key", key, "err", err)
			return err
		}
		stored = encPrefix + ct
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO server_settings (key, value, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_at = NOW()
	`, key, stored)
	if err != nil {
		s.logger.ErrorContext(ctx, "settings set", "key", key, "err", err)
	}
	return err
}
