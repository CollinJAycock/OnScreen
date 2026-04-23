-- +goose Up
-- session_epoch is bumped whenever a user's authorization context changes
-- (admin demotion, delete, force-logout). The PASETO access token
-- encodes the epoch at issue time; the auth middleware rejects tokens
-- whose epoch doesn't match the current row. This closes a window where
-- demoted admins keep admin privileges until their 1h token expires.
--
-- Stored as BIGINT (not a timestamp) so it's a monotonic counter — avoids
-- clock-skew bugs and keeps comparisons cheap.

ALTER TABLE users ADD COLUMN session_epoch BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN session_epoch;
