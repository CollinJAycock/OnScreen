package v1

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// fakeIdP serves (or refuses) SAML IdP metadata and counts the fetches.
type fakeIdP struct {
	srv   *httptest.Server
	hits  atomic.Int32
	ok    atomic.Bool
	delay time.Duration
	meta  string
}

func newFakeIdP(t *testing.T, certPEM string) *fakeIdP {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("no PEM certificate")
	}
	f := &fakeIdP{delay: 100 * time.Millisecond}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		time.Sleep(f.delay)
		if !f.ok.Load() {
			http.Error(w, "idp down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, f.meta)
	}))
	f.meta = fmt.Sprintf(`<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s/metadata">
  <IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>%s</X509Certificate></X509Data></KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`, f.srv.URL, base64.StdEncoding.EncodeToString(block.Bytes), f.srv.URL)
	t.Cleanup(f.srv.Close)
	return f
}

// Item 10: while the SAML setup is broken, the anonymous SAML routes must not
// turn every hit into an IdP metadata fetch. Concurrent requests share one
// build; a failed build is reused for samlBuildFailureTTL; a working one is
// cached as before.
func TestSAMLMiddleware_FailedBuildCachedAndShared(t *testing.T) {
	certPEM, keyPEM, err := GenerateSPKeyPair("onscreen.test")
	if err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t, certPEM)
	cfg := settings.SAMLConfig{
		Enabled: true, IdPMetadataURL: idp.srv.URL + "/metadata",
		SPCertificatePEM: certPEM, SPPrivateKeyPEM: keyPEM,
	}
	h := NewSAMLHandler(bindTestSAMLSettings{cfg}, nil, "https://onscreen.example",
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	var clockMu sync.Mutex
	h.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	advance := func(d time.Duration) { clockMu.Lock(); now = now.Add(d); clockMu.Unlock() }

	// A burst against a failing IdP: one fetch, every caller gets the error.
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.middleware(context.Background()); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := idp.hits.Load(); got != 1 {
		t.Fatalf("burst of 8: %d metadata fetches, want 1", got)
	}
	if failures.Load() != 8 {
		t.Fatalf("%d callers failed, want all 8", failures.Load())
	}

	// Within the TTL the failure is reused without a fetch.
	advance(samlBuildFailureTTL - time.Second)
	if _, err := h.middleware(context.Background()); err == nil {
		t.Fatal("cached failure answered with a middleware")
	}
	if got := idp.hits.Load(); got != 1 {
		t.Fatalf("within the failure TTL: %d fetches, want still 1", got)
	}

	// After it, the next request retries — and once the IdP works, the built
	// middleware is cached.
	idp.ok.Store(true)
	advance(2 * time.Second)
	mw, err := h.middleware(context.Background())
	if err != nil || mw == nil {
		t.Fatalf("after the TTL with a working IdP: %v", err)
	}
	if got := idp.hits.Load(); got != 2 {
		t.Fatalf("after the TTL: %d fetches, want 2", got)
	}
	for i := 0; i < 3; i++ {
		if again, err := h.middleware(context.Background()); err != nil || again != mw {
			t.Fatalf("cached middleware not reused: %v", err)
		}
	}
	if got := idp.hits.Load(); got != 2 {
		t.Errorf("a cached build refetched: %d fetches, want 2", got)
	}
}

// A caller hanging up mid-fetch must not fail — and negatively cache — the
// build every other waiter shares.
func TestSAMLMiddleware_BuildSurvivesCallerCancel(t *testing.T) {
	certPEM, keyPEM, err := GenerateSPKeyPair("onscreen.test")
	if err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t, certPEM)
	idp.ok.Store(true)
	cfg := settings.SAMLConfig{
		Enabled: true, IdPMetadataURL: idp.srv.URL + "/metadata",
		SPCertificatePEM: certPEM, SPPrivateKeyPEM: keyPEM,
	}
	h := NewSAMLHandler(bindTestSAMLSettings{cfg}, nil, "https://onscreen.example",
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _ = h.middleware(ctx) // gives up long before the 100 ms fetch ends
	if _, err := h.middleware(context.Background()); err != nil {
		t.Fatalf("build after a cancelled caller: %v", err)
	}
	if got := idp.hits.Load(); got != 1 {
		t.Errorf("%d fetches, want 1 (the detached build served both)", got)
	}
}
