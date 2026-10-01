package v1

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"
	"github.com/google/uuid"
	dsig "github.com/russellhaering/goxmldsig"
	"golang.org/x/oauth2"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// The OIDC / SAML services audit an IdP-driven session wipe only if the
// sign-in handler hands them withSSOAudit's context value (see
// syncAdminFromIdP). These drive each handler through a real, verified sign-in
// — a fake IdP signs the ID token / assertion — into a stub service that
// records what context it was given, so dropping the withSSOAudit wiring from
// either handler fails here. (LDAP's wiring is pinned end to end by
// TestLDAPLogin_AdminSyncIsAudited.)

// ssoCtxProbe records the ssoAudit value the service was called with.
type ssoCtxProbe struct {
	called bool
	got    ssoAudit
	ok     bool
}

func (p *ssoCtxProbe) observe(ctx context.Context) *TokenPair {
	p.called = true
	p.got, p.ok = ctx.Value(ssoAuditKey{}).(ssoAudit)
	return &TokenPair{AccessToken: "at", RefreshToken: "rt", AssetToken: "as",
		UserID: uuid.New(), Username: "eve", ExpiresAt: time.Now().Add(time.Hour)}
}

func (p *ssoCtxProbe) check(t *testing.T, want *audit.Logger, wantIP string) {
	t.Helper()
	if !p.called {
		t.Fatal("the sign-in never reached the service")
	}
	if !p.ok {
		t.Fatal("service context carries no ssoAudit — an IdP-driven session wipe would go unaudited")
	}
	if p.got.log != want {
		t.Error("ssoAudit carries a different audit logger than the handler's")
	}
	if p.got.ip != wantIP {
		t.Errorf("ssoAudit ip: got %q, want %q", p.got.ip, wantIP)
	}
}

type probeOIDCService struct{ ssoCtxProbe }

func (s *probeOIDCService) LoginOrCreateOIDCUser(ctx context.Context, _ OIDCProfile) (*TokenPair, error) {
	return s.observe(ctx), nil
}

type probeSAMLService struct{ ssoCtxProbe }

func (s *probeSAMLService) LoginOrCreateSAMLUser(ctx context.Context, _ SAMLProfile) (*TokenPair, error) {
	return s.observe(ctx), nil
}

func newProbeAuditLogger() *audit.Logger {
	// Buffered: the handler's own login_success entry lands here too.
	return audit.New(&auditCapture{ch: make(chan gen.InsertAuditLogParams, 8)}, slog.Default())
}

// ── OIDC ─────────────────────────────────────────────────────────────────────

// signRS256 returns a compact RS256 JWS over claims.
func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := enc.EncodeToString(hdr) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + enc.EncodeToString(sig)
}

// newFakeOIDCIdP serves discovery, JWKS and a token endpoint whose ID token is
// signed by the published key and carries nonce. Loopback, which oidcCtx's
// safehttp policy allows (self-hosted IdPs run there).
func newFakeOIDCIdP(t *testing.T, clientID, nonce string) *httptest.Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)
	// The issuer (= the server's URL) goes into the discovery doc and the
	// token, so fix it before Start.
	issuer := "http://" + srv.Listener.Addr().String()
	now := time.Now()
	idToken := signRS256(t, key, "k1", map[string]any{
		"iss": issuer, "sub": "eve-subject", "aud": clientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"nonce": nonce, "preferred_username": "eve",
	})
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"jwks_uri":                              issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "k1",
			"n": enc.EncodeToString(key.N.Bytes()),
			"e": enc.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"access_token": "idp-at", "token_type": "Bearer", "expires_in": 300,
			"id_token": idToken,
		})
	})
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestOIDCCallback_HandsAuditContextToService(t *testing.T) {
	flow := oidcFlowState{State: "st-1", Nonce: "nonce-1", CodeVerifier: oauth2.GenerateVerifier()}
	idp := newFakeOIDCIdP(t, "onscreen", flow.Nonce)
	cfg := settings.OIDCConfig{Enabled: true, IssuerURL: idp.URL, ClientID: "onscreen", ClientSecret: "shh"}
	svc := &probeOIDCService{}
	al := newProbeAuditLogger()
	h := NewOIDCHandler(&fakeOIDCSettings{cfg: cfg}, svc, "https://onscreen.example", slog.Default()).WithAudit(al)

	cookie, err := encodeOIDCFlow(flow)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet,
		"https://onscreen.example/api/v1/auth/oidc/callback?state=st-1&code=auth-code", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: cookie})
	req.RemoteAddr = "203.0.113.5:40000"
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/?oidc_auth=1" {
		t.Fatalf("callback: got %d → %q, want 307 → /?oidc_auth=1", rec.Code, rec.Header().Get("Location"))
	}
	svc.check(t, al, "203.0.113.5")
}

