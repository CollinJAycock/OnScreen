package v1

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml/samlsp"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/testvalkey"
)

// startSAMLFlow runs TrackRequest the way /auth/saml does and returns the
// RelayState plus the binding cookie the starting browser received.
func startSAMLFlow(t *testing.T, tr samlsp.RequestTracker, target string, jar ...*http.Cookie) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, c := range jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	idx, err := tr.TrackRequest(rec, req, "id-abc123")
	if err != nil {
		t.Fatalf("TrackRequest: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == samlBindCookieName {
			return idx, c
		}
	}
	t.Fatal("TrackRequest did not set the binding cookie")
	return "", nil
}

func acsRequest(target, relay string, cookie *http.Cookie) *http.Request {
	form := url.Values{"RelayState": {relay}, "SAMLResponse": {"x"}}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	return req
}

const (
	httpsStart = "https://onscreen.example/api/v1/auth/saml"
	httpsACS   = "https://onscreen.example/api/v1/auth/saml/acs"
	httpStart  = "http://onscreen.lan/api/v1/auth/saml"
	httpACS    = "http://onscreen.lan/api/v1/auth/saml/acs"
)

func TestSAMLBinding_CookieAttributes(t *testing.T) {
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), nil)

	_, c := startSAMLFlow(t, tr, httpsStart)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteNoneMode {
		t.Errorf("HTTPS cookie must be HttpOnly+Secure+SameSite=None (reaches the IdP's cross-site POST); got %+v", c)
	}
	if c.Path != samlBindCookiePath || c.MaxAge <= 0 || c.MaxAge > 600 {
		t.Errorf("cookie path/max-age = %q/%d", c.Path, c.MaxAge)
	}

	_, c = startSAMLFlow(t, tr, httpStart)
	if c.Secure || c.SameSite == http.SameSiteNoneMode {
		t.Errorf("plain-HTTP cookie can't be SameSite=None/Secure; got %+v", c)
	}
}

func TestSAMLBinding_MatchingCookieResolves(t *testing.T) {
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), nil)
	idx, c := startSAMLFlow(t, tr, httpsStart)

	got, err := tr.GetTrackedRequest(acsRequest(httpsACS, idx, c), idx)
	if err != nil {
		t.Fatalf("same browser: %v", err)
	}
	if got.SAMLRequestID != "id-abc123" {
		t.Errorf("SAMLRequestID = %q, want the bare request ID (binding digest stripped)", got.SAMLRequestID)
	}
}

// Login CSRF: the attacker's flow completed in a victim's browser.
func TestSAMLBinding_ForeignBrowserRejected(t *testing.T) {
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), nil)
	idx, _ := startSAMLFlow(t, tr, httpsStart)          // attacker's browser
	_, victimCookie := startSAMLFlow(t, tr, httpsStart) // victim's own, unrelated flow

	if _, err := tr.GetTrackedRequest(acsRequest(httpsACS, idx, nil), idx); !errors.Is(err, errSAMLBindMissing) {
		t.Errorf("HTTPS, no cookie: got %v, want errSAMLBindMissing (fail closed)", err)
	}
	if _, err := tr.GetTrackedRequest(acsRequest(httpsACS, idx, victimCookie), idx); !errors.Is(err, errSAMLBindMismatch) {
		t.Errorf("HTTPS, other flow's cookie: got %v, want errSAMLBindMismatch", err)
	}
	forged := &http.Cookie{Name: samlBindCookieName, Value: "not-the-right-value"}
	if _, err := tr.GetTrackedRequest(acsRequest(httpACS, idx, forged), idx); !errors.Is(err, errSAMLBindMismatch) {
		t.Errorf("plain HTTP, present-but-wrong cookie: got %v, want errSAMLBindMismatch", err)
	}
}

// Plain HTTP can't carry a SameSite=None cookie across the IdP's POST, so a
// MISSING cookie there is tolerated (with a warning), never a wrong one.
func TestSAMLBinding_PlainHTTPMissingCookieTolerated(t *testing.T) {
	var logs bytes.Buffer
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(),
		slog.New(slog.NewTextHandler(&logs, nil)))
	idx, _ := startSAMLFlow(t, tr, httpStart)
	got, err := tr.GetTrackedRequest(acsRequest(httpACS, idx, nil), idx)
	if err != nil || got.SAMLRequestID != "id-abc123" {
		t.Fatalf("plain HTTP without cookie: got (%v, %v), want accepted", got, err)
	}
	if !strings.Contains(logs.String(), "without browser binding") {
		t.Error("accepting an unbound plain-HTTP sign-in must log a warning")
	}
}

