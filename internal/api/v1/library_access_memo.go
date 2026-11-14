package v1

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// libraryAccessMemoTTL is how long a (user, library) grant check is reused.
// Short enough that revoking a profile's library access takes effect within
// seconds; long enough that an image grid (every tile an /artwork or
// /items/{id}/image request, each one checking the same grant) costs one
// round trip instead of hundreds against a pool of at most 20 connections.
// Being this stale can only ever let a just-revoked profile load a few more
// images of a library it could see moments earlier.
const libraryAccessMemoTTL = 5 * time.Second

// libraryAccessMemoMax bounds the memo; past it the expired entries are
// swept, and if none have expired it starts over.
const libraryAccessMemoMax = 4096

type libraryAccessKey struct {
	user    uuid.UUID
	library uuid.UUID
	admin   bool
}

type libraryAccessEntry struct {
	ok bool
	at time.Time
}

// libraryAccessMemo wraps a LibraryAccessChecker with a short per-(user,
// library) cache of CanAccessLibrary, in the style of watchLimitMemo. Errors
// are not cached. AllowedLibraryIDs passes straight through: list endpoints
// call it once per request, not once per image.
type libraryAccessMemo struct {
	inner LibraryAccessChecker

	mu  sync.Mutex
	m   map[libraryAccessKey]libraryAccessEntry
	now func() time.Time // overridable in tests
}

// NewLibraryAccessMemo wraps inner. A nil inner returns nil, so "no checker
// wired" stays a nil interface (handlers test `access == nil`).
func NewLibraryAccessMemo(inner LibraryAccessChecker) LibraryAccessChecker {
	if inner == nil {
		return nil
	}
	if m, ok := inner.(*libraryAccessMemo); ok {
		return m // already memoized
	}
	return &libraryAccessMemo{inner: inner, m: map[libraryAccessKey]libraryAccessEntry{}}
}

func (m *libraryAccessMemo) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *libraryAccessMemo) CanAccessLibrary(ctx context.Context, userID, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	key := libraryAccessKey{user: userID, library: libraryID, admin: isAdmin}
	now := m.clock()
	m.mu.Lock()
	if e, ok := m.m[key]; ok && now.Sub(e.at) < libraryAccessMemoTTL {
		m.mu.Unlock()
		return e.ok, nil
	}
	m.mu.Unlock()

	// Checked outside the lock: a slow DB must not serialize every other
	// request's check behind this one. A concurrent duplicate is harmless.
	ok, err := m.inner.CanAccessLibrary(ctx, userID, libraryID, isAdmin)
	if err != nil {
		return ok, err
	}
	m.mu.Lock()
	if len(m.m) >= libraryAccessMemoMax {
		for k, e := range m.m {
			if now.Sub(e.at) >= libraryAccessMemoTTL {
				delete(m.m, k)
			}
		}
		if len(m.m) >= libraryAccessMemoMax {
			m.m = map[libraryAccessKey]libraryAccessEntry{}
		}
	}
	m.m[key] = libraryAccessEntry{ok: ok, at: now}
	m.mu.Unlock()
	return ok, nil
}

func (m *libraryAccessMemo) AllowedLibraryIDs(ctx context.Context, userID uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	return m.inner.AllowedLibraryIDs(ctx, userID, isAdmin)
}
