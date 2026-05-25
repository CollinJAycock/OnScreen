// Package db provides helpers for building PostgreSQL connection pools.
package db

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgxpool.Pool with production-ready defaults (ADR-021) and
// verifies connectivity with a ping. A multi-host DSN
// (host1,host2:5432/db?target_session_attrs=read-write) enables Postgres
// failover: pgx picks the read-write primary at connect time and re-homes to a
// promoted primary after a failover (ADR-033).
func NewPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
	cfg, err := buildPoolConfig(connStr)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	// Verify connectivity.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	return pool, nil
}

// buildPoolConfig parses connStr and applies OnScreen's pool tuning. Split from
// NewPool so the tuning — including the HA-DSN detection below — is unit-testable
// without a live database.
func buildPoolConfig(connStr string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}

	// Keep the pool small to avoid exhausting PostgreSQL's connection slots
	// when the container is hard-killed (connections orphan until TCP keepalive
	// expires). Two pools (rw + ro) × 20 = 40 max — well within the default
	// PostgreSQL max_connections of 100.
	cpus := runtime.NumCPU()
	maxConns := int32(cpus * 2)
	if maxConns < 4 {
		maxConns = 4
	}
	if maxConns > 20 {
		maxConns = 20
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = min(int32(cpus), maxConns/2)
	cfg.MaxConnIdleTime = 3 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// Connection lifetime. A multi-host DSN is an HA topology (pgconn records the
	// extra hosts as Fallbacks): pgx selects the read-write primary at connect
	// time, but an existing connection to a *gracefully* demoted primary stays
	// usable-yet-read-only until it's recycled. Shorten the lifetime in that case
	// so writes re-home to the promoted primary within ~1 min after a switchover.
	// A crash failover recovers at once (broken connections are evicted
	// immediately); this only bounds the graceful-switchover window. Single-host
	// deployments keep the long lifetime to minimise reconnect churn.
	if len(cfg.ConnConfig.Fallbacks) > 0 {
		cfg.MaxConnLifetime = 60 * time.Second
	} else {
		cfg.MaxConnLifetime = 15 * time.Minute
	}

	// OTel tracing on every query. When no tracer provider is registered, the
	// global no-op provider is used, so this is free at runtime. Query
	// parameters are excluded from spans to keep PII out of traces.
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithTrimSQLInSpanName(),
	)

	return cfg, nil
}

// PingablePool wraps pgxpool.Pool to satisfy the observability.Pinger interface.
type PingablePool struct {
	Pool *pgxpool.Pool
}

// Ping pings the database.
func (p *PingablePool) Ping(ctx context.Context) error {
	return p.Pool.Ping(ctx)
}

// MaxAppliedVersion returns the highest goose-applied migration version, or
// 0 if the goose_db_version table does not exist (fresh DB before any
// migration has been recorded). Anything else is a real error.
func (p *PingablePool) MaxAppliedVersion(ctx context.Context) (int64, error) {
	var v int64
	err := p.Pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version`).Scan(&v)
	if err != nil {
		// goose hasn't initialised the table yet — treat as 0 applied so the
		// readiness check reports pending instead of erroring.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}
