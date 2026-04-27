package v1

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/jackc/pgx/v5"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// SAMLSettingsReader is the slice of settings.Service the SAML handler
// uses, kept narrow so tests can stub it without importing the full
// service.
type SAMLSettingsReader interface {
	SAML(ctx context.Context) settings.SAMLConfig
}

// SAMLAuthService logs in or provisions a user from a verified SAML
// assertion. Mirrors OIDCAuthService — the two protocols differ in
// transport but both ultimately resolve to "given a verified
// (issuer, subject, email) tuple, hand back a TokenPair."
type SAMLAuthService interface {
	LoginOrCreateSAMLUser(ctx context.Context, profile SAMLProfile) (*TokenPair, error)
}

// SAMLProfile is the subset of assertion attributes the auth service
// needs. Issuer is the IdP's entity ID; Subject is the NameID.
type SAMLProfile struct {
	Issuer    string
	Subject   string
	Email     string
	Username  string
	IsAdmin   bool
	GroupSync bool // whether AdminGroup was configured
}

// SAMLOAuthDB is the DB subset for SAML user provisioning.
type SAMLOAuthDB interface {
	GetUserBySAMLSubject(ctx context.Context, arg gen.GetUserBySAMLSubjectParams) (gen.User, error)
	GetUserByEmail(ctx context.Context, email *string) (gen.User, error)
	LinkSAMLAccount(ctx context.Context, arg gen.LinkSAMLAccountParams) error
	CreateSAMLUser(ctx context.Context, arg gen.CreateSAMLUserParams) (gen.User, error)
	CountUsers(ctx context.Context) (int64, error)
	SetUserAdmin(ctx context.Context, arg gen.SetUserAdminParams) error
}

// ── handler ─────────────────────────────────────────────────────────────────

// SAMLHandler exposes the SAML 2.0 SP endpoints. The IdP config lives
// in DB settings — the handler lazy-loads it on each request and
// rebuilds its samlsp.Middleware when the metadata URL or SP cert
// changes (no restart needed when an admin enables the IdP).
type SAMLHandler struct {
	cfgSrc  SAMLSettingsReader
	svc     SAMLAuthService
	baseURL string
	logger  *slog.Logger

	mu        sync.RWMutex
	cached    settings.SAMLConfig // last-built config (cache key)
	mw        *samlsp.Middleware  // lazily built per-config-change
	idPMeta   *saml.EntityDescriptor
}

// NewSAMLHandler creates a SAMLHandler. baseURL is the public URL used
// to construct the ACS + metadata endpoints (e.g. "https://onscreen.example.com").
func NewSAMLHandler(cfgSrc SAMLSettingsReader, svc SAMLAuthService, baseURL string, logger *slog.Logger) *SAMLHandler {
	return &SAMLHandler{cfgSrc: cfgSrc, svc: svc, baseURL: baseURL, logger: logger}
}

// Enabled returns whether SAML SSO is configured. UI uses this to
// decide whether to show the "Sign in with SSO" button.
//
// GET /api/v1/auth/saml/enabled
func (h *SAMLHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfgSrc.SAML(r.Context())
	respond.Success(w, r, map[string]any{
		"enabled":      cfg.Enabled && cfg.IdPMetadataURL != "",
		"display_name": cfg.DisplayName,
	})
}

// Login starts an SP-initiated SAML SSO flow. The samlsp middleware
// builds the AuthnRequest, signs it with the SP private key, and
// 302-redirects the browser to the IdP's SSO URL.
//
// GET /api/v1/auth/saml
func (h *SAMLHandler) Login(w http.ResponseWriter, r *http.Request) {
	mw, err := h.middleware(r.Context())
	if err != nil {
		h.logger.WarnContext(r.Context(), "saml login: middleware build", "err", err)
		respond.Error(w, r, http.StatusServiceUnavailable, "SAML_UNCONFIGURED",
			"SAML SSO is not configured on this server.")
		return
	}
	mw.HandleStartAuthFlow(w, r)
}

