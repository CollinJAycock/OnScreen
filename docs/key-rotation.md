# Rotating `SECRET_KEY`

`SECRET_KEY` does double duty: it signs PASETO access tokens / HLS segment
tokens **and** it's the AES-256-GCM key for the secrets OnScreen stores at rest:

- secret-bearing **server settings** — TMDB / TVDB / arr / OpenSubtitles API
  keys, OIDC / SAML / LDAP / SMTP configs, object-storage credentials, and the
  uploaded TLS private key (stored with an `encv1:` prefix in `server_settings`);
- **webhook signing secrets** (`webhook_endpoints.secret`);
- per-user **TOTP secrets** (`users.totp_secret`).

So you can't just swap `SECRET_KEY` and restart — the new key can't decrypt the
old ciphertext, webhook delivery refuses to send unsigned, and 2FA logins break.
Use the `rotate-key` tool to re-encrypt everything old-key → new-key first.

## Procedure

1. **Generate the new key:**

   ```sh
   openssl rand -hex 32
   ```

2. **Dry run** (reads only — reports what it would change, commits nothing):

   ```sh
   OLD_SECRET_KEY=<current> SECRET_KEY=<new> DATABASE_URL=<dsn> \
     go run ./cmd/rotate-key
   ```

   Expect a per-table summary like `rotated=3 skipped=0`. If everything shows
   `rotated=0 skipped=N`, `OLD_SECRET_KEY` isn't the key currently in use —
   stop and fix it before applying.

3. **Apply** (single transaction; rolls back on any error):

   ```sh
   OLD_SECRET_KEY=<current> SECRET_KEY=<new> DATABASE_URL=<dsn> \
     go run ./cmd/rotate-key -apply
   ```

4. **Restart** every server and worker with the **new** `SECRET_KEY`.

Run steps 3–4 in a maintenance window with the server stopped (or briefly
after) — it avoids a race where the running server re-encrypts a setting with
the old key between the dry run and the apply.

## After rotation

- **All access tokens are invalid** — users re-login (refresh tokens are SHA-256
  hashes, not encrypted with `SECRET_KEY`, but the session epoch / new key
  invalidates the access path; clients fall back to a fresh login).
- **In-flight HLS streams reconnect** — segment tokens were signed by the old
  key.
- Values the old key can't decrypt are **left untouched and reported as
  skipped** — the tool never clobbers data, so a wrong `OLD_SECRET_KEY` is safe
  (it just rotates nothing).

## What it does NOT touch

Node/site-local config (`node_settings`), connection strings, and other
non-credential operational data are plaintext by design and unaffected. Refresh
tokens and password hashes aren't `SECRET_KEY`-encrypted, so they survive
rotation untouched.
