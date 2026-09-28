-- +goose Up
-- +goose StatementBegin
-- Season-level TV requests. A show request names the seasons it wants
-- (media_requests.seasons, a JSON array; NULL = every season), and a user may
-- now hold several active requests for one show as long as their seasons
-- don't overlap — "request more seasons" for a show already partly in the
-- library. The overlap rule is enforced by requests.Service.Create; the
-- unique indexes below keep the part a btree can express.
--
--   seasons_available: the requested seasons that are complete (every aired
--       episode on disk), in ascending order. Filled by the fulfilment
--       reconciler one season at a time with a conditional append, so each
--       season's "now available" notice is sent exactly once. The request
--       itself only turns 'available' when every requested season is
--       complete; until then the status is unchanged (clients parse the
--       status enum strictly, so partial availability is this column, not a
--       new status value).
ALTER TABLE public.media_requests
    ADD COLUMN seasons_available integer[] DEFAULT '{}'::integer[] NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- One active request per user and movie, and one active "all seasons"
-- request per user and show. Requests naming specific seasons are left to
-- the service's overlap check (a btree can't compare season sets).
DROP INDEX IF EXISTS public.media_requests_unique_active;
CREATE UNIQUE INDEX media_requests_unique_active
    ON public.media_requests USING btree (user_id, type, tmdb_id)
    WHERE status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text])
      AND (type = 'movie'::text OR seasons IS NULL);
-- +goose StatementEnd

-- +goose StatementBegin
-- …except the part a btree can compare: one active request per user, show
-- and exact season set. The service's overlap check is a read-then-insert, so
-- without this a double submit or a client retry creates two identical active
-- requests (two Sonarr adds, two sets of notices). The service stores seasons
-- sorted and de-duplicated, and jsonb equality ignores formatting, so equal
-- selections compare equal. Two racing requests for different but
-- overlapping season sets still both get in — the residual the service's
-- check can't close.
CREATE UNIQUE INDEX media_requests_unique_active_seasons
    ON public.media_requests USING btree (user_id, tmdb_id, seasons)
    WHERE status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text])
      AND type = 'show'::text AND seasons IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- The fulfilment lookup by title (ListActiveMediaRequestsForTMDB,
-- MarkMediaRequestAvailable) now includes failed requests — a file landing
-- after a failed download still fulfils them — so the partial index serving
-- it has to cover 'failed' too, or the planner can't use it.
DROP INDEX IF EXISTS public.media_requests_tmdb_lookup;
CREATE INDEX media_requests_tmdb_lookup
    ON public.media_requests USING btree (type, tmdb_id)
    WHERE status = ANY (ARRAY['approved'::text, 'downloading'::text, 'failed'::text]);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The old index allows a single active request per user and title. Keep each
-- user's oldest active request for a show and decline the later season
-- requests, rather than fail the rollback on the restored constraint.
UPDATE public.media_requests AS mr
SET status         = 'declined',
    decline_reason = 'superseded: season requests were rolled back',
    decided_at     = NOW(),
    updated_at     = NOW()
WHERE mr.status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text])
  AND EXISTS (
      SELECT 1 FROM public.media_requests o
      WHERE o.user_id = mr.user_id
        AND o.type = mr.type
        AND o.tmdb_id = mr.tmdb_id
        AND o.status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text])
        AND (o.created_at, o.id) < (mr.created_at, mr.id)
  );
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS public.media_requests_tmdb_lookup;
CREATE INDEX media_requests_tmdb_lookup
    ON public.media_requests USING btree (type, tmdb_id)
    WHERE status = ANY (ARRAY['approved'::text, 'downloading'::text]);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS public.media_requests_unique_active_seasons;
DROP INDEX IF EXISTS public.media_requests_unique_active;
CREATE UNIQUE INDEX media_requests_unique_active
    ON public.media_requests USING btree (user_id, type, tmdb_id)
    WHERE status = ANY (ARRAY['pending'::text, 'approved'::text, 'downloading'::text]);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE public.media_requests
    DROP COLUMN IF EXISTS seasons_available;
-- +goose StatementEnd
