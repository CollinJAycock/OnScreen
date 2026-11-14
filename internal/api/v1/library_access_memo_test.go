package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type countingAccess struct {
	allowed map[uuid.UUID]bool
	err     error
	calls   int
}

func (c *countingAccess) CanAccessLibrary(_ context.Context, _, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	c.calls++
	if c.err != nil {
		return false, c.err
	}
	return isAdmin || c.allowed[libraryID], nil
}

func (c *countingAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

// An image grid checks the same (user, library) grant per tile; the memo
// answers repeats for libraryAccessMemoTTL, keeps users and libraries apart,
// lets a revocation through after the TTL, and never caches an error.
func TestLibraryAccessMemo(t *testing.T) {
	ctx := context.Background()
	user, other, lib := uuid.New(), uuid.New(), uuid.New()
	inner := &countingAccess{allowed: map[uuid.UUID]bool{lib: true}}
	m := NewLibraryAccessMemo(inner).(*libraryAccessMemo)
	now := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		if ok, err := m.CanAccessLibrary(ctx, user, lib, false); !ok || err != nil {
			t.Fatalf("CanAccessLibrary = %v, %v", ok, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	_, _ = m.CanAccessLibrary(ctx, other, lib, false)
	_, _ = m.CanAccessLibrary(ctx, user, uuid.New(), false)
	if inner.calls != 3 {
		t.Fatalf("inner calls = %d, want 3 (per user and per library)", inner.calls)
	}

	// Revoked: visible once the entry expires.
	inner.allowed[lib] = false
	now = now.Add(libraryAccessMemoTTL)
	if ok, _ := m.CanAccessLibrary(ctx, user, lib, false); ok {
		t.Fatal("a revoked grant must be denied after the TTL")
	}

	inner.err = errors.New("db blip")
	now = now.Add(libraryAccessMemoTTL)
	if _, err := m.CanAccessLibrary(ctx, user, lib, false); err == nil {
		t.Fatal("expected the error")
	}
	inner.err = nil
	calls := inner.calls
	_, _ = m.CanAccessLibrary(ctx, user, lib, false)
	if inner.calls != calls+1 {
		t.Fatal("an error must not be memoized")
	}
}

func TestNewLibraryAccessMemo_NilAndIdempotent(t *testing.T) {
	if NewLibraryAccessMemo(nil) != nil {
		t.Fatal("nil checker must stay nil (handlers treat nil as unwired)")
	}
	m := NewLibraryAccessMemo(&countingAccess{})
	if NewLibraryAccessMemo(m) != m {
		t.Fatal("wrapping a memo again should return it unchanged")
	}
}
