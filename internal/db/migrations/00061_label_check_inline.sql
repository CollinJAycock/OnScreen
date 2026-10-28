-- +goose Up
-- +goose StatementBegin
-- media_item_labels_permitted (00047) is plpgsql, so the planner can't see
-- inside it: every row a hub / library / memories query looks at paid a
-- function call even for a caller with no label restriction (every admin and
-- almost every user), where the early RETURN true made the call pure
-- overhead. On QA's ~110k-item catalog that per-row call was a large share of
-- the home page's "Recently added" query.
--
-- Split it: the ancestor walk moves, body unchanged, to
-- media_item_labels_walk, and media_item_labels_permitted becomes a
-- one-expression SQL function the planner inlines. With no restriction the
-- inlined check folds to true (or, in a generic plan, short-circuits before
-- the walk), so unrestricted callers no longer call any function per row.
-- Restricted callers get the same walk as before. Signature and semantics
-- are unchanged, so no query or caller changes; media_item_id_labels_permitted
-- calls it by name. (CREATE OR REPLACE rather than ALTER ... RENAME: sqlc's
-- schema parser doesn't follow a function rename.)
CREATE FUNCTION public.media_item_labels_walk(item_labels text[], item_parent uuid, blocked text[], allowed text[])
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
CREATE OR REPLACE FUNCTION public.media_item_labels_permitted(item_labels text[], item_parent uuid, blocked text[], allowed text[])
    RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
    SELECT (COALESCE(cardinality(blocked), 0) = 0 AND COALESCE(cardinality(allowed), 0) = 0)
        OR public.media_item_labels_walk(item_labels, item_parent, blocked, allowed)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.media_item_labels_permitted(item_labels text[], item_parent uuid, blocked text[], allowed text[])
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
DROP FUNCTION public.media_item_labels_walk(text[], uuid, text[], text[]);
-- +goose StatementEnd
