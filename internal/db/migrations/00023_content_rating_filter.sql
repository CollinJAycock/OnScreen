-- +goose Up
ALTER TABLE users ADD COLUMN max_content_rating TEXT;

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

-- +goose Down
DROP FUNCTION IF EXISTS content_rating_rank;
ALTER TABLE users DROP COLUMN IF EXISTS max_content_rating;
