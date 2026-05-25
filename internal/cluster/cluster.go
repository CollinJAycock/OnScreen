// Package cluster reports a node's PostgreSQL role (read-write primary vs
// read-only standby) and replication lag — the operability surface for
// multi-site active/passive DR and active/active reads (HA roadmap §6). A
// standby trails the primary over the WAN; geo-routing, promotion decisions, and
// graceful read-only handling all key off this.
package cluster

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Role is a node's PostgreSQL role.
type Role string

const (
	RolePrimary Role = "primary" // accepts writes
	RoleStandby Role = "standby" // in recovery; read-only replica
	RoleUnknown Role = "unknown" // couldn't be determined
)

// Querier is the minimal DB surface these checks need; *pgxpool.Pool satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DetectRole reports whether the connected node is a read-write primary or a
// read-only standby, via pg_is_in_recovery(). On error the role is Unknown.
func DetectRole(ctx context.Context, q Querier) (Role, error) {
	var inRecovery bool
	if err := q.QueryRow(ctx, `SELECT pg_is_in_recovery()`).Scan(&inRecovery); err != nil {
		return RoleUnknown, err
	}
	if inRecovery {
		return RoleStandby, nil
	}
	return RolePrimary, nil
}

// ReplicationLag returns how far a standby trails the primary, from the last
// replayed-transaction timestamp. Returns 0 on a primary, and 0 on a standby
// that hasn't replayed any transaction yet (NULL timestamp).
func ReplicationLag(ctx context.Context, q Querier) (time.Duration, error) {
	var lagSeconds pgtype.Float8
	err := q.QueryRow(ctx, `
		SELECT CASE
		  WHEN pg_is_in_recovery() AND pg_last_xact_replay_timestamp() IS NOT NULL
		  THEN EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))
		  ELSE 0 END`).Scan(&lagSeconds)
	if err != nil {
		return 0, err
	}
	if !lagSeconds.Valid || lagSeconds.Float64 < 0 {
		return 0, nil
	}
	return time.Duration(lagSeconds.Float64 * float64(time.Second)), nil
}