// ACS receives the IdP's POST-back with the signed SAMLResponse,
// parses + verifies the assertion, derives a SAMLProfile, and hands
// off to the auth service to mint OnScreen session tokens.
//
// POST /api/v1/auth/saml/acs
func (h *SAMLHandler) ACS(w http.ResponseWriter, r *http.Request) {
	mw, err := h.middleware(r.Context())
	if err != nil {
		h.logger.WarnContext(r.Context(), "saml acs: middleware build", "err", err)
		respond.Error(w, r, http.StatusServiceUnavailable, "SAML_UNCONFIGURED",
			"SAML SSO is not configured on this server.")
		return
	}
	if err := r.ParseForm(); err != nil {
		respond.BadRequest(w, r, "could not parse SAML response")
		return
	}
	// crewjam/saml's ServiceProvider.ParseResponse verifies the IdP
	// signature, the audience, the timestamps, and the destination.
	// Returning a non-nil err means the assertion is untrustworthy
	// (replay, tampered, expired) and we MUST refuse the login.
	//
	// Look up the request ID via the RequestTracker keyed by RelayState
	// (echoed back by the IdP in the form POST). Without this the
	// possibleRequestIDs list is empty and ParseResponse rejects every
	// SP-init response with "InResponseTo does not match expected []".
	var possibleIDs []string
	if relay := r.Form.Get("RelayState"); relay != "" {
		if tr, terr := mw.RequestTracker.GetTrackedRequest(r, relay); terr == nil && tr != nil {
			possibleIDs = []string{tr.SAMLRequestID}
		}
	}
	assertion, err := mw.ServiceProvider.ParseResponse(r, possibleIDs)
	if err != nil {
		// *InvalidResponseError.Error() returns "Authentication failed"
		// by design — it deliberately masks the real reason to avoid
		// leaking attacker-useful info via user-facing channels. Log
		// the PrivateErr (and the raw response when available) server-
		// side so an admin can actually diagnose mismatch IdPs without
		// flying blind.
		var detail any = err
		var ire *saml.InvalidResponseError
		if errors.As(err, &ire) {
			detail = ire.PrivateErr
			h.logger.WarnContext(r.Context(), "saml acs: parse response",
				"err", detail,
				"response_xml", ire.Response,
				"now", ire.Now,
			)
		} else {
			h.logger.WarnContext(r.Context(), "saml acs: parse response", "err", err)
		}
		respond.Error(w, r, http.StatusUnauthorized, "SAML_INVALID_ASSERTION",
			"The SAML assertion could not be verified.")
		return
	}

	// Stop tracking the request — single-use prevents replay of a
	// captured assertion against the same RelayState. crewjam/saml's
	// own ServeACS does this; we have to mirror it because we call
	// ParseResponse directly.
	if relay := r.Form.Get("RelayState"); relay != "" {
		_ = mw.RequestTracker.StopTrackingRequest(w, r, relay)
	}

	cfg := h.cfgSrc.SAML(r.Context())
	profile := buildSAMLProfile(assertion, cfg)
	if profile.Subject == "" {
		respond.Error(w, r, http.StatusUnauthorized, "SAML_NO_SUBJECT",
			"The SAML assertion did not include a NameID.")
		return
	}

	tokens, err := h.svc.LoginOrCreateSAMLUser(r.Context(), profile)
	if err != nil {
		if errors.Is(err, ErrSSOAccountConflict) {
			respond.Error(w, r, http.StatusConflict, "SSO_ACCOUNT_CONFLICT",
				"An OnScreen account with this email already exists with another credential. Sign in with that credential and link SAML from your profile.")
			return
		}
		h.logger.ErrorContext(r.Context(), "saml acs: login", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Mirror OIDC: set both the access + refresh cookies via the shared
	// helper so the cookie names (onscreen_at / onscreen_rt), path scoping,
	// SameSite mode, and Secure-flag derivation match every other auth
	// path. The earlier hand-rolled cookie used the wrong name and the
	// auth middleware never saw it — SAML logins appeared to succeed but
	// every subsequent request came back unauthenticated. The hand-rolled
	// version also discarded the refresh token, so even with the correct
	// name a SAML user would be evicted after the 1h access TTL.
	setAuthCookies(w, r, tokens)
	// Marker query param so the SPA's layout knows to bootstrap user info
	// from /api/v1/auth/refresh on first load — without it, the auth gate
	// at every page checks localStorage.onscreen_user, finds nothing
	// (cookies are httpOnly), and bounces to /login.
	http.Redirect(w, r, "/?saml_auth=1", http.StatusFound)
}

// Metadata serves the SP metadata XML the IdP admin uses to register
// OnScreen as a SAML SP. Generated from the cached middleware so the
// SP cert + ACS URL are always in sync with what's actually wired.
//
// GET /api/v1/auth/saml/metadata
func (h *SAMLHandler) Metadata(w http.ResponseWriter, r *http.Request) {
	mw, err := h.middleware(r.Context())
	if err != nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "SAML_UNCONFIGURED",
			"SAML SSO is not configured on this server.")
		return
	}
	mw.ServeMetadata(w, r)
}

