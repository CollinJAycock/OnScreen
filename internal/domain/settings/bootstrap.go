package settings

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// LoadOTelConfig reads the OTel tracing config directly from the DB using a
// one-shot pgx.Conn — no pool, no instrumentation. The TracerProvider must be
// built before the instrumented pgxpool because otelpgx caches the global TP
// at pgxpool.NewWithConfig time, so this runs first at process startup.
//
// Missing row, missing table (fresh DB before migrations have run), or any
// other error degrades to OTelConfig{} (disabled). Tracing is opt-in, so a
// degraded read should never block startup.
func LoadOTelConfig(ctx context.Context, connStr string) OTelConfig {
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return OTelConfig{}
	}
	defer conn.Close(ctx)

	var raw string
	err = conn.QueryRow(ctx,
		`SELECT value FROM server_settings WHERE key = $1`, keyOTelConfig,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OTelConfig{}
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return OTelConfig{}
		}
		return OTelConfig{}
	}
	if raw == "" {
		return OTelConfig{}
	}
	var cfg OTelConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return OTelConfig{}
	}
	return cfg
}

// LoadGeneralConfig reads the general server config (base URL, log level, CORS
// origins) directly from the DB using a one-shot pgx.Conn — no pool, no
// instrumentation. Runs at process startup before the slog level var is
// installed and before HTTP handlers are constructed.
//
// Missing row, missing table (fresh DB before migrations have run), or any
// other error degrades to GeneralConfig{} so callers fall back to built-in
// defaults rather than blocking startup.
func LoadGeneralConfig(ctx context.Context, connStr string) GeneralConfig {
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return GeneralConfig{}
	}
	defer conn.Close(ctx)

	var raw string
	err = conn.QueryRow(ctx,
		`SELECT value FROM server_settings WHERE key = $1`, keyGeneralConfig,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GeneralConfig{}
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return GeneralConfig{}
		}
		return GeneralConfig{}
	}
	if raw == "" {
		return GeneralConfig{}
	}
	var cfg GeneralConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return GeneralConfig{}
	}
	return cfg
}
