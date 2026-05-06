package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── SafeTransport ────────────────────────────────────────────────────────────

func TestSafeTransport_RejectsLoopback(t *testing.T) {
	tr := SafeTransport()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	// httptest starts on 127.0.0.1 — SafeTransport should reject it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected error for loopback address, got nil")
	}
}

func TestSafeTransport_RejectsPrivateAddress(t *testing.T) {
	tr := SafeTransport()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	// 192.168.x.x is private — connection should be rejected.
	_, err := client.Get("http://192.168.1.1:9999")
	if err == nil {
		t.Fatal("expected error for private address, got nil")
	}
}

// ── Deliver ──────────────────────────────────────────────────────────────────

func testEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key := sha256.Sum256([]byte("test-secret-key-for-webhook-tests"))
	enc, err := auth.NewEncryptor(key[:])
	if err != nil {
		t.Fatalf("create encryptor: %v", err)
	}
	return enc
}

func TestDeliver_Success(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enc := testEncryptor(t)
	ep := gen.WebhookEndpoint{Url: srv.URL, Enabled: true}
	body := []byte(`{"event":"media.play"}`)

	err := Deliver(context.Background(), srv.Client(), enc, ep, body)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", gotContentType, "application/json")
	}
	if string(gotBody) != string(body) {
		t.Errorf("body: got %q, want %q", gotBody, body)
	}
}

func TestDeliver_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	enc := testEncryptor(t)
	ep := gen.WebhookEndpoint{Url: srv.URL, Enabled: true}

	err := Deliver(context.Background(), srv.Client(), enc, ep, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestDeliver_HMACSignature(t *testing.T) {
	var gotSignature, gotTimestamp string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-OnScreen-Signature")
		gotTimestamp = r.Header.Get("X-OnScreen-Timestamp")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enc := testEncryptor(t)
	secret := "my-webhook-secret"
	encrypted, err := enc.Encrypt(secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ep := gen.WebhookEndpoint{Url: srv.URL, Enabled: true, Secret: &encrypted}
	body := []byte(`{"event":"test"}`)

	if err := Deliver(context.Background(), srv.Client(), enc, ep, body); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	// Stripe-shaped signature: HMAC over "{ts}.{body}" with the
	// timestamp echoed in X-OnScreen-Timestamp. Receivers reject
	// timestamps outside their drift window AND verify the MAC over
	// the timestamp+body together — this defeats indefinite replay
	// of a captured (sig, body) pair.
	if gotTimestamp == "" {
		t.Fatal("X-OnScreen-Timestamp header missing")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(gotTimestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSignature != want {
		t.Errorf("signature:\n  got  %q\n  want %q", gotSignature, want)
	}
}

func TestDeliver_NoSignatureWhenNoSecret(t *testing.T) {
	var gotSignature string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignature = r.Header.Get("X-OnScreen-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enc := testEncryptor(t)
	ep := gen.WebhookEndpoint{Url: srv.URL, Enabled: true}

	if err := Deliver(context.Background(), srv.Client(), enc, ep, []byte(`{}`)); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if gotSignature != "" {
		t.Errorf("expected no signature header, got %q", gotSignature)
	}
}

func TestDeliver_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // never responds in time
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	enc := testEncryptor(t)
	ep := gen.WebhookEndpoint{Url: srv.URL, Enabled: true}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := Deliver(ctx, srv.Client(), enc, ep, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