// ── Middleware build ────────────────────────────────────────────────────────

// middleware returns the cached samlsp.Middleware, rebuilding it when
// the persisted config has changed since the last build. The cache
// key is the full SAMLConfig — any field change forces a rebuild,
// which fetches fresh IdP metadata and re-parses the SP cert.
func (h *SAMLHandler) middleware(ctx context.Context) (*samlsp.Middleware, error) {
	cfg := h.cfgSrc.SAML(ctx)
	if !cfg.Enabled || cfg.IdPMetadataURL == "" {
		return nil, errors.New("saml: not configured")
	}

	h.mu.RLock()
	if cfg == h.cached && h.mw != nil {
		mw := h.mw
		h.mu.RUnlock()
		return mw, nil
	}
	h.mu.RUnlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	// Re-check after upgrading the lock — another caller may have
	// rebuilt while we were waiting.
	if cfg == h.cached && h.mw != nil {
		return h.mw, nil
	}

	mw, idpMeta, err := buildSAMLMiddleware(ctx, cfg, h.baseURL)
	if err != nil {
		return nil, err
	}
	h.mw = mw
	h.cached = cfg
	h.idPMeta = idpMeta
	return mw, nil
}

// buildSAMLMiddleware constructs a samlsp.Middleware from a SAMLConfig
// and the SP base URL. Fetches IdP metadata, parses the SP cert/key,
// and wires the ACS endpoint at /api/v1/auth/saml/acs.
func buildSAMLMiddleware(ctx context.Context, cfg settings.SAMLConfig, baseURL string) (*samlsp.Middleware, *saml.EntityDescriptor, error) {
	if cfg.SPCertificatePEM == "" || cfg.SPPrivateKeyPEM == "" {
		return nil, nil, errors.New("saml: SP certificate + private key required (auto-generate via SetSAML)")
	}
	keyPair, err := tls.X509KeyPair([]byte(cfg.SPCertificatePEM), []byte(cfg.SPPrivateKeyPEM))
	if err != nil {
		return nil, nil, fmt.Errorf("saml: parse SP keypair: %w", err)
	}
	if len(keyPair.Certificate) == 0 {
		return nil, nil, errors.New("saml: SP keypair contained no certificate")
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("saml: parse SP leaf cert: %w", err)
	}
	keyPair.Leaf = leaf

	rootURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("saml: parse base URL: %w", err)
	}

	idpMeta, err := fetchIdPMetadata(ctx, cfg.IdPMetadataURL)
	if err != nil {
		return nil, nil, fmt.Errorf("saml: fetch IdP metadata: %w", err)
	}

	mw, err := samlsp.New(samlsp.Options{
		EntityID:    coalesceString(cfg.EntityID, baseURL+"/api/v1/auth/saml/metadata"),
		URL:         *rootURL,
		Key:         keyPair.PrivateKey.(*rsa.PrivateKey),
		Certificate: leaf,
		IDPMetadata: idpMeta,
		// Sign the AuthnRequest so IdPs that require it (Keycloak's
		// default "Client signature required = ON", Okta when configured
		// for "Sign AuthnRequest") accept the flow. Most IdPs reject
		// unsigned AuthnRequests with "Invalid Requester" when this
		// toggle is on; the SP keypair is auto-generated on first enable
		// so admins don't pay any setup cost. Redirect binding signs via
		// the Signature/SigAlg query params; POST binding embeds the
		// signature in the XML — crewjam/saml routes that automatically.
		SignRequest: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("saml: build middleware: %w", err)
	}
	// Override the middleware's default ACS path so it matches the
	// OnScreen API surface (/api/v1/auth/saml/acs).
	acsURL, _ := url.Parse(baseURL + "/api/v1/auth/saml/acs")
	mw.ServiceProvider.AcsURL = *acsURL
	metaURL, _ := url.Parse(baseURL + "/api/v1/auth/saml/metadata")
	mw.ServiceProvider.MetadataURL = *metaURL
	// Upgrade signature algorithm from samlsp's RSA-SHA1 default to
	// RSA-SHA256. Modern IdPs (Keycloak default, Okta, ADFS post-2019)
	// reject SHA-1 with "Invalid Requester" — and SHA-1 is broadly
	// deprecated for digital signatures regardless. crewjam/saml uses
	// the SP's SignatureMethod field for both binding-direct (POST
	// embedded) and redirect-binding (Signature/SigAlg query param)
	// signing.
	mw.ServiceProvider.SignatureMethod = dsig.RSASHA256SignatureMethod
	// Replace the default cookie-backed RequestTracker with an in-memory
	// one keyed by RelayState. The cookie tracker breaks on local
	// cross-port HTTP testing because Chromium drops SameSite=Lax cookies
	// on the IdP's cross-site POST-back. RelayState is echoed by every
	// IdP and survives the round-trip independent of cookies. See
	// auth_saml_tracker.go for the full reasoning.
	mw.RequestTracker = newMemorySAMLRequestTracker()
	return mw, idpMeta, nil
}

