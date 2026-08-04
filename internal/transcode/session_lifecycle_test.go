package transcode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

// ── concurrency cap vs ABR rung children ─────────────────────────────────────

// One ABR playback = one parent + N rung children, all under the same user.
// Only the parent may count toward the concurrent-stream cap: counting
// children made a single ABR stream eat 2+ of the default 5 slots, and two
// ABR playbacks could brick the account with "you already have 5 active
// streams".
func TestCountByUser_SkipsABRRungChildren(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user := uuid.New()

	parent := Session{
		ID: "abr-parent-x", UserID: user, MediaItemID: uuid.New(), FileID: uuid.New(),
		ABR: true, CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}
	if err := store.Create(ctx, parent); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []string{"1080p", "720p"} {
		child := Session{
			ID: "abr-parent-x-r" + rung, UserID: user, MediaItemID: parent.MediaItemID,
			FileID: parent.FileID, ParentID: parent.ID,
			CreatedAt: time.Now(), LastActivityAt: time.Now(),
		}
		if err := store.Create(ctx, child); err != nil {
			t.Fatal(err)
		}
	}

	n, err := store.CountByUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CountByUser = %d, want 1 — rung children are internal sessions, "+
			"not extra streams; counting them multiplies one playback across the cap", n)
	}
}

// ── TTL is an idle timeout, not an absolute lifetime ─────────────────────────

// A session being actively watched must never expire out from under its
// viewer. The TTL used to be absolute: mutations preserved the remaining
// countdown, so at exactly sessionTTL of wall-clock the key expired
// mid-stream, the worker's watchdog read not-found, and the ffmpeg was shot.
// Activity now refreshes the TTL to the full window.
func TestSessionTTL_RefreshedByActivity(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID: "ttl-refresh", UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(),
		CreatedAt: time.Now(), LastActivityAt: time.Now(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	// Simulate a session deep into its lifetime: squeeze the TTL down.
	if err := v.Raw().Expire(ctx, sessionKey(sess.ID), time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	// Any activity write must restore the full window.
	store.TouchActivity(ctx, sess.ID)

	ttl := v.Raw().TTL(ctx, sessionKey(sess.ID)).Val()
	if ttl < sessionTTL-time.Minute {
		t.Errorf("TTL after activity = %v, want ~%v — an active viewer three hours "+
			"into a marathon is on a countdown to a hard kill", ttl, sessionTTL)
	}
}

// Same property for the segment token: at the fixed 4 h expiry every segment
// fetch started 401ing even though the session was alive and encoding.
func TestSegToken_ValidateRefreshesTTL(t *testing.T) {
	v := testvalkey.New(t)
	mgr := NewSegmentTokenManager(v)
	ctx := context.Background()

	tok, err := mgr.Issue(ctx, "sess-1", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Raw().Expire(ctx, segTokenKey(tok), 30*time.Second).Err(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := mgr.Validate(ctx, tok); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	ttl := v.Raw().TTL(ctx, segTokenKey(tok)).Val()
	if ttl < segTokenTTL-time.Minute {
		t.Errorf("token TTL after Validate = %v, want ~%v — in-use tokens must not "+
			"expire mid-stream", ttl, segTokenTTL)
	}

	// Revocation must stay revocation: a revoked token is deleted, and
	// validating it must not resurrect anything.
	if err := mgr.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Validate(ctx, tok); err == nil {
		t.Error("revoked token validated")
	}
}

// ── UpdatePositionByMedia goes through the optimistic-lock mutator ───────────

// The beacon's position write must not clobber concurrent field stamps. It
// was the one remaining raw read-modify-write of the whole session blob: a
// beacon that read the session before SetWorkerInfo landed wrote the
// pre-stamp blob back, wiping WorkerAddr — the same lost-update the other
// mutators were converted away from. This pins the observable half: position
// lands, other fields survive, and a mismatched owner is untouched.
func TestUpdatePositionByMedia_PreservesConcurrentStamps(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	user := uuid.New()
	mediaID := uuid.New()

	sess := Session{
		ID: "pos-mutate", UserID: user, MediaItemID: mediaID, FileID: uuid.New(),
		CreatedAt: time.Now(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	// Worker stamps its identity (the write the raw RMW used to wipe).
	if err := store.SetWorkerInfo(ctx, sess.ID, "w1", "10.0.0.9:8090", true, false); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdatePositionByMedia(ctx, user, mediaID, 90_000); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PositionMS != 90_000 {
		t.Errorf("PositionMS = %d, want 90000", got.PositionMS)
	}
	if got.WorkerAddr != "10.0.0.9:8090" || !got.HEVCOutput {
		t.Errorf("beacon write lost the worker stamp: addr=%q hevc=%v", got.WorkerAddr, got.HEVCOutput)
	}
}

// ── temp_file on every HLS builder ───────────────────────────────────────────

// Segments must become visible by RENAME: every serving path treats "file
// exists" as "segment complete", so without temp_file a fetch racing ffmpeg's
// write served the truncated prefix of an in-progress segment with a 200.
func TestBuildHLS_SegmentsWrittenViaTempFile(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath: "/media/movie.mkv", Encoder: EncoderSoftware,
		Width: 1280, Height: 720, BitrateKbps: 4000, AudioCodec: "aac",
		SessionDir: "/tmp/s", SegmentPrefix: "seg",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "temp_file") {
		t.Errorf("BuildHLS -hls_flags missing temp_file:\n%s", joined)
	}

	remux := BuildDirectStream("/media/movie.mkv", "/tmp/s", 0)
	joined = strings.Join(remux, " ")
	if !strings.Contains(joined, "temp_file") {
		t.Errorf("direct-stream builder -hls_flags missing temp_file:\n%s", joined)
	}
}
