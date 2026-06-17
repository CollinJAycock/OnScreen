-- +goose Up
-- +goose StatementBegin
-- Align the SQL content_rating_rank() ladder with internal/contentrating.Rank
-- in Go. The Go ladder gained anime ratings (R-15, R-17+, R+, R-18+, Rx) but the
-- SQL function (from 00001) had no arms for them, so they fell through to ELSE 4
-- (most-restrictive). Because per-item rank is computed in SQL while the per-user
-- ceiling (max_rating_rank) is computed in Go, an anime item rated at or below a
-- profile's anime ceiling was wrongly hidden from every SQL-gated list/hub query
-- (continue-watching, genre rows, trending, history, collections) while staying
-- visible/playable through the Go-gated direct handlers. CREATE OR REPLACE so the
-- IMMUTABLE function is updated in place; the rank values mirror Rank() exactly.
CREATE OR REPLACE FUNCTION public.content_rating_rank(rating text) RETURNS integer
    LANGUAGE plpgsql IMMUTABLE
    AS $$
BEGIN
  IF rating IS NULL OR rating = '' THEN
    RETURN 4;  -- treat unrated as most restrictive
  END IF;
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2  WHEN 'R-15' THEN 2
    WHEN 'R'     THEN 3  WHEN 'R-17+' THEN 3
    WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3  WHEN 'R+'   THEN 3  WHEN 'R-18+' THEN 3
    -- Rx (hentai): most-restrictive bucket so a TV-MA/NC-17 ceiling still blocks it.
    WHEN 'Rx'    THEN 4
    ELSE 4  -- NR, UNRATED, X, empty, etc.
  END;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restore the pre-anime ladder from migration 00001.
CREATE OR REPLACE FUNCTION public.content_rating_rank(rating text) RETURNS integer
    LANGUAGE plpgsql IMMUTABLE
    AS $$
BEGIN
  IF rating IS NULL OR rating = '' THEN
    RETURN 4;  -- treat unrated as most restrictive
  END IF;
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2
    WHEN 'R'     THEN 3
    WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3
    ELSE 4  -- NR, UNRATED, X, empty, etc.
  END;
END;
$$;
-- +goose StatementEnd
