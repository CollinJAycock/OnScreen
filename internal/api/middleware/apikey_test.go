package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// fakeKeyStore is an in-memory APIKeyResolver. Policy fields live on the
// user, not the key, so a test can change them between requests the way an
// admin would.
type fakeKeyStore struct {
	mu       sync.Mutex
	keys     map[string]APIKeyIdentity // hash → identity
	epoch    int64
	isAdmin  bool
	blocked  []string
	err      error
	touches  int
	userGone bool
}

func (f *fakeKeyStore) ResolveAPIKey(_ context.Context, hash string) (APIKeyIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return APIKeyIdentity{}, f.err
	}
	id, ok := f.keys[hash]
	if !ok {
		return APIKeyIdentity{}, ErrAPIKeyNotFound
	}
	id.SessionEpoch = f.epoch
	id.IsAdmin = f.isAdmin
	id.BlockedLabels = f.blocked
	return id, nil
}

func (f *fakeKeyStore) APIKeyState(_ context.Context, keyID, _ uuid.UUID) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.userGone {
		return 0, false, ErrUserNotFound
	}
	if f.err != nil {
		return 0, false, f.err
	}
	for _, id := range f.keys {
		if id.KeyID == keyID {
			return f.epoch, true, nil
		}
	}
	return f.epoch, false, nil
}

func (f *fakeKeyStore) TouchAPIKey(context.Context, uuid.UUID) error {
	f.mu.Lock()
	f.touches++
	f.mu.Unlock()
	return nil
}

func (f *fakeKeyStore) add(scope string) (raw string, id APIKeyIdentity) {
	raw, hash, _, _ := auth.GenerateAPIKey()
	id = APIKeyIdentity{KeyID: uuid.New(), UserID: uuid.New(), Scope: scope, Username: "script"}
	f.mu.Lock()
	if f.keys == nil {
		f.keys = map[string]APIKeyIdentity{}
	}
	f.keys[hash] = id
	f.mu.Unlock()
	return raw, id
}

func (f *fakeKeyStore) remove(raw string) {
	f.mu.Lock()
	delete(f.keys, auth.HashAPIKey(raw))
	f.mu.Unlock()
}

// claimsEcho records the claims the handler saw.
func claimsEcho(got **auth.Claims) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = ClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestAPIKey_BothHeadersAuthenticate(t *testing.T) {
	store := &fakeKeyStore{epoch: 7}
	raw, id := store.add(auth.APIKeyScopeFull)
	a := NewAuthenticator(testTokenMaker(t)).WithAPIKeys(store)

	for name, set := range map[string]func(*http.Request){
		"bearer":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+raw) },
		"x-api-key": func(r *http.Request) { r.Header.Set("X-Api-Key", raw) },
	} {
		t.Run(name, func(t *testing.T) {
			var got *auth.Claims
			r := httptest.NewRequest(http.MethodPost, "/api/v1/items/x/progress", nil)
			set(r)
			w := httptest.NewRecorder()
			a.Required(claimsEcho(&got)).ServeHTTP(w, r)
			if w.Code != http.StatusOK || got == nil {
				t.Fatalf("status %d, claims %v", w.Code, got)
			}
			if got.UserID != id.UserID || got.APIKeyID != id.KeyID || got.SessionEpoch != 7 || got.Purpose != "" {
				t.Fatalf("claims not built from the key's owner: %+v", got)
			}
		})
	}
}

