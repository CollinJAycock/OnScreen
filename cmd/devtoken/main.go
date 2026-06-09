// devtoken mints an admin PASETO for the local dev server. Reads SECRET_KEY
// and USER_ID from the environment. SESSION_EPOCH is resolved in this order:
//
//  1. Explicit SESSION_EPOCH env var (e.g. SESSION_EPOCH=0 to deliberately
//     mint a stale token for testing the rejection path).
//  2. If DATABASE_URL is set and USER_ID is a real UUID, query the user's
//     current session_epoch from the DB. This is the friendly default —
//     without it, the minted token gets rejected the moment the user has
//     ever logged out from anywhere (epoch=0 falls behind users.session_epoch).
//  3. Fall back to 0 with a warning so the operator knows the token may be
//     rejected and can choose to set SESSION_EPOCH explicitly.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for database/sql

	"github.com/onscreen/onscreen/internal/auth"
)

func main() {
	secret := os.Getenv("SECRET_KEY")
	if secret == "" {
		// No hardcoded fallback: a known in-repo key would mint forgeable
		// admin tokens against any server accidentally booted with it.
		fmt.Fprintln(os.Stderr, "devtoken: SECRET_KEY is required")
		os.Exit(1)
	}
	key := auth.DeriveKey32(secret)
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	userID := os.Getenv("USER_ID")
	if userID == "" {
		userID = "00000000-0000-0000-0000-000000000000"
	}

	epoch := resolveSessionEpoch(userID)

	token, err := tm.IssueAccessToken(auth.Claims{
		UserID:       uuid.MustParse(userID),
		Username:     "dev",
		IsAdmin:      true,
		SessionEpoch: epoch,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(token)
}

// resolveSessionEpoch returns the SessionEpoch claim to embed in the minted
// token, honoring the priority order documented at the package level.
//
// Writes a single one-line note to stderr explaining which path won so the
// operator never has to guess why a token was rejected (or, when they DO
// set SESSION_EPOCH explicitly to test the stale path, why it took effect).
// The token itself still goes only to stdout — stderr is informational.
func resolveSessionEpoch(userID string) int64 {
	if e := os.Getenv("SESSION_EPOCH"); e != "" {
		var v int64
		fmt.Sscan(e, &v)
		fmt.Fprintf(os.Stderr, "devtoken: SESSION_EPOCH=%d from env\n", v)
		return v
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || userID == "00000000-0000-0000-0000-000000000000" {
		// Nothing to query against; classic env-default path.
		return 0
	}

	v, err := fetchSessionEpoch(dsn, userID)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"devtoken: warning — couldn't read session_epoch from DB (%v); using 0. "+
				"Token may be rejected if this user has logged out. Set SESSION_EPOCH explicitly to silence.\n",
			err)
		return 0
	}
	fmt.Fprintf(os.Stderr, "devtoken: SESSION_EPOCH=%d auto-fetched from DB for user %s\n", v, userID)
	return v
}

// fetchSessionEpoch opens a short-lived connection to read users.session_epoch.
// Uses database/sql + the pgx driver to keep this tiny CLI's import surface
// small (no pool, no migrations dependency). Bounded by a 5-second timeout
// so a wedged DB doesn't hang the dev workflow.
func fetchSessionEpoch(dsn, userID string) (int64, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return 0, fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var epoch int64
	err = db.QueryRowContext(ctx,
		`SELECT session_epoch FROM users WHERE id = $1`, userID).Scan(&epoch)
	if err != nil {
		return 0, fmt.Errorf("query: %w", err)
	}
	return epoch, nil
}
