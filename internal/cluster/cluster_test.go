package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeQuerier struct{ scan func(dest ...any) error }

func (q fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{q.scan} }

type fakeRow struct{ scan func(dest ...any) error }

func (r fakeRow) Scan(dest ...any) error { return r.scan(dest...) }

func TestDetectRole(t *testing.T) {
	ctx := context.Background()

	standby := fakeQuerier{scan: func(d ...any) error { *d[0].(*bool) = true; return nil }}
	if r, err := DetectRole(ctx, standby); err != nil || r != RoleStandby {
		t.Errorf("in-recovery: got (%v,%v), want standby", r, err)
	}

	primary := fakeQuerier{scan: func(d ...any) error { *d[0].(*bool) = false; return nil }}
	if r, err := DetectRole(ctx, primary); err != nil || r != RolePrimary {
		t.Errorf("not-in-recovery: got (%v,%v), want primary", r, err)
	}

	boom := fakeQuerier{scan: func(...any) error { return errors.New("db down") }}
	if r, err := DetectRole(ctx, boom); err == nil || r != RoleUnknown {
		t.Errorf("error: got (%v,%v), want (unknown, err)", r, err)
	}
}

func TestReplicationLag(t *testing.T) {
	ctx := context.Background()

	q := fakeQuerier{scan: func(d ...any) error {
		*d[0].(*pgtype.Float8) = pgtype.Float8{Float64: 2.5, Valid: true}
		return nil
	}}
	lag, err := ReplicationLag(ctx, q)
	if err != nil {
		t.Fatalf("ReplicationLag: %v", err)
	}
	if lag != 2500*time.Millisecond {
		t.Errorf("lag = %v, want 2.5s", lag)
	}

	// NULL replay timestamp (fresh standby / primary) → zero lag, no error.
	null := fakeQuerier{scan: func(d ...any) error {
		*d[0].(*pgtype.Float8) = pgtype.Float8{Valid: false}
		return nil
	}}
	if lag, err := ReplicationLag(ctx, null); err != nil || lag != 0 {
		t.Errorf("NULL lag: got (%v,%v), want (0,nil)", lag, err)
	}
}
