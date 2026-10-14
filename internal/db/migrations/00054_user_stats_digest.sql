-- +goose Up
-- +goose StatementBegin
-- A user's own stats (My stats, Year in review) and the digest email.
--
-- The per-user stats read the watch_plays analytics matview filtered to one
-- user over a date range. Its existing indexes lead with occurred_at /
-- media_id, so one user's history meant a range scan over everyone's plays;
-- (user_id, occurred_at) makes the per-user aggregates an index range scan
-- however long the shared history grows. Plain CREATE INDEX: the matview is
-- rebuilt by REFRESH ... CONCURRENTLY, which keeps its indexes.
CREATE INDEX watch_plays_user_occurred_idx ON public.watch_plays (user_id, occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Opt-in digest email ("Email me a digest"). No row = off, which is every
-- user until they opt in on their Account page.
--
--   frequency    'off' | 'weekly' | 'monthly'
--   last_period  the period the last digest covered ('2026-W42' for a week,
--                '2026-10' for a month). The send task skips a user whose
--                last_period is the current one, so a retried or overlapping
--                run never sends a period twice.
--   last_sent_at when that digest went out (shown on the Account page).
--
-- The row is kept when the user turns the digest off (frequency = 'off') so
-- turning it back on mid-period doesn't resend the period already mailed.
CREATE TABLE public.user_digest_prefs (
    user_id      uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    frequency    text NOT NULL DEFAULT 'off',
    last_period  text,
    last_sent_at timestamp with time zone,
    updated_at   timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_digest_prefs_frequency_check
        CHECK (frequency = ANY (ARRAY['off'::text, 'weekly'::text, 'monthly'::text]))
);

-- The send task lists only opted-in users.
CREATE INDEX idx_user_digest_prefs_on ON public.user_digest_prefs USING btree (frequency)
    WHERE frequency <> 'off';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_digest_prefs;
DROP INDEX IF EXISTS public.watch_plays_user_occurred_idx;
-- +goose StatementEnd