// The owner's policy is read on every request: an admin narrowing the user's
// labels (or demoting them) applies to the very next key request.
func TestAPIKey_PolicyIsLive(t *testing.T) {
	store := &fakeKeyStore{isAdmin: true}
	raw, _ := store.add(auth.APIKeyScopeFull)
	a := NewAuthenticator(testTokenMaker(t)).WithAPIKeys(store)
	call := func() *auth.Claims {
		var got *auth.Claims
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Api-Key", raw)
		a.Required(claimsEcho(&got)).ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	if c := call(); c == nil || !c.IsAdmin || len(c.BlockedLabels) != 0 {
		t.Fatalf("first call: %+v", c)
	}
	store.mu.Lock()
	store.isAdmin, store.blocked = false, []string{"horror"}
	store.mu.Unlock()
	if c := call(); c == nil || c.IsAdmin || len(c.BlockedLabels) != 1 {
		t.Fatalf("policy change not applied: %+v", c)
	}
}

func TestAPIKey_Rejections(t *testing.T) {
	store := &fakeKeyStore{}
	raw, _ := store.add(auth.APIKeyScopeFull)
	ro, _ := store.add(auth.APIKeyScopeReadOnly)
	unknown, _, _, _ := auth.GenerateAPIKey() // well-formed, never stored
	a := NewAuthenticator(testTokenMaker(t)).WithAPIKeys(store)

	cases := []struct {
		name   string
		method string
		target string
		set    func(*http.Request)
		want   int
	}{
		{"unknown key", http.MethodGet, "/", func(r *http.Request) { r.Header.Set("X-Api-Key", unknown) }, 401},
		{"malformed key header", http.MethodGet, "/", func(r *http.Request) { r.Header.Set("X-Api-Key", "letmein") }, 401},
		{"read-only key on GET", http.MethodGet, "/", func(r *http.Request) { r.Header.Set("X-Api-Key", ro) }, 200},
		{"read-only key on HEAD", http.MethodHead, "/", func(r *http.Request) { r.Header.Set("X-Api-Key", ro) }, 200},
		{"read-only key on POST", http.MethodPost, "/", func(r *http.Request) { r.Header.Set("X-Api-Key", ro) }, 403},
		{"read-only key on DELETE", http.MethodDelete, "/", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+ro) }, 403},
		// Never in a URL: a long-lived key must not reach logs / Referer.
		{"key in ?token=", http.MethodGet, "/?token=" + raw, func(*http.Request) {}, 401},
		{"key in ?api_key=", http.MethodGet, "/?api_key=" + raw, func(*http.Request) {}, 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.target, nil)
			c.set(r)
			w := httptest.NewRecorder()
			var got *auth.Claims
			a.Required(claimsEcho(&got)).ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("status %d, want %d", w.Code, c.want)
			}
		})
	}

	// The asset-route variant takes keys in headers but still not in ?token=.
	r := httptest.NewRequest(http.MethodGet, "/?token="+raw, nil)
	w := httptest.NewRecorder()
	var got *auth.Claims
	a.RequiredAllowQueryToken(claimsEcho(&got)).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("?token=<api key> on an asset route: status %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Api-Key", raw)
	w = httptest.NewRecorder()
	a.RequiredAllowQueryToken(claimsEcho(&got)).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("header key on an asset route: status %d", w.Code)
	}
}

