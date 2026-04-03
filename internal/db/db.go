// Package db provides helpers for building PostgreSQL connection pools.
package db

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgxpool.Pool with production-ready defaults (ADR-021).
// The pool is not yet connected — it connects lazily on first use.
func NewPool(ctx context.Context, connStr string) (*pgxpool.Pool, error) {
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
	cfg.MaxConnLifetime = 15 * time.Minute
	cfg.MaxConnIdleTime = 3 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

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

// PingablePool wraps pgxpool.Pool to satisfy the observability.Pinger interface.
type PingablePool struct {
	Pool *pgxpool.Pool
}

// Ping pings the database.
func (p *PingablePool) Ping(ctx context.Context) error {
	return p.Pool.Ping(ctx)
}
