-- +goose Up
-- +goose StatementBegin
-- Admin labels on media items and per-user label restrictions (Plex's label
-- filters, Jellyfin's blocked / allowed tags).
--
-- media_items.labels is separate from media_items.tags on purpose: tags are
-- provider keywords that every metadata refresh overwrites, so a restriction
-- keyed on them could be lifted (or imposed) silently by a refresh. labels
-- are written only by the admin label API and never by the scanner or the
-- enricher. Values are stored normalised (lower-case, single-spaced; see
-- internal/itemlabel) so comparisons here can be plain array overlap.
ALTER TABLE public.media_items
    ADD COLUMN labels text[] DEFAULT '{}'::text[] NOT NULL;

-- Partial: almost every row has no labels, and the labels-in-use list (the
-- admin autocomplete) only ever reads the rows that have some.
CREATE INDEX idx_media_items_labeled ON public.media_items USING btree (id)
    WHERE labels <> '{}'::text[];

-- blocked_labels: the user never sees an item carrying any of these.
-- allowed_labels: when non-empty, the user sees ONLY items carrying at least
-- one of these. Both are copied into managed profiles at creation and cascade
-- on update, like max_content_rating.
ALTER TABLE public.users
    ADD COLUMN blocked_labels text[] DEFAULT '{}'::text[] NOT NULL,
    ADD COLUMN allowed_labels text[] DEFAULT '{}'::text[] NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- media_item_labels_permitted is the SQL half of the label restriction; every
-- query that applies the content-rating ceiling also calls this. It takes the
-- row's own labels and parent_id (not its id) so a movie — no parent — costs
-- no lookup at all, and short-circuits before any lookup when the caller has
-- no restriction, which is every unrestricted user.
--
-- A row's effective labels are its own plus those of every ancestor (episode
-- → season → show, track → album → artist, chapter → audiobook → series →
-- author), which is how Jellyfin applies tags: labelling a show hides every
-- episode, and a label on a single episode affects that episode alone. The
-- walk is capped at four levels, deeper than any hierarchy we build.
CREATE FUNCTION public.media_item_labels_permitted(item_labels text[], item_parent uuid, blocked text[], allowed text[])
    RETURNS boolean
    LANGUAGE plpgsql STABLE PARALLEL SAFE
    AS $$
DECLARE
    eff   text[] := COALESCE(item_labels, '{}'::text[]);
    p     uuid   := item_parent;
    pl    text[];
    depth int    := 0;
BEGIN
    IF COALESCE(cardinality(blocked), 0) = 0 AND COALESCE(cardinality(allowed), 0) = 0 THEN
        RETURN true;
    END IF;
    WHILE p IS NOT NULL AND depth < 4 LOOP
        -- No row found sets both targets to NULL, which ends the walk.
        SELECT mi.labels, mi.parent_id INTO pl, p FROM public.media_items mi WHERE mi.id = p;
        eff := eff || COALESCE(pl, '{}'::text[]);
        depth := depth + 1;
    END LOOP;
    IF COALESCE(cardinality(blocked), 0) > 0 AND eff && blocked THEN
        RETURN false;
    END IF;
    IF COALESCE(cardinality(allowed), 0) > 0 AND NOT (eff && allowed) THEN
        RETURN false;
    END IF;
    RETURN true;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The same check for a caller holding only an item id (episode_ancestry rows,
-- the per-item gates in Go). A missing row is not permitted.
CREATE FUNCTION public.media_item_id_labels_permitted(p_item_id uuid, blocked text[], allowed text[])
    RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
    SELECT (COALESCE(cardinality(blocked), 0) = 0 AND COALESCE(cardinality(allowed), 0) = 0)
        OR COALESCE((SELECT public.media_item_labels_permitted(mi.labels, mi.parent_id, blocked, allowed)
                     FROM public.media_items mi WHERE mi.id = p_item_id), false);
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS public.media_item_id_labels_permitted(uuid, text[], text[]);
DROP FUNCTION IF EXISTS public.media_item_labels_permitted(text[], uuid, text[], text[]);
ALTER TABLE public.users
    DROP COLUMN IF EXISTS allowed_labels,
    DROP COLUMN IF EXISTS blocked_labels;
DROP INDEX IF EXISTS public.idx_media_items_labeled;
ALTER TABLE public.media_items DROP COLUMN IF EXISTS labels;
-- +goose StatementEnd
