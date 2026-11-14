package transcode

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/valkey"
)

// roundTrips counts the Valkey round trips a client makes: one per command
// sent on its own, one per pipeline or MULTI/EXEC block.
type roundTrips struct{ n atomic.Int64 }

func (c *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmd)
	}
}

func (c *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		c.n.Add(1)
		return next(ctx, cmds)
	}
}

// during returns how many round trips fn made.
func (c *roundTrips) during(fn func()) int64 {
	before := c.n.Load()
	fn()
	return c.n.Load() - before
}

func countedValkey(t *testing.T) (*valkey.Client, *roundTrips) {
	t.Helper()
	v := testvalkey.New(t)
	rt := &roundTrips{}
	v.Raw().AddHook(rt)
	return v, rt
}

// The token check used to be a GET and two EXPIREs, and the segment handler's
// session read a fourth round trip. Authorize does all of it in one, and the
// sessions it returns are the stored ones.
func TestAuthorize_OneRoundTripWithSessions(t *testing.T) {
	v, rt := countedValkey(t)
	store := NewSessionStore(v)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()
	user := uuid.New()

	sess := Session{ID: "auth-1", UserID: user, MediaItemID: uuid.New(), WorkerAddr: "10.0.0.9:7073", CreatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	tok, err := mgr.Issue(ctx, sess.ID, user)
	if err != nil {
		t.Fatal(err)
	}
	// Load the script once, as the first fetch after a Valkey start does.
	if _, err := mgr.Authorize(ctx, tok, sess.ID); err != nil {
		t.Fatal(err)
	}

	var acc *SegmentAccess
	if n := rt.during(func() { acc, err = mgr.Authorize(ctx, tok, sess.ID, sess.ID, "missing") }); n != 1 {
		t.Errorf("Authorize took %d round trips, want 1", n)
	}
	if err != nil {
		t.Fatal(err)
	}
	if acc.UserID != user {
		t.Errorf("UserID = %s, want %s", acc.UserID, user)
	}
	if len(acc.Sessions) != 2 || acc.Sessions[0] == nil || acc.Sessions[0].WorkerAddr != sess.WorkerAddr {
		t.Fatalf("sessions = %+v, want the stored session then nil", acc.Sessions)
	}
	if acc.Sessions[1] != nil {
		t.Error("a missing session must come back nil")
	}
	if n := rt.during(func() { _, _, err = mgr.Validate(ctx, tok) }); n != 1 || err != nil {
		t.Errorf("Validate: %d round trips, err %v; want 1, nil", n, err)
	}

	// Bound to its own session only.
	if _, err := mgr.Authorize(ctx, tok, "some-other-session"); !errors.Is(err, ErrInvalidSegmentToken) {
		t.Errorf("token for another session: err = %v, want ErrInvalidSegmentToken", err)
	}
	// Revocation is seen by the very next check: nothing is cached.
	if err := mgr.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Authorize(ctx, tok, sess.ID, sess.ID); !errors.Is(err, ErrInvalidSegmentToken) {
		t.Errorf("revoked token: err = %v, want ErrInvalidSegmentToken", err)
	}
}

// Both TTLs are refreshed by the one-trip script, as by the old GET+EXPIREs:
// the token's (an idle timeout), and its user index's (RevokeAllForUser must
// still find a token that has outlived the index's first TTL).
func TestAuthorize_RefreshesTokenAndIndexTTL(t *testing.T) {
	v := testvalkey.New(t)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()
	user := uuid.New()
	tok, err := mgr.Issue(ctx, "s", user)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{segTokenKey(tok), userIndexKey(user)} {
		if err := v.Raw().Expire(ctx, k, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mgr.Authorize(ctx, tok, "s"); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{segTokenKey(tok), userIndexKey(user)} {
		if ttl := v.Raw().TTL(ctx, k).Val(); ttl < segTokenTTL-time.Minute {
			t.Errorf("%s TTL = %v after Authorize, want ~%v", k, ttl, segTokenTTL)
		}
	}
	// And the script reloads itself after Valkey loses its script cache.
	if err := v.Raw().ScriptFlush(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Authorize(ctx, tok, "s", "s"); err != nil {
		t.Fatalf("after SCRIPT FLUSH: %v", err)
	}
}

// A segment stamp used to be a WATCH / GET / MULTI-SET-EXEC on every fetch.
// From a snapshot it is free while the stored stamp is fresh and one
// conditional write when due.
func TestStampActivity_ThrottledAndOneTrip(t *testing.T) {
	v, rt := countedValkey(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	sess := Session{ID: "stamp-1", UserID: uuid.New(), CreatedAt: time.Now().Add(-time.Minute)}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	snap, err := store.GetSnapshot(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Prime the script cache.
	store.StampActivity(ctx, snap)
	store.stamps.forget(sess.ID)

	snap, _ = store.GetSnapshot(ctx, sess.ID)
	if n := rt.during(func() { store.StampActivity(ctx, snap) }); n != 0 {
		t.Errorf("fresh stamp: %d round trips, want 0", n)
	}

	// A stamp a minute old is due.
	before := time.Now().Add(-time.Minute)
	if err := store.mutateSession(ctx, sess.ID, func(s *Session) error { s.LastActivityAt = before; return nil }); err != nil {
		t.Fatal(err)
	}
	snap, _ = store.GetSnapshot(ctx, sess.ID)
	if n := rt.during(func() { store.StampActivity(ctx, snap) }); n != 1 {
		t.Errorf("due stamp: %d round trips, want 1", n)
	}
	got, _ := store.Get(ctx, sess.ID)
	if !got.LastActivityAt.After(before.Add(30 * time.Second)) {
		t.Errorf("LastActivityAt not advanced: %v -> %v", before, got.LastActivityAt)
	}
}

// The conditional write must never put a snapshot's older fields back: a
// worker stamp (SetWorkerInfo) landing between the read and the stamp makes
// the write fall back to the WATCH path, which re-reads.
func TestStampActivity_ChangedSinceReadKeepsConcurrentWrite(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	sess := Session{ID: "stamp-race", UserID: uuid.New(), CreatedAt: time.Now().Add(-time.Minute)}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	snap, err := store.GetSnapshot(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkerInfo(ctx, sess.ID, "w1", "10.0.0.9:7073", true, false); err != nil {
		t.Fatal(err)
	}
	store.StampActivity(ctx, snap)

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkerAddr != "10.0.0.9:7073" || !got.HEVCOutput {
		t.Errorf("stamp from a stale snapshot wiped the worker stamp: addr=%q hevc=%v", got.WorkerAddr, got.HEVCOutput)
	}
	if got.LastActivityAt.IsZero() {
		t.Error("the stamp itself was lost")
	}

	// A session deleted after the read stays deleted.
	snap, _ = store.GetSnapshot(ctx, sess.ID)
	if err := store.Delete(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	old := activityStampInterval
	activityStampInterval = 0
	t.Cleanup(func() { activityStampInterval = old })
	store.StampActivity(ctx, snap)
	if _, err := store.Get(ctx, sess.ID); !errors.Is(err, valkey.ErrNotFound) {
		t.Errorf("stamp resurrected a deleted session: err = %v", err)
	}
}

// Callers without a snapshot (the playlist and rung-wait polls, every
// 100–150 ms) stamp at most once per interval per instance.
func TestTouchActivity_ThrottledPerSession(t *testing.T) {
	v, rt := countedValkey(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	sess := Session{ID: "touch-1", UserID: uuid.New(), CreatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	first := rt.during(func() { store.TouchActivity(ctx, sess.ID) })
	if first == 0 {
		t.Fatal("first touch wrote nothing")
	}
	if n := rt.during(func() {
		for i := 0; i < 20; i++ {
			store.TouchActivity(ctx, sess.ID)
		}
	}); n != 0 {
		t.Errorf("20 touches inside the interval: %d round trips, want 0", n)
	}
	// A deleted session's throttle is dropped with it.
	_ = store.Delete(ctx, sess.ID)
	if _, ok := store.stamps.last[sess.ID]; ok {
		t.Error("Delete left the throttle entry behind")
	}
}

// ── per-user session index ───────────────────────────────────────────────────

func TestListByUser_UsesTheUsersIndexAndHeals(t *testing.T) {
	v, rt := countedValkey(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	item := uuid.New()

	for i := 0; i < 2; i++ {
		_ = store.Create(ctx, Session{ID: NewSessionID(), UserID: user, MediaItemID: item, CreatedAt: time.Now()})
	}
	for i := 0; i < 20; i++ {
		_ = store.Create(ctx, Session{ID: NewSessionID(), UserID: other, MediaItemID: item, CreatedAt: time.Now()})
	}
	_, _ = store.ListByUser(ctx, user) // the one-time backfill

	var got []Session
	if n := rt.during(func() { got, _ = store.ListByUser(ctx, user) }); n != 2 {
		t.Errorf("ListByUser: %d round trips, want 2 (index + one pipelined read)", n)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want the user's 2 (not the other user's 20)", len(got))
	}

	// An ID-only Delete leaves the index entry; the next read drops it.
	if err := store.Delete(ctx, got[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ = store.ListByUser(ctx, user)
	if len(got) != 1 {
		t.Fatalf("after Delete: %d sessions, want 1", len(got))
	}
	if n := v.Raw().SCard(ctx, userSessionsKey(user)).Val(); n != 1 {
		t.Errorf("index still holds %d entries after the read, want the stale one dropped", n)
	}

	// The index outlives its members: a mutation renews it with them.
	_ = v.Raw().Expire(ctx, userSessionsKey(user), time.Minute)
	if err := store.SetWorkerInfo(ctx, got[0].ID, "w", "a:1", false, false); err != nil {
		t.Fatal(err)
	}
	if ttl := v.Raw().TTL(ctx, userSessionsKey(user)).Val(); ttl < sessionTTL-time.Minute {
		t.Errorf("index TTL after a mutation = %v, want ~%v", ttl, sessionTTL)
	}
}

// Sessions written before the index existed (a build from before this one,
// still streaming across the upgrade) are found: the first per-user read of
// the process backfills every stored session into its owner's index.
func TestListByUser_BackfillsSessionsMissingFromTheIndex(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user, item := uuid.New(), uuid.New()
	sess := Session{ID: "legacy-1", UserID: user, MediaItemID: item, CreatedAt: time.Now()}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	// What an older build's Create left: no user index at all.
	_ = v.Raw().Del(ctx, userSessionsKey(user))

	got, err := store.ListByUserItem(ctx, user, item)
	if err != nil || len(got) != 1 || got[0].ID != sess.ID {
		t.Fatalf("ListByUserItem = %v, %v; want the pre-index session", got, err)
	}
	deleted, err := store.DeleteByMedia(ctx, user, item)
	if err != nil || len(deleted) != 1 {
		t.Fatalf("DeleteByMedia = %v, %v; want the pre-index session deleted", deleted, err)
	}
	if _, err := store.Get(ctx, sess.ID); !errors.Is(err, valkey.ErrNotFound) {
		t.Errorf("session still stored: %v", err)
	}
}

// DeleteSessions applies the owner + item scope itself, whatever it is handed.
func TestDeleteSessions_ScopedAndUnindexed(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user, item := uuid.New(), uuid.New()
	mine := Session{ID: "mine", UserID: user, MediaItemID: item, CreatedAt: time.Now()}
	theirs := Session{ID: "theirs", UserID: uuid.New(), MediaItemID: item, CreatedAt: time.Now()}
	otherItem := Session{ID: "other-item", UserID: user, MediaItemID: uuid.New(), CreatedAt: time.Now()}
	for _, s := range []Session{mine, theirs, otherItem} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := store.DeleteSessions(ctx, user, item, []Session{mine, theirs, otherItem})
	if err != nil || len(deleted) != 1 || deleted[0].ID != "mine" {
		t.Fatalf("deleted = %v, %v; want only the owner's session for the item", deleted, err)
	}
	if v.Raw().SIsMember(ctx, userSessionsKey(user), "mine").Val() {
		t.Error("deleted session left in the owner's index")
	}
	for _, id := range []string{"theirs", "other-item"} {
		if _, err := store.Get(ctx, id); err != nil {
			t.Errorf("%s was deleted: %v", id, err)
		}
	}
}

// The beacon's position goes to the playback's own session, not one of its
// internal rung children.
func TestUpdatePositionOf_PrefersTheParent(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user, item := uuid.New(), uuid.New()
	child := Session{ID: "p-r720p", ParentID: "p", UserID: user, MediaItemID: item, CreatedAt: time.Now()}
	parent := Session{ID: "p", ABR: true, UserID: user, MediaItemID: item, CreatedAt: time.Now()}
	for _, s := range []Session{child, parent} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdatePositionOf(ctx, user, item, []Session{child, parent}, 61_000); err != nil {
		t.Fatal(err)
	}
	p, _ := store.Get(ctx, "p")
	c, _ := store.Get(ctx, "p-r720p")
	if p.PositionMS != 61_000 || c.PositionMS != 0 {
		t.Errorf("parent=%d child=%d, want the parent updated", p.PositionMS, c.PositionMS)
	}
}
