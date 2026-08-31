-- +goose Up
-- +goose StatementBegin
-- Refresh-token reuse detection needs to recognise a SUPERSEDED token, not
-- just an unknown one.
--
-- Rotation rewrites sessions.token_hash in place, so once a token is rotated
-- its old hash matches no row at all. Refresh looks the session up by the
-- presented hash and bails on a miss, which means the reuse-detection branch
-- (the compare-and-swap returning 0 rows) was only reachable in the
-- sub-millisecond window where two requests both completed their SELECT
-- before either UPDATE committed. The actual theft case — attacker refreshes
-- with a stolen token, victim's client presents the now-superseded one
-- minutes later — looked identical to garbage input: a plain 401, no session
-- wipe, no epoch bump, no warning, no audit row. The victim simply signs in
-- again and the attacker's chain survives indefinitely.
--
-- Keeping one generation of history lets GetSessionByTokenHash resolve a
-- superseded token to its session and take the theft branch that is already
-- written. One generation is enough: presenting a token two rotations old is
-- indistinguishable from an unknown token either way, and the legitimate
-- client only ever holds the newest.
ALTER TABLE public.sessions ADD COLUMN IF NOT EXISTS prev_token_hash text;

-- Absolute lifetime. Rotation resets expires_at to now+30d with no ceiling,
-- so a continuously-refreshed chain never expires. Anchoring the cap to the
-- row's own created_at bounds a stolen-and-kept-warm session without
-- disturbing the sliding window for normal use.
ALTER TABLE public.sessions ADD COLUMN IF NOT EXISTS absolute_expires_at timestamp with time zone;

-- Backfill so existing sessions get a cap rather than an open-ended one.
UPDATE public.sessions
SET absolute_expires_at = created_at + interval '90 days'
WHERE absolute_expires_at IS NULL;

-- Lookup index for the superseded-hash probe. Partial: only rotated rows have
-- a previous hash, and this keeps the index off the majority that do not.
CREATE INDEX IF NOT EXISTS idx_sessions_prev_token_hash
  ON public.sessions (prev_token_hash)
  WHERE prev_token_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sessions_prev_token_hash;
ALTER TABLE public.sessions DROP COLUMN IF EXISTS absolute_expires_at;
ALTER TABLE public.sessions DROP COLUMN IF EXISTS prev_token_hash;
-- +goose StatementEnd