// ── SAML ─────────────────────────────────────────────────────────────────────

func parseRSAKeyPair(t *testing.T, certPEM, keyPEM string) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	kp, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, ok := kp.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("key is %T, want RSA", kp.PrivateKey)
	}
	return key, cert
}

// signedSAMLResponse is the IdP's POST-binding answer to requestID: a signed
// response around a signed (unencrypted) assertion for sp's ACS.
func signedSAMLResponse(t *testing.T, idp *saml.IdentityProvider, sp *saml.ServiceProvider, requestID string) string {
	t.Helper()
	now := saml.TimeNow()
	req := &saml.IdpAuthnRequest{
		IDP:                     idp,
		HTTPRequest:             httptest.NewRequest(http.MethodPost, idp.SSOURL.String(), nil),
		Request:                 saml.AuthnRequest{ID: requestID, IssueInstant: now},
		ServiceProviderMetadata: sp.Metadata(),
		SPSSODescriptor:         &saml.SPSSODescriptor{}, // no encryption key → plain signed assertion
		ACSEndpoint:             &saml.IndexedEndpoint{Binding: saml.HTTPPostBinding, Location: sp.AcsURL.String()},
		Now:                     now,
	}
	session := &saml.Session{NameID: "eve-subject", UserName: "eve", UserEmail: "eve@idp.example", CreateTime: now}
	if err := (saml.DefaultAssertionMaker{}).MakeAssertion(req, session); err != nil {
		t.Fatalf("make assertion: %v", err)
	}
	form, err := req.PostBinding()
	if err != nil {
		t.Fatalf("sign response: %v", err)
	}
	return form.SAMLResponse
}

func TestSAMLACS_HandsAuditContextToService(t *testing.T) {
	idpCertPEM, idpKeyPEM, err := GenerateSPKeyPair("test-idp")
	if err != nil {
		t.Fatal(err)
	}
	idpKey, idpCert := parseRSAKeyPair(t, idpCertPEM, idpKeyPEM)
	idp := &saml.IdentityProvider{
		Key: idpKey, Certificate: idpCert,
		MetadataURL:     url.URL{Scheme: "https", Host: "idp.example", Path: "/metadata"},
		SSOURL:          url.URL{Scheme: "https", Host: "idp.example", Path: "/sso"},
		SignatureMethod: dsig.RSASHA256SignatureMethod,
	}
	meta, err := xml.Marshal(idp.Metadata())
	if err != nil {
		t.Fatal(err)
	}
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(meta)
	}))
	t.Cleanup(metaSrv.Close)

	spCertPEM, spKeyPEM, err := GenerateSPKeyPair("onscreen")
	if err != nil {
		t.Fatal(err)
	}
	cfg := settings.SAMLConfig{Enabled: true, IdPMetadataURL: metaSrv.URL,
		SPCertificatePEM: spCertPEM, SPPrivateKeyPEM: spKeyPEM}
	svc := &probeSAMLService{}
	al := newProbeAuditLogger()
	h := NewSAMLHandler(bindTestSAMLSettings{cfg}, svc, "https://onscreen.example", slog.Default()).WithAudit(al)

	// The browser starts the sign-in (what GET /auth/saml does)…
	mw, err := h.middleware(context.Background())
	if err != nil {
		t.Fatalf("build SAML middleware: %v", err)
	}
	relay, bind := startSAMLFlow(t, mw.RequestTracker, httpsStart)
	// …and the IdP posts back a signed answer to that request.
	form := url.Values{
		"RelayState":   {relay},
		"SAMLResponse": {signedSAMLResponse(t, idp, &mw.ServiceProvider, "id-abc123")},
	}
	req := httptest.NewRequest(http.MethodPost, httpsACS, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: bind.Name, Value: bind.Value})
	req.RemoteAddr = "203.0.113.5:40000"
	rec := httptest.NewRecorder()
	h.ACS(rec, req)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?saml_auth=1" {
		t.Fatalf("acs: got %d → %q (%s), want 302 → /?saml_auth=1",
			rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	svc.check(t, al, "203.0.113.5")
}
