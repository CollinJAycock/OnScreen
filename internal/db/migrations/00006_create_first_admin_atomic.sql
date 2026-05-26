-- +goose Up
-- +goose StatementBegin
--
-- Atomic "first user is admin" bootstrap, take 2.
--
-- The previous query (a single statement with a CTE that called
-- pg_advisory_xact_lock + an INSERT...WHERE NOT EXISTS) closed *most* of
-- the race but not all of it: in Read Committed, the entire statement
-- evaluates against ONE snapshot taken at statement start. The CTE's
-- advisory-lock call happens during execution — after the snapshot is
-- frozen — so a goroutine that loses the lock race waits for the
-- winner's commit, then proceeds with its stale snapshot in which the
-- winner's row is still invisible. NOT EXISTS evaluates false, the
-- INSERT lands, two admins exist. Test
-- TestCreateFirstAdmin_Integration_AtomicUnderConcurrency reproduces
-- this ~1 run in 4.
--
-- The fix: do it in PL/pgSQL where each procedural statement gets its
-- own fresh snapshot. After PERFORM pg_advisory_xact_lock returns, the
-- subsequent SELECT count(*) FROM users takes a NEW snapshot that
-- includes any row a previously-blocking transaction just committed.
--
-- Returns SETOF users: exactly one row on win, zero rows on race loss
-- (the caller's :one sqlc query surfaces an empty result as
-- pgx.ErrNoRows, matching the previous statement-form's contract — no
-- Go-side changes needed). Argument names are deliberately bare
-- (`username` not `p_username`) so sqlc keeps the existing CamelCase
-- field names (Username, Email, PasswordHash) on the Go params struct.
-- The VALUES clause uses positional refs ($1/$2/$3) to disambiguate
-- against the same-named columns; PL/pgSQL would also resolve the bare
-- names to the params, but $N is clearer at a glance.
CREATE OR REPLACE FUNCTION public.create_first_admin_atomic(
    username      text,
    email         text,
    password_hash text
) RETURNS SETOF public.users
LANGUAGE plpgsql AS $$
DECLARE
    existing_count bigint;
BEGIN
    -- Serialize concurrent bootstrap attempts on a single advisory key.
    -- The lock is transaction-scoped (held until COMMIT/ROLLBACK), so the
    -- caller doesn't need to release it explicitly.
    PERFORM pg_advisory_xact_lock(hashtext('onscreen:first_admin'));

    -- Fresh snapshot: any prior winner has already committed (we waited
    -- on their lock above), so we'll see their row here even though we
    -- couldn't have seen it inside the previous single-statement form.
    SELECT count(*) INTO existing_count FROM public.users;
    IF existing_count > 0 THEN
        -- Race loser: return empty set. Caller (sqlc :one) surfaces this
        -- as pgx.ErrNoRows, matching the previous query's contract.
        RETURN;
    END IF;

    RETURN QUERY
        INSERT INTO public.users (username, email, password_hash, is_admin)
        -- NULLIF($2, '') preserves the original semantic where an unset
        -- email was inserted as NULL (so the partial unique index on
        -- email-when-not-null doesn't collide across blank-email users).
        -- Password hash is always set by the caller, no NULLIF needed.
        VALUES ($1, NULLIF($2, ''), $3, true)
        RETURNING *;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS public.create_first_admin_atomic(text, text, text);
-- +goose StatementEnd