// Without a resolver (or with the store down) a key is not a credential:
// fail closed, never fall through to "authenticated".
func TestAPIKey_NoResolverOrDBErrorFailsClosed(t *testing.T) {
	store := &fakeKeyStore{}
	raw, _ := store.add(auth.APIKeyScopeFull)
	for name, a := range map[string]*Authenticator{
		"no resolver": NewAuthenticator(testTokenMaker(t)),
		"db error":    NewAuthenticator(testTokenMaker(t)).WithAPIKeys(&fakeKeyStore{keys: store.keys, err: errors.New("conn refused")}),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Authorization", "Bearer "+raw)
			w := httptest.NewRecorder()
			var got *auth.Claims
			a.Required(claimsEcho(&got)).ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
}

// A PASETO Bearer token keeps working next to the key support, and wins over
// a stray X-Api-Key header.
func TestAPIKey_PasetoBearerUnaffected(t *testing.T) {
	tm := testTokenMaker(t)
	store := &fakeKeyStore{}
	raw, _ := store.add(auth.APIKeyScopeReadOnly)
	a := NewAuthenticator(tm).WithAPIKeys(store)
	uid := uuid.New()
	tok, _ := tm.IssueAccessToken(auth.Claims{UserID: uid})
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("X-Api-Key", raw)
	w := httptest.NewRecorder()
	var got *auth.Claims
	a.Required(claimsEcho(&got)).ServeHTTP(w, r)
	if w.Code != http.StatusOK || got == nil || got.UserID != uid || got.IsAPIKey() {
		t.Fatalf("status %d claims %+v", w.Code, got)
	}
}

// Deleting a key kills the key at once, and the stream tokens a request made
// with it minted, and a long-lived response re-checking its claims.
func TestAPIKey_DeletionRevokesDerivedCredentials(t *testing.T) {
	tm := testTokenMaker(t)
	store := &fakeKeyStore{epoch: 1}
	raw, id := store.add(auth.APIKeyScopeFull)
	a := NewAuthenticator(tm).WithAPIKeys(store)
	ctx := context.Background()

	var claims *auth.Claims
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Api-Key", raw)
	a.Required(claimsEcho(&claims)).ServeHTTP(httptest.NewRecorder(), r)
	if claims == nil {
		t.Fatal("key not admitted")
	}
	fileID := uuid.New()
	stream, _ := tm.IssueStreamToken(*claims, fileID)
	streamClaims, err := tm.ValidateAccessToken(stream)
	if err != nil || streamClaims.APIKeyID != id.KeyID {
		t.Fatalf("stream token: %v %+v", err, streamClaims)
	}
	if !a.SessionStillValid(ctx, claims) || !a.SessionStillValid(ctx, streamClaims) {
		t.Fatal("live key's credentials rejected")
	}

	store.remove(raw)
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Api-Key", raw)
	w := httptest.NewRecorder()
	a.Required(claimsEcho(&claims)).ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted key still admitted: %d", w.Code)
	}
	if a.SessionStillValid(ctx, streamClaims) {
		t.Fatal("stream token minted under a deleted key still valid")
	}

	// An epoch bump (e.g. a restriction change) ends derived tokens too, like
	// every other token; the key itself just resolves the new epoch.
	raw2, _ := store.add(auth.APIKeyScopeFull)
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Api-Key", raw2)
	a.Required(claimsEcho(&claims)).ServeHTTP(httptest.NewRecorder(), r)
	store.mu.Lock()
	store.epoch = 2
	store.mu.Unlock()
	if a.SessionStillValid(ctx, claims) {
		t.Fatal("epoch bump didn't end derived credentials")
	}
	store.mu.Lock()
	store.userGone = true
	store.mu.Unlock()
	if a.SessionStillValid(ctx, claims) {
		t.Fatal("deleted user's key credential still valid")
	}
}

func TestAPIKey_LastUsedIsThrottled(t *testing.T) {
	store := &fakeKeyStore{}
	raw, _ := store.add(auth.APIKeyScopeFull)
	a := NewAuthenticator(testTokenMaker(t)).WithAPIKeys(store)
	for i := 0; i < 20; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Api-Key", raw)
		var got *auth.Claims
		a.Required(claimsEcho(&got)).ServeHTTP(httptest.NewRecorder(), r)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		n := store.touches
		store.mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			if n != 1 {
				t.Fatalf("last_used written %d times for 20 requests, want 1", n)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRefuseAPIKey(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RefuseAPIKey(ok)

	// Authenticated with a key.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(WithClaims(r.Context(), &auth.Claims{UserID: uuid.New(), APIKeyID: uuid.New()}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("key claims: status %d", w.Code)
	}

	// A key presented on an unauthenticated route (login, refresh).
	for _, set := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("X-Api-Key", "osk_whatever") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer osk_whatever") },
	} {
		r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		set(r)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("presented key: status %d", w.Code)
		}
	}

	// Sessions pass.
	r = httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(WithClaims(r.Context(), &auth.Claims{UserID: uuid.New()}))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("session claims: status %d", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("anonymous login: status %d", w.Code)
	}
}

// AdminRequired reuses key claims (they are general-purpose) and applies the
// owner's live admin flag.
func TestAPIKey_AdminRequired(t *testing.T) {
	store := &fakeKeyStore{isAdmin: false}
	raw, _ := store.add(auth.APIKeyScopeFull)
	a := NewAuthenticator(testTokenMaker(t)).WithAPIKeys(store)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	chain := a.Required(a.AdminRequired(ok))
	call := func() int {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Api-Key", raw)
		w := httptest.NewRecorder()
		chain.ServeHTTP(w, r)
		return w.Code
	}
	if c := call(); c != http.StatusForbidden {
		t.Fatalf("non-admin key: %d", c)
	}
	store.mu.Lock()
	store.isAdmin = true
	store.mu.Unlock()
	if c := call(); c != http.StatusOK {
		t.Fatalf("admin key: %d", c)
	}
}