// fetchIdPMetadata GETs the IdP metadata XML and parses it into the
// crewjam EntityDescriptor that samlsp.New consumes. 30 s timeout —
// SAML setups are usually inside a corporate network where the IdP
// is fast, but a flaky link shouldn't hang the OnScreen process.
func fetchIdPMetadata(ctx context.Context, idpURL string) (*saml.EntityDescriptor, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, idpURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml,text/xml")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("idp metadata GET %s: status %d", idpURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MB cap
	if err != nil {
		return nil, fmt.Errorf("idp metadata read: %w", err)
	}
	return samlsp.ParseMetadata(body)
}

// buildSAMLProfile pulls the user attributes out of the assertion
// using the names configured in SAMLConfig (or sensible defaults).
func buildSAMLProfile(assertion *saml.Assertion, cfg settings.SAMLConfig) SAMLProfile {
	p := SAMLProfile{
		Issuer:  assertion.Issuer.Value,
		Subject: assertion.Subject.NameID.Value,
	}

	emailKey := coalesceString(cfg.EmailAttribute,
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress")
	usernameKey := coalesceString(cfg.UsernameAttribute,
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name")

	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			vals := samlAttrValues(attr)
			if len(vals) == 0 {
				continue
			}
			switch attr.Name {
			case emailKey, "email", "mail":
				if p.Email == "" {
					p.Email = vals[0]
				}
			case usernameKey, "username", "preferred_username", "uid":
				if p.Username == "" {
					p.Username = vals[0]
				}
			}
			if cfg.GroupsAttribute != "" && cfg.AdminGroup != "" && attr.Name == cfg.GroupsAttribute {
				p.GroupSync = true
				for _, g := range vals {
					if g == cfg.AdminGroup {
						p.IsAdmin = true
						break
					}
				}
			}
		}
	}

	// Fallbacks — Subject NameID is often the email when the IdP
	// doesn't emit attributes.
	if p.Email == "" && strings.Contains(p.Subject, "@") {
		p.Email = p.Subject
	}
	if p.Username == "" {
		if p.Email != "" {
			p.Username = strings.SplitN(p.Email, "@", 2)[0]
		} else {
			p.Username = p.Subject
		}
	}
	return p
}

func samlAttrValues(a saml.Attribute) []string {
	out := make([]string, 0, len(a.Values))
	for _, v := range a.Values {
		if v.Value != "" {
			out = append(out, v.Value)
		}
	}
	return out
}

