package v1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// selfSignedPEM returns a throwaway cert + key pair for exercising the upload path.
func selfSignedPEM(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func tlsHandler(svc *mockSettingsService, envConfigured bool) *SettingsHandler {
	return NewSettingsHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetTLSEnvConfigured(envConfigured)
}

func getTLSStatus(t *testing.T, h *SettingsHandler) tlsStatusDTO {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetTLS(rec, httptest.NewRequest(http.MethodGet, "/settings/tls", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GetTLS status = %d", rec.Code)
	}
	var env struct {
		Data tlsStatusDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestGetTLS_Source(t *testing.T) {
	// Nothing stored, no env → none.
	if got := getTLSStatus(t, tlsHandler(&mockSettingsService{}, false)); got.Configured || got.Source != "none" {
		t.Errorf("empty: got %+v, want none/false", got)
	}
	// Env file paths set → env-file, configured, no PEMs leaked.
	if got := getTLSStatus(t, tlsHandler(&mockSettingsService{}, true)); !got.Configured || got.Source != "env-file" {
		t.Errorf("env: got %+v, want env-file/true", got)
	}
	// Stored uploaded cert → uploaded + parsed subject.
	cert, key := selfSignedPEM(t)
	got := getTLSStatus(t, tlsHandler(&mockSettingsService{tlsCfg: settings.TLSConfig{CertPEM: cert, KeyPEM: key}}, false))
	if !got.Configured || got.Source != "uploaded" || got.Subject != "test.local" {
		t.Errorf("uploaded: got %+v", got)
	}
}

func TestUpdateTLS_ValidatesAndStores(t *testing.T) {
	cert, key := selfSignedPEM(t)
	svc := &mockSettingsService{}
	h := tlsHandler(svc, false)

	// Mismatched/garbage pair → 400, nothing stored.
	rec := httptest.NewRecorder()
	body := `{"cert_pem":"-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----","key_pem":"bad"}`
	h.UpdateTLS(rec, httptest.NewRequest(http.MethodPut, "/settings/tls", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage pair: status = %d, want 400", rec.Code)
	}
	if svc.tlsCfg.CertPEM != "" {
		t.Error("garbage pair should not have been stored")
	}

	// Valid pair → 204, stored.
	stored, _ := json.Marshal(tlsUpdateDTO{CertPEM: cert, KeyPEM: key})
	rec = httptest.NewRecorder()
	h.UpdateTLS(rec, httptest.NewRequest(http.MethodPut, "/settings/tls", strings.NewReader(string(stored))))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid pair: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if svc.tlsCfg.CertPEM != cert || svc.tlsCfg.KeyPEM != key {
		t.Error("valid pair not stored")
	}
}

func TestUpdateTLS_BlockedWhenEnvConfigured(t *testing.T) {
	h := tlsHandler(&mockSettingsService{}, true)
	rec := httptest.NewRecorder()
	h.UpdateTLS(rec, httptest.NewRequest(http.MethodPut, "/settings/tls", strings.NewReader(`{"cert_pem":"x","key_pem":"y"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("env-configured upload should be rejected: status = %d", rec.Code)
	}
}
