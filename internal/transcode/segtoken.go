package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/valkey"
)

const segTokenTTL = 4 * time.Hour

// ErrInvalidSegmentToken is the answer for a token that doesn't exist (never
// issued, expired, revoked) or, from Authorize, is bound to another session.
var ErrInvalidSegmentToken = errors.New("invalid or expired segment token")

// segTokenData is stored in Valkey under each segment token.
type segTokenData struct {
	SessionID string    `json:"session_id"`
	UserID    uuid.UUID `json:"user_id"`
}

// SegmentTokenManager issues and validates short-lived HLS segment auth tokens (ADR-019).
type SegmentTokenManager struct {
	v *valkey.Client
}

// NewSegmentTokenManager creates a SegmentTokenManager.
func NewSegmentTokenManager(v *valkey.Client) *SegmentTokenManager {
	return &SegmentTokenManager{v: v}
}

// Issue creates a new segment token for the given session and user.
// The token is stored in Valkey with a 4-hour TTL. The token is also
// added to a per-user index set so RevokeAllForUser can wipe every
// outstanding token when the user's session_epoch is bumped (password
// reset, admin demote). The index set is given the same TTL so a
// long-idle user doesn't leave an unbounded set behind in Valkey.
func (m *SegmentTokenManager) Issue(ctx context.Context, sessionID string, userID uuid.UUID) (string, error) {
	token := uuid.New().String()
	data := segTokenData{SessionID: sessionID, UserID: userID}
	b, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal seg token: %w", err)
	}
	if err := m.v.Set(ctx, segTokenKey(token), string(b), segTokenTTL); err != nil {
		return "", fmt.Errorf("store seg token: %w", err)
	}
	idx := userIndexKey(userID)
	if err := m.v.SAdd(ctx, idx, token); err != nil {
		// Best-effort: a missing index entry just means RevokeAllForUser
		// won't see this token. The token still expires on its own TTL.
		// Don't fail the Issue call — playback would 503.
		_ = err
	} else {
		_ = m.v.Expire(ctx, idx, segTokenTTL)
	}
	return token, nil
}

// segTokenValidateScript reads a segment token and refreshes both its TTL and
// its owning user's index TTL in one step.
//
// The TTL is an IDLE timeout, not an absolute lifetime. With a fixed expiry, a
// stream that outlived segTokenTTL — a film marathon, or a long pause — had
// every subsequent segment fetch 401 at the four-hour mark even though the
// session itself was alive and encoding. A revoked token stays revoked (the
// key is deleted, not expired), and an ABANDONED token still ages out
// segTokenTTL after its last use.
//
// The OWNING INDEX is refreshed on the same beat. Issue gives the index a
// one-shot TTL, and while only the token key was refreshed the index expired
// out from under a token that was still valid on any stream that outlived
// segTokenTTL. That made RevokeAllForUser silently useless: SMembers on a
// missing key returns an empty slice and a nil error, so every caller —
// password reset, admin demote, content-rating tighten — got "success" having
// revoked nothing, and the orphaned token kept streaming. The index has to
// live at least as long as the tokens it indexes.
//
// This used to be a GET and two separate EXPIREs: three round trips on every
// playlist and segment fetch. The index key depends on the user stored in the
// token, so the client couldn't pipeline the refreshes behind the GET; the
// script derives it server-side, which is fine because OnScreen's Valkey is a
// single node or a Sentinel-managed primary, never Cluster.
//
// KEYS[1] = the token key. ARGV[1] = TTL in ms, ARGV[2] = user-index key
// prefix. Returns the token's stored JSON, or nil when the token is gone.
var segTokenValidateScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
	return false
end
redis.call('PEXPIRE', KEYS[1], ARGV[1])
local ok, data = pcall(cjson.decode, raw)
if ok and type(data) == 'table' and type(data.user_id) == 'string' then
	redis.call('PEXPIRE', ARGV[2] .. data.user_id, ARGV[1])