func coalesceString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ── Auth service implementation ─────────────────────────────────────────────

type samlAuthService struct {
	db          SAMLOAuthDB
	issueTokens IssueTokenPairFn
	logger      *slog.Logger
}

// NewSAMLAuthService creates a SAMLAuthService backed by the given DB
// and token issuer.
func NewSAMLAuthService(db SAMLOAuthDB, issueTokens IssueTokenPairFn, logger *slog.Logger) SAMLAuthService {
	return &samlAuthService{db: db, issueTokens: issueTokens, logger: logger}
}

// LoginOrCreateSAMLUser implements the same three-step lookup as the
// OIDC flow: existing SAML link → email match into stub → JIT create.
// First-ever user becomes admin (parity with local registration), and
// when GroupSync is on the is_admin flag tracks the IdP group.
func (s *samlAuthService) LoginOrCreateSAMLUser(ctx context.Context, p SAMLProfile) (*TokenPair, error) {
	issuer, subject := p.Issuer, p.Subject

	// 1. Already linked.
	user, err := s.db.GetUserBySAMLSubject(ctx, gen.GetUserBySAMLSubjectParams{
		SamlIssuer: &issuer, SamlSubject: &subject,
	})
	if err == nil {
		s.maybeSyncAdmin(ctx, user, p)
		return s.issueTokens(ctx, user)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get user by saml: %w", err)
	}

	// 2. Email-based linking — only when present AND only into a stub
	// row (no password, no other SSO link). Trusting an email match
	// against a fully-credentialed account would let an IdP user
	// hijack it.
	if p.Email != "" {
		emailPtr := p.Email
		user, err = s.db.GetUserByEmail(ctx, &emailPtr)
		if err == nil {
			if !isStubUser(user) {
				return nil, ErrSSOAccountConflict
			}
			if linkErr := s.db.LinkSAMLAccount(ctx, gen.LinkSAMLAccountParams{
				ID: user.ID, SamlIssuer: &issuer, SamlSubject: &subject, Email: &emailPtr,
			}); linkErr != nil {
				s.logger.Warn("saml: link existing account", "user_id", user.ID, "err", linkErr)
			}
			s.maybeSyncAdmin(ctx, user, p)
			return s.issueTokens(ctx, user)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
	}

	// 3. JIT create. First-ever user is admin.
	count, err := s.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	isAdmin := p.IsAdmin || count == 0
	var emailPtr *string
	if p.Email != "" {
		e := p.Email
		emailPtr = &e
	}
	user, err = s.db.CreateSAMLUser(ctx, gen.CreateSAMLUserParams{
		Username:    p.Username,
		Email:       emailPtr,
		SamlIssuer:  &issuer,
		SamlSubject: &subject,
		IsAdmin:     isAdmin,
	})
	if err != nil {
		return nil, fmt.Errorf("create saml user: %w", err)
	}
	return s.issueTokens(ctx, user)
}

func (s *samlAuthService) maybeSyncAdmin(ctx context.Context, user gen.User, p SAMLProfile) {
	if !p.GroupSync || user.IsAdmin == p.IsAdmin {
		return
	}
	if err := s.db.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: user.ID, IsAdmin: p.IsAdmin}); err != nil {
		s.logger.Warn("saml: sync admin", "user_id", user.ID, "err", err)
	}
}

// ── SP keypair generation ───────────────────────────────────────────────────

// GenerateSPKeyPair creates a fresh RSA-2048 self-signed certificate
// + private key for the SP. Used by the admin endpoint when an
// operator first enables SAML — they don't want to learn openssl
// just to publish their SP metadata.
//
// Returns (certPEM, keyPEM) for storage in SAMLConfig. The cert
// validity is 10 years; rotating the SP cert is an explicit admin
// action, not an auto-renewal, because rotating means re-registering
// at the IdP.
func GenerateSPKeyPair(commonName string) (certPEM, keyPEM string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate rsa key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", fmt.Errorf("generate serial: %w", err)
	}
	tpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("create cert: %w", err)
	}
	certBuf := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("marshal key: %w", err)
	}
	keyBuf := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	return string(certBuf), string(keyBuf), nil
}
