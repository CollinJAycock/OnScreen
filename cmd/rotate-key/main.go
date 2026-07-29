// Command rotate-key re-encrypts every at-rest secret from an old SECRET_KEY to
// a new one, so rotating SECRET_KEY isn't destructive.
//
// OnScreen encrypts a handful of values at rest with AES-256-GCM derived from
// SECRET_KEY: secret-bearing server_settings (TMDB/TVDB/arr/OpenSubtitles/OIDC/
// SAML/LDAP/SMTP/storage/TLS — stored with an `encv1:` prefix), webhook signing
// secrets (webhook_endpoints.secret), and per-user TOTP secrets
// (users.totp_secret). Rotating SECRET_KEY without re-encrypting these orphans
// them: the new key can't decrypt them, webhook delivery refuses to send
// unsigned, and 2FA logins break.
//
// This tool decrypts each value with the OLD key and re-encrypts with the NEW
// key, in a single transaction. It's a DRY RUN by default — pass -apply to
// commit. Values that don't decrypt with the old key are left untouched and
// reported (so a wrong old key shows "rotated=0" rather than corrupting data).
//
// Usage:
//
//	OLD_SECRET_KEY=<current> SECRET_KEY=<new> DATABASE_URL=<dsn> rotate-key        # dry run
//	OLD_SECRET_KEY=<current> SECRET_KEY=<new> DATABASE_URL=<dsn> rotate-key -apply # commit
//
// Run it with the server stopped (or in a maintenance window): after rotation,
// every PASETO access token and HLS segment token signed by the old key is
// invalid, so users re-login and in-flight streams reconnect.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/auth"
)

// encPrefix must match internal/domain/settings.encPrefix — the sentinel that
// marks an encrypted server_settings value. It's a persisted on-disk format
// marker, so it only changes alongside a migration.
const encPrefix = "encv1:"

// encPrefixV2 is the current settings envelope: AES-GCM with the settings KEY
// NAME as associated data, so a ciphertext only decrypts in the row it was
// written for. Rotation is also the upgrade path — every settings row this
// tool rewrites comes out as v2 regardless of which envelope it went in as.
const encPrefixV2 = "encv2:"

