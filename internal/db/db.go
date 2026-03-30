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

	cpus := runtime.NumCPU()
	cfg.MaxConns = int32(cpus * 4)
	cfg.MinConns = int32(cpus)
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

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
