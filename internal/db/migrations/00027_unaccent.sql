-- +goose Up
-- unaccent lets dedupe queries fold diacritics so "Beyoncé" and "Beyonce"
-- normalize to the same key.
CREATE EXTENSION IF NOT EXISTS unaccent;

-- +goose Down
DROP EXTENSION IF EXISTS unaccent;