func TestSAMLBinding_StopClearsCookie(t *testing.T) {
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), nil)
	idx, c := startSAMLFlow(t, tr, httpsStart)
	rec := httptest.NewRecorder()
	if err := tr.StopTrackingRequest(rec, acsRequest(httpsACS, idx, c), idx); err != nil {
		t.Fatal(err)
	}
	var cleared bool
	for _, rc := range rec.Result().Cookies() {
		if rc.Name == samlBindCookieName && rc.MaxAge < 0 && rc.Path == samlBindCookiePath &&
			rc.Secure && rc.SameSite == http.SameSiteNoneMode {
			cleared = true
		}
	}
	if !cleared {
		t.Error("StopTrackingRequest must expire the binding cookie with matching attributes")
	}
	if _, err := tr.GetTrackedRequest(acsRequest(httpsACS, idx, c), idx); err == nil {
		t.Error("stopped request must not resolve (single use)")
	}
}

// Two sign-ins started in parallel tabs of one browser both stay valid.
func TestSAMLBinding_ParallelTabsShareCookie(t *testing.T) {
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), nil)
	idx1, c1 := startSAMLFlow(t, tr, httpsStart)
	idx2, c2 := startSAMLFlow(t, tr, httpsStart, &http.Cookie{Name: c1.Name, Value: c1.Value})
	if c1.Value != c2.Value {
		t.Fatal("a browser that already holds a binding cookie should keep it")
	}
	for _, idx := range []string{idx1, idx2} {
		if _, err := tr.GetTrackedRequest(acsRequest(httpsACS, idx, c2), idx); err != nil {
			t.Errorf("flow %s: %v", idx, err)
		}
	}
}

// The binding digest must survive the Valkey tracker's JSON round-trip (HA).
func TestSAMLBinding_OverValkeyTracker(t *testing.T) {
	v := testvalkey.New(t)
	a := newBindingSAMLRequestTracker(NewValkeySAMLRequestTracker(v), nil)
	b := newBindingSAMLRequestTracker(NewValkeySAMLRequestTracker(v), nil)
	idx, c := startSAMLFlow(t, a, httpsStart)
	if got, err := b.GetTrackedRequest(acsRequest(httpsACS, idx, c), idx); err != nil || got.SAMLRequestID != "id-abc123" {
		t.Fatalf("cross-instance, same browser: (%v, %v)", got, err)
	}
	if _, err := b.GetTrackedRequest(acsRequest(httpsACS, idx, nil), idx); !errors.Is(err, errSAMLBindMissing) {
		t.Fatalf("cross-instance, foreign browser: got %v, want errSAMLBindMissing", err)
	}
}

type bindTestSAMLSettings struct{ cfg settings.SAMLConfig }

func (s bindTestSAMLSettings) SAML(context.Context) settings.SAMLConfig { return s.cfg }

// ACS refuses the foreign browser before the assertion is even parsed.
func TestSAMLACS_RefusesSignInFromForeignBrowser(t *testing.T) {
	cfg := settings.SAMLConfig{Enabled: true, IdPMetadataURL: "https://idp.example/metadata"}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := NewSAMLHandler(bindTestSAMLSettings{cfg}, nil, "https://onscreen.example", logger)
	tr := newBindingSAMLRequestTracker(newMemorySAMLRequestTracker(), logger)
	h.mw = &samlsp.Middleware{RequestTracker: tr}
	h.cached = cfg

	idx, _ := startSAMLFlow(t, tr, httpsStart) // attacker's browser starts it
	rec := httptest.NewRecorder()
	h.ACS(rec, acsRequest(httpsACS, idx, nil)) // victim's browser completes it

	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "SAML_INVALID_ASSERTION") {
		t.Fatalf("got %d %s, want 401 SAML_INVALID_ASSERTION", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "sign-in not started by this browser") {
		t.Error("ACS must refuse on the binding check, before parsing the assertion")
	}
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "onscreen_") && c.Name != samlBindCookieName {
			t.Errorf("no session cookie may be set on a refused sign-in, got %s", c.Name)
		}
	}
}
