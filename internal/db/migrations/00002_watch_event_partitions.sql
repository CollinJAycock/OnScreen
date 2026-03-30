-- +goose Up
-- +goose StatementBegin

-- Initial watch_event partitions: current month + 2 forward.
-- Future partitions are managed by the partition worker (ADR-002).
-- This migration is idempotent — the worker uses the same CREATE IF NOT EXISTS pattern.

CREATE TABLE IF NOT EXISTS watch_events_2026_03 PARTITION OF watch_events
    FOR VALUES FROM ('2026-03-01') TO ('2026-04-01');

CREATE TABLE IF NOT EXISTS watch_events_2026_04 PARTITION OF watch_events
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');

CREATE TABLE IF NOT EXISTS watch_events_2026_05 PARTITION OF watch_events
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS watch_events_2026_03;
DROP TABLE IF EXISTS watch_events_2026_04;
DROP TABLE IF EXISTS watch_events_2026_05;

-- +goose StatementEnd