end
return raw
`)

func segTokenScriptArgs(token string) ([]string, []any) {
	return []string{segTokenKey(token)}, []any{segTokenTTL.Milliseconds(), userIndexKeyPrefix}
}

// Validate looks up a segment token and returns the associated session and
// user, refreshing the token's and its user index's TTLs in the same round
// trip (segTokenValidateScript).
func (m *SegmentTokenManager) Validate(ctx context.Context, token string) (sessionID string, userID uuid.UUID, err error) {
	keys, args := segTokenScriptArgs(token)
	data, err := decodeSegToken(segTokenValidateScript.Run(ctx, m.v.Raw(), keys, args...).Text())
	if err != nil {
		return "", uuid.UUID{}, err
	}
	return data.SessionID, data.UserID, nil
}

// SegmentAccess is what one playlist or segment request learned from its
// authorization round trip: whose token it carried, and the sessions it asked
// to read alongside the check.
type SegmentAccess struct {
	UserID uuid.UUID
	// Sessions has one entry per ID Authorize was asked to read, in order. An
	// entry is nil when that session's key is gone (stopped, expired) or
	// can't be decoded.
	Sessions []*SessionSnapshot
}

// Authorize validates token — it must exist and be bound to boundSession —
// and reads the sessions named in read, all in ONE pipelined round trip.
//
// Every playlist and segment fetch used to validate the token (a GET and two
// EXPIREs) and then read its session in a separate GET: four round trips
// before a byte was served. Reading the sessions in the same pipeline as the
// check means they are never staler than the check itself — a session stopped
// or a token revoked before this call is seen by it, which a per-instance
// session cache could not promise across API instances.
func (m *SegmentTokenManager) Authorize(ctx context.Context, token, boundSession string, read ...string) (*SegmentAccess, error) {
	keys, args := segTokenScriptArgs(token)
	run := func(byHash bool) (*redis.Cmd, []*redis.StringCmd) {
		pipe := m.v.Raw().Pipeline()
		var tok *redis.Cmd
		if byHash {
			tok = segTokenValidateScript.EvalSha(ctx, pipe, keys, args...)
		} else {
			tok = segTokenValidateScript.Eval(ctx, pipe, keys, args...)
		}
		gets := make([]*redis.StringCmd, len(read))
		for i, id := range read {
			gets[i] = pipe.Get(ctx, sessionKey(id))
		}
		// Exec reports the first failed command, and a missing session is a
		// failed command (redis.Nil); each one is inspected on its own below.
		_, _ = pipe.Exec(ctx)
		return tok, gets
	}
	tok, gets := run(true)
	if redis.HasErrorPrefix(tok.Err(), "NOSCRIPT") {
		// Valkey's script cache is empty after a restart or a failover. EVAL
		// loads the script, so this second trip happens once per server.
		tok, gets = run(false)
	}
	data, err := decodeSegToken(tok.Text())
	if err != nil {
		return nil, err
	}
	if data.SessionID != boundSession {
		// A token issued for session A must not open session B.
		return nil, ErrInvalidSegmentToken
	}
	acc := &SegmentAccess{UserID: data.UserID, Sessions: make([]*SessionSnapshot, len(read))}
	for i, g := range gets {
		raw, err := g.Result()
		if err != nil {
			continue
		}
		if snap, err := decodeSnapshot(raw); err == nil {
			acc.Sessions[i] = snap
		}
	}
	return acc, nil
}

// decodeSegToken turns the validate script's reply into the token's data.
func decodeSegToken(raw string, err error) (segTokenData, error) {
	if errors.Is(err, redis.Nil) {
		return segTokenData{}, ErrInvalidSegmentToken
	}
	if err != nil {
		return segTokenData{}, fmt.Errorf("validate seg token: %w", err)
	}
	var data segTokenData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return segTokenData{}, fmt.Errorf("decode seg token: %w", err)
	}
	return data, nil
}

// Revoke deletes a segment token immediately (called on session stop).
// We don't have the userID here so the index entry is left as a dangling
// reference — RevokeAllForUser tolerates that by treating Del of a missing
// token-key as a no-op.
func (m *SegmentTokenManager) Revoke(ctx context.Context, token string) error {
	return m.v.Del(ctx, segTokenKey(token))
}

// RevokeAllForUser deletes every outstanding segment token for the given
// user. Called from credential-rotation paths (password reset, admin demote)
// so an active HLS playback can't outlive the access-token revocation.
//
// Returns nil even when the user has no live tokens — the post-condition
// "no live segment tokens for this user" is satisfied either way.
func (m *SegmentTokenManager) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	idx := userIndexKey(userID)
	tokens, err := m.v.SMembers(ctx, idx)
	if err != nil {
		return fmt.Errorf("list user seg tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tokens)+1)
	for _, t := range tokens {
		keys = append(keys, segTokenKey(t))
	}
	keys = append(keys, idx)
	if err := m.v.Del(ctx, keys...); err != nil {
		return fmt.Errorf("delete user seg tokens: %w", err)
	}
	return nil
}

// userIndexKeyPrefix prefixes the per-user token index (userIndexKey); the
// validate script appends the user ID itself.
const userIndexKeyPrefix = "user_segtokens:"

func segTokenKey(token string) string { return "segment_token:" + token }

func userIndexKey(userID uuid.UUID) string { return userIndexKeyPrefix + userID.String() }
