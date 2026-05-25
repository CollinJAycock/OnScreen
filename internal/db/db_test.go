package db

import (
	"testing"
	"time"
)

// buildPoolConfig is the unit-testable half of NewPool (no live DB needed). The
// case that matters most for HA is the connection-lifetime branch: a multi-host
// DSN must shorten MaxConnLifetime so writes re-home promptly after a graceful
// Postgres switchover, while a single-host DSN keeps the long lifetime.

func TestBuildPoolConfig_SingleHostKeepsLongLifetime(t *testing.T) {
	cfg, err := buildPoolConfig("postgres://u:p@localhost:5432/onscreen?sslmode=disable")
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if got := len(cfg.ConnConfig.Fallbacks); got != 0 {
		t.Fatalf("single-host DSN should have 0 fallbacks, got %d", got)
	}
	if cfg.MaxConnLifetime != 15*time.Minute {
		t.Errorf("single-host MaxConnLifetime = %v, want 15m (minimise reconnect churn)", cfg.MaxConnLifetime)
	}
}

func TestBuildPoolConfig_MultiHostShortensLifetime(t *testing.T) {
	// Two hosts → pgconn records the second as a Fallback → HA topology.
	cfg, err := buildPoolConfig(
		"postgres://u:p@primary:5432,replica:5432/onscreen?target_session_attrs=read-write&sslmode=disable")
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if got := len(cfg.ConnConfig.Fallbacks); got == 0 {
		t.Fatal("multi-host DSN should record fallback hosts, got 0 (HA detection broken)")
	}
	if cfg.MaxConnLifetime != 60*time.Second {
		t.Errorf("multi-host MaxConnLifetime = %v, want 60s (re-home after switchover)", cfg.MaxConnLifetime)
	}
}

func TestBuildPoolConfig_AppliesPoolTuning(t *testing.T) {
	// The tuning invariants the pool relies on regardless of topology: bounded
	// MaxConns (so a hard-kill can't exhaust PostgreSQL's slots) and MinConns
	// never exceeding MaxConns.
	cfg, err := buildPoolConfig("postgres://u:p@localhost:5432/onscreen")
	if err != nil {
		t.Fatalf("buildPoolConfig: %v", err)
	}
	if cfg.MaxConns < 4 || cfg.MaxConns > 20 {
		t.Errorf("MaxConns = %d, want clamped to [4,20]", cfg.MaxConns)
	}
	if cfg.MinConns > cfg.MaxConns {
		t.Errorf("MinConns %d > MaxConns %d", cfg.MinConns, cfg.MaxConns)
	}
}

func TestBuildPoolConfig_RejectsBadDSN(t *testing.T) {
	if _, err := buildPoolConfig("://nonsense"); err == nil {
		t.Error("expected error for unparseable DSN, got nil")
	}
}
