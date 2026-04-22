package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
)

// memPairStore is an in-memory PairStore used by the pairing tests.
type memPairStore struct {
	mu   sync.Mutex
	data map[string]string
	// failGet, failSet let individual tests force store errors.
	failGet bool
	failSet bool
}

func newMemPairStore() *memPairStore {
	return &memPairStore{data: map[string]string{}}
}

func (m *memPairStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	if m.failSet {
		return errors.New("set failed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *memPairStore) Get(_ context.Context, key string) (string, error) {
	if m.failGet {
		return "", errors.New("get failed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", ErrPairNotFound
	}
	return v, nil
}

func (m *memPairStore) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.data, k)
	}
	return nil
}

func newPairHandler(store PairStore, issuer PairTokenIssuer) *PairHandler {
	return NewPairHandler(store, issuer, slog.Default())
}

func decodePairData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	raw, ok := env["data"]
	if !ok {
		t.Fatalf("response missing data envelope: %s", body)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	return out
}

// ── CreateCode ────────────────────────────────────────────────────────────────

func TestPair_CreateCode_ReturnsPINAndDeviceToken(t *testing.T) {
	store := newMemPairStore()
	h := newPairHandler(store, nil)
	rec := httptest.NewRecorder()
	h.CreateCode(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/code", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	data := decodePairData(t, rec.Body.Bytes())
	pin, _ := data["pin"].(string)
	if !validPIN(pin) {
		t.Errorf("pin %q is not 6 digits", pin)
	}
	dev, _ := data["device_token"].(string)
	if dev == "" {
		t.Errorf("device_token missing")
	}
	// Both keys must be present in store under their conventional prefixes.
	if _, err := store.Get(context.Background(), pairKeyPIN+pin); err != nil {
		t.Errorf("pin index not stored: %v", err)
	}
	if _, err := store.Get(context.Background(), pairKeyDev+dev); err != nil {
		t.Errorf("device record not stored: %v", err)
	}
}

func TestPair_CreateCode_StoreFailureReturns500(t *testing.T) {
	store := newMemPairStore()
	store.failSet = true
	h := newPairHandler(store, nil)
	rec := httptest.NewRecorder()
	h.CreateCode(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/code", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

// ── Poll ──────────────────────────────────────────────────────────────────────

func TestPair_Poll_Pending(t *testing.T) {
	store := newMemPairStore()
	h := newPairHandler(store, nil)

	// Seed a pending record.
	rec := httptest.NewRecorder()
	h.CreateCode(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/code", nil))
	dev, _ := decodePairData(t, rec.Body.Bytes())["device_token"].(string)

	rec = httptest.NewRecorder()
	h.Poll(rec, httptest.NewRequest("GET", "/api/v1/auth/pair/poll?device_token="+dev, nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPair_Poll_MissingToken(t *testing.T) {
	h := newPairHandler(newMemPairStore(), nil)
	rec := httptest.NewRecorder()
	h.Poll(rec, httptest.NewRequest("GET", "/api/v1/auth/pair/poll", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPair_Poll_UnknownToken(t *testing.T) {
	h := newPairHandler(newMemPairStore(), nil)
	rec := httptest.NewRecorder()
	h.Poll(rec, httptest.NewRequest("GET", "/api/v1/auth/pair/poll?device_token=nope", nil))
	if rec.Code != http.StatusGone {
		t.Errorf("status: got %d, want 410", rec.Code)
	}
}

// ── Claim → Poll happy path ───────────────────────────────────────────────────

func TestPair_ClaimThenPoll_IssuesTokens(t *testing.T) {
	store := newMemPairStore()
	userID := uuid.New()
	issued := false
	issuer := func(_ context.Context, uid uuid.UUID) (*TokenPair, error) {
		if uid != userID {
			t.Errorf("issuer got uid %s, want %s", uid, userID)
		}
		issued = true
		return &TokenPair{AccessToken: "at", RefreshToken: "rt", UserID: uid, Username: "alice"}, nil
	}
	h := newPairHandler(store, issuer)

	// Step 1: device requests a code.
	rec := httptest.NewRecorder()
	h.CreateCode(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/code", nil))
	data := decodePairData(t, rec.Body.Bytes())
	pin, _ := data["pin"].(string)
	dev, _ := data["device_token"].(string)

	// Step 2: browser-authenticated user claims the PIN.
	claims := &auth.Claims{UserID: userID, Username: "alice"}
	body := strings.NewReader(`{"pin":"` + pin + `","device_name":"Living Room TV"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/pair/claim", body)
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec = httptest.NewRecorder()
	h.Claim(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// Step 3: device polls and receives tokens.
	rec = httptest.NewRecorder()
	h.Poll(rec, httptest.NewRequest("GET", "/api/v1/auth/pair/poll?device_token="+dev, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !issued {
		t.Error("token issuer was not invoked")
	}
	got := decodePairData(t, rec.Body.Bytes())
	if got["access_token"] != "at" {
		t.Errorf("access_token: got %v, want \"at\"", got["access_token"])
	}

	// Step 4: second poll should be Gone — record is one-shot.
	rec = httptest.NewRecorder()
	h.Poll(rec, httptest.NewRequest("GET", "/api/v1/auth/pair/poll?device_token="+dev, nil))
	if rec.Code != http.StatusGone {
		t.Errorf("repeat poll status: got %d, want 410", rec.Code)
	}
}

// ── Claim error paths ─────────────────────────────────────────────────────────

func TestPair_Claim_RequiresAuth(t *testing.T) {
	h := newPairHandler(newMemPairStore(), nil)
	rec := httptest.NewRecorder()
	h.Claim(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/claim",
		strings.NewReader(`{"pin":"123456"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestPair_Claim_RejectsBadPIN(t *testing.T) {
	h := newPairHandler(newMemPairStore(), nil)
	claims := &auth.Claims{UserID: uuid.New(), Username: "alice"}
	cases := []string{"abcdef", "12345", "1234567", ""}
	for _, p := range cases {
		req := httptest.NewRequest("POST", "/api/v1/auth/pair/claim",
			strings.NewReader(`{"pin":"`+p+`"}`))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.Claim(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("pin %q: status %d, want 400", p, rec.Code)
		}
	}
}

func TestPair_Claim_UnknownPIN(t *testing.T) {
	h := newPairHandler(newMemPairStore(), nil)
	claims := &auth.Claims{UserID: uuid.New(), Username: "alice"}
	req := httptest.NewRequest("POST", "/api/v1/auth/pair/claim",
		strings.NewReader(`{"pin":"999999"}`))
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h.Claim(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

func TestPair_Claim_AlreadyClaimedReturns409(t *testing.T) {
	store := newMemPairStore()
	issuer := func(_ context.Context, uid uuid.UUID) (*TokenPair, error) {
		return &TokenPair{AccessToken: "at", UserID: uid}, nil
	}
	h := newPairHandler(store, issuer)
	claims := &auth.Claims{UserID: uuid.New(), Username: "alice"}

	// Create + claim once.
	rec := httptest.NewRecorder()
	h.CreateCode(rec, httptest.NewRequest("POST", "/api/v1/auth/pair/code", nil))
	pin, _ := decodePairData(t, rec.Body.Bytes())["pin"].(string)
	req := httptest.NewRequest("POST", "/api/v1/auth/pair/claim",
		strings.NewReader(`{"pin":"`+pin+`"}`))
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec = httptest.NewRecorder()
	h.Claim(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first claim: %d", rec.Code)
	}

	// PIN reverse-index is gone after first claim, so second claim with same
	// PIN gets 404 (the cleaner failure mode); to provoke the "already
	// claimed" path we need to look up the device token directly. Easiest
	// path: re-add the PIN reverse-index pointing at the same device.
	// (Real-world this can't happen — the first claim removed the index.)
	for k, v := range store.data {
		if strings.HasPrefix(k, pairKeyDev+"") {
			// derive devToken from key
			devToken := strings.TrimPrefix(k, pairKeyDev)
			_ = v
			_ = store.Set(context.Background(), pairKeyPIN+pin, devToken, time.Minute)
			break
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/v1/auth/pair/claim",
		strings.NewReader(`{"pin":"`+pin+`"}`))
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	h.Claim(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("repeat claim: got %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}
