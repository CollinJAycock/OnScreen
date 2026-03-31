-- +goose Up
-- Fix content_rating_rank() to return 3 (R) for NULL/empty input so unrated
-- content is treated as R-rated, protecting restricted profiles.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION content_rating_rank(rating TEXT) RETURNS INT AS $$
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
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

-- +goose Down
-- Restore the original function (NULL/empty → 999).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION content_rating_rank(rating TEXT) RETURNS INT AS $$
BEGIN
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-G' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-PG' THEN 2
    WHEN 'R'     THEN 3  WHEN 'TV-14' THEN 3
    WHEN 'NC-17' THEN 4  WHEN 'TV-MA' THEN 4
    ELSE 999
  END;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd
