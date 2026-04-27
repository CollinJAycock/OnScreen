package valkey

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestClient returns a *Client backed by an in-process miniredis. The
// miniredis is auto-cleaned via t.Cleanup. Pattern matches the one in
// ratelimit_test.go so the two test files share an idiom.
func newTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &Client{rdb: rdb}, mr
}

func TestClient_SetAndGet(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	if err := c.Set(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v" {
		t.Errorf("got %q, want \"v\"", got)
	}
}

func TestClient_Get_MissingReturnsErrNotFound(t *testing.T) {
	// Sentinel: callers branch on errors.Is(err, ErrNotFound) to
	// distinguish "key absent" from "Valkey is broken." Locking down the
	// exported sentinel matters because go-redis's redis.Nil isn't
	// obviously a "not found" signal at call sites.
	c, _ := newTestClient(t)
	_, err := c.Get(context.Background(), "missing-key")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestClient_Set_TTLActuallyExpires(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	if err := c.Set(ctx, "ephemeral", "x", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(200 * time.Millisecond)

	if _, err := c.Get(ctx, "ephemeral"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound after TTL expiry", err)
	}
}

func TestClient_Del_RemovesKeys(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	_ = c.Set(ctx, "a", "1", time.Minute)
	_ = c.Set(ctx, "b", "2", time.Minute)
	_ = c.Set(ctx, "c", "3", time.Minute)

	if err := c.Del(ctx, "a", "c"); err != nil {
		t.Fatalf("Del: %v", err)
	}

	if _, err := c.Get(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a should be gone, got %v", err)
	}
	if _, err := c.Get(ctx, "c"); !errors.Is(err, ErrNotFound) {
		t.Errorf("c should be gone, got %v", err)
	}
	if v, err := c.Get(ctx, "b"); err != nil || v != "2" {
		t.Errorf("b should survive: v=%q err=%v", v, err)
	}
}

func TestClient_SetNX_FirstWriteWinsSecondReturnsFalse(t *testing.T) {
	// SetNX is the "claim a lock" primitive. Two callers must not both
	// observe success.
	c, _ := newTestClient(t)
	ctx := context.Background()

	ok1, err := c.SetNX(ctx, "lock", "owner-1", time.Minute)
	if err != nil {
		t.Fatalf("first SetNX: %v", err)
	}
	if !ok1 {
		t.Error("first SetNX should succeed on a free key")
	}

	ok2, err := c.SetNX(ctx, "lock", "owner-2", time.Minute)
	if err != nil {
		t.Fatalf("second SetNX: %v", err)
	}
	if ok2 {
		t.Error("second SetNX must fail when key exists — lock invariant broken")
	}

	// Original value preserved.
	if v, _ := c.Get(ctx, "lock"); v != "owner-1" {
		t.Errorf("value mutated: got %q, want owner-1", v)
	}
}

func TestClient_Expire_ResetsTTL(t *testing.T) {
	c, mr := newTestClient(t)
	ctx := context.Background()

	_ = c.Set(ctx, "k", "v", 100*time.Millisecond)
	if err := c.Expire(ctx, "k", time.Hour); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	mr.FastForward(500 * time.Millisecond)

	if _, err := c.Get(ctx, "k"); err != nil {
		t.Errorf("Expire should have extended TTL, got %v", err)
	}
}

// ── Set ops (added for segment-token revocation) ─────────────────────────────

func TestClient_SAdd_SMembers_RoundTrip(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	if err := c.SAdd(ctx, "myset", "alpha", "bravo", "charlie"); err != nil {
		t.Fatalf("SAdd: %v", err)
	}

	got, err := c.SMembers(ctx, "myset")
	if err != nil {
		t.Fatalf("SMembers: %v", err)
	}
	sort.Strings(got)
	want := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("got %d members, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("members[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClient_SAdd_DuplicatesAreIgnored(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	_ = c.SAdd(ctx, "myset", "x")
	_ = c.SAdd(ctx, "myset", "x")
	_ = c.SAdd(ctx, "myset", "x")

	got, err := c.SMembers(ctx, "myset")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("set has %d members after duplicate adds, want 1 (set semantics)", len(got))
	}
}

func TestClient_SRem_RemovesOnlyTargetMember(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	_ = c.SAdd(ctx, "myset", "keep1", "remove-me", "keep2")
	if err := c.SRem(ctx, "myset", "remove-me"); err != nil {
		t.Fatalf("SRem: %v", err)
	}

	got, _ := c.SMembers(ctx, "myset")
	sort.Strings(got)
	want := []string{"keep1", "keep2"}
	if len(got) != 2 {
		t.Fatalf("got %d members, want 2", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("members[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestClient_SMembers_MissingSetReturnsEmpty(t *testing.T) {
	// Important: SMembers on a non-existent key must return empty, not
	// error. The segment-token revoker relies on this — a user who never
	// played anything has no index set and the revocation must be a
	// no-op, not a 500.
	c, _ := newTestClient(t)

	got, err := c.SMembers(context.Background(), "never-existed")
	if err != nil {
		t.Errorf("SMembers on missing key returned error: %v (must be empty slice instead)", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
}

// ── Scan ─────────────────────────────────────────────────────────────────────

func TestClient_Scan_PaginatesAcrossBatches(t *testing.T) {
	// Scan uses cursor-based iteration with a 100-key batch internally.
	// Seed > batch size to make sure the wrap-around path is exercised
	// rather than just the first page.
	c, _ := newTestClient(t)
	ctx := context.Background()

	for i := 0; i < 250; i++ {
		_ = c.Set(ctx, "scan-test:"+intToString(i), "x", time.Minute)
	}

	keys, err := c.Scan(ctx, "scan-test:*")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(keys) != 250 {
		t.Errorf("got %d keys, want 250 (pagination broken?)", len(keys))
	}
}

func TestClient_Scan_PatternFilterExclusive(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	_ = c.Set(ctx, "match:1", "x", time.Minute)
	_ = c.Set(ctx, "match:2", "x", time.Minute)
	_ = c.Set(ctx, "other:1", "x", time.Minute)

	keys, err := c.Scan(ctx, "match:*")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Errorf("got %d keys, want 2 (filter leaked?)", len(keys))
	}
	for _, k := range keys {
		if k == "other:1" {
			t.Errorf("other:1 leaked into match:* scan")
		}
	}
}

// intToString avoids strconv import for this one tiny helper.
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