func main() {
	var apply bool
	var oldKey, newKey, dsn string
	flag.BoolVar(&apply, "apply", false, "commit the rotation (default: dry run)")
	flag.StringVar(&oldKey, "old-key", os.Getenv("OLD_SECRET_KEY"), "current SECRET_KEY (or OLD_SECRET_KEY env)")
	flag.StringVar(&newKey, "new-key", os.Getenv("SECRET_KEY"), "new SECRET_KEY (or SECRET_KEY env)")
	flag.StringVar(&dsn, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL DSN (or DATABASE_URL env)")
	flag.Parse()

	if oldKey == "" || newKey == "" || dsn == "" {
		fmt.Fprintln(os.Stderr, "rotate-key: re-encrypt at-rest secrets old SECRET_KEY -> new SECRET_KEY")
		fmt.Fprintln(os.Stderr, "  set OLD_SECRET_KEY, SECRET_KEY, DATABASE_URL (or pass -old-key/-new-key/-database-url)")
		fmt.Fprintln(os.Stderr, "  dry run by default; pass -apply to commit")
		os.Exit(2)
	}
	if oldKey == newKey {
		fmt.Fprintln(os.Stderr, "rotate-key: old and new keys are identical — nothing to do")
		os.Exit(2)
	}

	oldEnc, err := auth.NewEncryptor(auth.DeriveKey32(oldKey))
	if err != nil {
		fatal("invalid old key: %v", err)
	}
	newEnc, err := auth.NewEncryptor(auth.DeriveKey32(newKey))
	if err != nil {
		fatal("invalid new key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("connect db: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		fatal("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful Commit

	jobs := []struct {
		name string
		fn   func(context.Context, pgx.Tx, *auth.Encryptor, *auth.Encryptor) (rotated, skipped int, err error)
	}{
		{"server_settings", rotateSettings},
		{"webhook_endpoints.secret", rotateWebhookSecrets},
		{"users.totp_secret", rotateTOTPSecrets},
		{"user_scrobble.listenbrainz_token", rotateScrobbleTokens},
	}

	var totalRot, totalSkip int
	for _, j := range jobs {
		rot, skip, err := j.fn(ctx, tx, oldEnc, newEnc)
		if err != nil {
			fatal("%s: %v", j.name, err)
		}
		fmt.Printf("  %-28s rotated=%-4d skipped=%d\n", j.name, rot, skip)
		totalRot += rot
		totalSkip += skip
	}

	if apply {
		if err := tx.Commit(ctx); err != nil {
			fatal("commit: %v", err)
		}
		fmt.Printf("APPLIED: re-encrypted %d secret(s); %d skipped (not decryptable with the old key).\n", totalRot, totalSkip)
		if totalRot == 0 {
			fmt.Println("note: nothing was rotated — double-check OLD_SECRET_KEY is the key currently in use.")
		}
		return
	}
	fmt.Printf("DRY RUN: would re-encrypt %d secret(s); %d skipped. Re-run with -apply to commit.\n", totalRot, totalSkip)
	if totalRot == 0 && totalSkip > 0 {
		fmt.Println("note: every value was skipped — OLD_SECRET_KEY is probably not the key currently in use.")
	}
}

// reEncrypt decrypts stored with oldEnc and re-encrypts with newEnc. Used for
// the raw, prefix-less columns (webhook secrets, TOTP secrets, scrobble
// tokens). ok=false means the old key couldn't decrypt it (wrong key, already
// rotated, or plaintext) — the caller skips it.
func reEncrypt(oldEnc, newEnc *auth.Encryptor, stored string) (out string, ok bool) {
	plain, err := oldEnc.Decrypt(stored)
	if err != nil {
		return "", false
	}
	reEnc, err := newEnc.Encrypt(plain)
	if err != nil {
		return "", false
	}
	return reEnc, true
}

// reEncryptSetting decrypts a server_settings value under whichever envelope it
// carries and re-encrypts it as v2, binding the key name as associated data.
// This is what upgrades a legacy v1 row: rotation rewrites it in the current
// format rather than preserving the weaker one.
func reEncryptSetting(oldEnc, newEnc *auth.Encryptor, key, stored string) (out string, ok bool) {
	var (
		plain string
		err   error
	)
	switch {
	case strings.HasPrefix(stored, encPrefixV2):
		plain, err = oldEnc.DecryptContext(strings.TrimPrefix(stored, encPrefixV2), key)
	case strings.HasPrefix(stored, encPrefix):
		plain, err = oldEnc.Decrypt(strings.TrimPrefix(stored, encPrefix))
	default:
		return "", false
	}
	if err != nil {
		return "", false
	}
	reEnc, err := newEnc.EncryptContext(plain, key)
	if err != nil {
		return "", false
	}
	return encPrefixV2 + reEnc, true
}

// rotateSettings re-encrypts every encv1:-prefixed server_settings value. Keyed
// on the prefix rather than a hardcoded key list, so it stays correct if new
// secret-bearing settings are added later.
func rotateSettings(ctx context.Context, tx pgx.Tx, oldEnc, newEnc *auth.Encryptor) (int, int, error) {
	type row struct{ key, value string }
	var rows []row
	q, err := tx.Query(ctx,
		`SELECT key, value FROM server_settings WHERE value LIKE $1 OR value LIKE $2`,
		encPrefix+"%", encPrefixV2+"%")
	if err != nil {
		return 0, 0, fmt.Errorf("select: %w", err)
	}
	for q.Next() {
		var r row
		if err := q.Scan(&r.key, &r.value); err != nil {
			q.Close()
			return 0, 0, fmt.Errorf("scan: %w", err)
		}
		rows = append(rows, r)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate: %w", err)
	}

	var rotated, skipped int
	for _, r := range rows {
		out, ok := reEncryptSetting(oldEnc, newEnc, r.key, r.value)
		if !ok {
			skipped++
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE server_settings SET value = $1 WHERE key = $2`, out, r.key); err != nil {
			return 0, 0, fmt.Errorf("update %q: %w", r.key, err)
		}
		rotated++
	}
	return rotated, skipped, nil
}

// rotateWebhookSecrets re-encrypts webhook_endpoints.secret (raw base64, no
// prefix).
func rotateWebhookSecrets(ctx context.Context, tx pgx.Tx, oldEnc, newEnc *auth.Encryptor) (int, int, error) {
	return rotateRawColumn(ctx, tx, oldEnc, newEnc,
		`SELECT id::text, secret FROM webhook_endpoints WHERE secret IS NOT NULL AND secret <> ''`,
		`UPDATE webhook_endpoints SET secret = $1 WHERE id = $2::uuid`)
}

// rotateTOTPSecrets re-encrypts users.totp_secret (raw base64, no prefix).
func rotateTOTPSecrets(ctx context.Context, tx pgx.Tx, oldEnc, newEnc *auth.Encryptor) (int, int, error) {
	return rotateRawColumn(ctx, tx, oldEnc, newEnc,
		`SELECT id::text, totp_secret FROM users WHERE totp_secret IS NOT NULL AND totp_secret <> ''`,
		`UPDATE users SET totp_secret = $1 WHERE id = $2::uuid`)
}

// rotateScrobbleTokens re-encrypts user_scrobble.listenbrainz_token (raw
// base64, no prefix).
//
// This sink was MISSING from the job list. Every other at-rest secret was
// rotated, so a documented SECRET_KEY rotation looked completely successful
// while silently orphaning every user's ListenBrainz token — undecryptable
// afterwards, with no error surfaced until scrobbling quietly stopped working.
func rotateScrobbleTokens(ctx context.Context, tx pgx.Tx, oldEnc, newEnc *auth.Encryptor) (int, int, error) {
	return rotateRawColumn(ctx, tx, oldEnc, newEnc,
		`SELECT user_id::text, listenbrainz_token FROM user_scrobble WHERE listenbrainz_token IS NOT NULL AND listenbrainz_token <> ''`,
		`UPDATE user_scrobble SET listenbrainz_token = $1 WHERE user_id = $2::uuid`)
}

// rotateRawColumn rotates a (id, secret) result set where secret is a raw
// base64 AES-GCM blob (no encv1: prefix). selectSQL must yield two text columns
// (id, secret); updateSQL takes ($1=new secret, $2=id).
func rotateRawColumn(ctx context.Context, tx pgx.Tx, oldEnc, newEnc *auth.Encryptor, selectSQL, updateSQL string) (int, int, error) {
	type row struct{ id, secret string }
	var rows []row
	q, err := tx.Query(ctx, selectSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("select: %w", err)
	}
	for q.Next() {
		var r row
		if err := q.Scan(&r.id, &r.secret); err != nil {
			q.Close()
			return 0, 0, fmt.Errorf("scan: %w", err)
		}
		rows = append(rows, r)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate: %w", err)
	}

	var rotated, skipped int
	for _, r := range rows {
		out, ok := reEncrypt(oldEnc, newEnc, r.secret)
		if !ok {
			skipped++
			continue
		}
		if _, err := tx.Exec(ctx, updateSQL, out, r.id); err != nil {
			return 0, 0, fmt.Errorf("update %s: %w", r.id, err)
		}
		rotated++
	}
	return rotated, skipped, nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rotate-key: "+format+"\n", args...)
	os.Exit(1)
}
