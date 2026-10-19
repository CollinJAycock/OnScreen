# Restore drill

A backup you have never restored is a guess. This page is a rehearsal you run
on a schedule: take a real backup, restore it into a **throwaway** OnScreen,
and check that the data came back. Nothing here touches the production server
except reading a backup from it.

What a backup covers: the whole Postgres database — accounts, libraries,
items, watch history, playlists, collections, settings (stored encrypted), the
audit log. What it does **not** cover: media files, the artwork resize cache,
transcode scratch space, and Valkey (sessions, rate limits and queues are
rebuilt at runtime). Those are either your source of truth already (media) or
regenerable.

## How often

| When | What |
|---|---|
| Every night | The scheduled `Database backup` task writes a dump (fresh installs get it by default: 03:11 server time, newest 7 kept, in `/var/backups/onscreen`). Check **Settings ▸ Tasks** for its last status, or let a `backup_failed` notification tell you. |
| Every week | Copy the newest dump **off the host** (another machine, a NAS, object storage). A dump next to the database it backs up dies with that disk. |
| Every quarter, and after every major upgrade | Run this whole drill. |

## Before you start

- **The `SECRET_KEY` of the production server.** Stored API keys, SMTP
  credentials, OIDC secrets and the like are encrypted with a key derived from
  it. Restore with a different `SECRET_KEY` and the data restores fine, but
  every encrypted setting comes back unreadable. Keep `SECRET_KEY` in your
  password manager alongside where the dumps go: the dump without it is a
  partial restore.
- An admin account on production with a **local password** (restore asks for
  it again). Single-sign-on admins can restore too; the step-up check is
  skipped for them.
- For the manual path, a `pg_restore` at least as new as the `pg_dump` that
  wrote the dump. The OnScreen image ships PostgreSQL 17 client tools, so its
  dumps need `pg_restore` 17: the `pgvector/pgvector:pg16` database container's
  own `pg_restore` refuses them ("unsupported version in file header"). The
  steps below run `pg_restore` from the OnScreen image for that reason.

## 1. Take a backup

Pick one of these:

- **Scheduled dump.** The newest `onscreen-backup-YYYYMMDD-HHMMSS.dump` in the
  backup task's output directory (`docker compose cp server:/var/backups/onscreen ./dumps`
  on the default Docker stack).
- **Web UI.** **Settings ▸ Backup ▸ Download backup**. The file name ends in
  `-vN.dump`, N being the schema version it was taken at.
- **API.** The backup endpoints refuse API keys (a dump holds every password
  hash), so sign in for a bearer token. An admin with 2FA gets a TOTP
  challenge from `/auth/login` instead of a token; use the web UI for that
  account, or a local admin without 2FA.

  ```bash
  SRC=https://onscreen.example.com
  TOKEN=$(curl -s "$SRC/api/v1/auth/login" -H 'Content-Type: application/json' \
            -d '{"username":"admin","password":"…"}' | jq -r .data.access_token)
  curl -fsS -H "Authorization: Bearer $TOKEN" -OJ "$SRC/api/v1/admin/backup"
  # → onscreen-backup-20261019-031100-v60.dump
  ```

While you are on production, write down the numbers you will compare against:

```sql
SELECT (SELECT count(*) FROM users)                         AS users,
       (SELECT count(*) FROM libraries WHERE deleted_at IS NULL) AS libraries,
       (SELECT count(*) FROM media_items WHERE deleted_at IS NULL) AS items,
       (SELECT count(*) FROM watch_events)                  AS watch_events,
       (SELECT max(version_id) FROM goose_db_version)       AS schema_version;
```

## 2. Check the archive is readable

```bash
pg_restore --list onscreen-backup-*.dump | head -20
pg_restore --list onscreen-backup-*.dump | grep -c 'TABLE DATA'
```

A truncated or corrupt dump fails here, in seconds, without a database. A
healthy one lists well over a hundred `TABLE DATA` entries.

## 3. Bring up a throwaway server

Use the same image tag as production (or newer, never older) and the
production `SECRET_KEY`, on a separate Compose project so nothing is shared
with the real stack:

```bash
mkdir restore-drill && cd restore-drill
cp /path/to/production/docker-compose.yml .
cat > .env <<EOF
DB_PASS=drill-only-password
SECRET_KEY=<production SECRET_KEY>
SERVER_PORT=7090
DEBUG_PORT=7091
EOF
docker compose -p onscreen-drill up -d
```

`SERVER_PORT` / `DEBUG_PORT` are read by the repository's
`docker/docker-compose.yml`; if your compose file publishes `7070:7070`
literally, change it to `7090:7070` in the drill copy so it doesn't collide
with production on the same host. The `-p onscreen-drill` project name gives
the drill its own volumes and network.

Open `http://localhost:7090`, complete first-run setup with a throwaway admin
(this account is about to be replaced by the restore), and sign in.

Do not point the drill stack at the production media folders with write
access, and do not enable its scheduled tasks against them; a read-only mount
(`:ro`) or no media at all is fine for the drill.

## 4. Restore

### Through the API (the normal path)

**Settings ▸ Backup ▸ Restore database** on the drill server: pick the dump,
type `RESTORE`, enter the throwaway admin's password, restore. Or with curl:

```bash
DST=http://localhost:7090
TOKEN=$(curl -s "$DST/api/v1/auth/login" -H 'Content-Type: application/json' \
          -d '{"username":"drill","password":"…"}' | jq -r .data.access_token)
curl -fsS -H "Authorization: Bearer $TOKEN" \
     -F file=@onscreen-backup-20261019-031100-v60.dump \
     -F password='…' \
     "$DST/api/v1/admin/restore" | jq .data
```

The response reports `dump_version`, `server_version`, `migrated` (true when
the server ran migrations to bring an older dump forward) and any `stderr`
from `pg_restore` that wasn't the known partition-constraint noise. A `409
DUMP_NEWER_THAN_SERVER` means the drill server is older than the dump: use the
production image tag, don't force it.

The restore replaces the whole database, including the accounts, so the
throwaway admin is gone: sign in again with a **production** account.

### By hand with pg_restore (when the server won't start)

This is the path for a dead server, a new host, or a dump too large for the
2 GiB upload limit. Stop the server first so nothing writes mid-restore:

```bash
docker compose -p onscreen-drill stop server worker
chmod a+r onscreen-backup-….dump   # dumps are written 0600; the container user must read this copy
docker compose -p onscreen-drill run --rm --no-deps \
  -v "$PWD:/dumps:ro" -e PGPASSWORD=drill-only-password \
  --entrypoint pg_restore server \
  -h postgres -U onscreen -d onscreen --clean --if-exists --no-owner --no-acl \
  /dumps/onscreen-backup-….dump
docker compose -p onscreen-drill run --rm migrate      # bring an older dump up to this build
docker compose -p onscreen-drill start server worker
```

On a bare-metal install it is the same three steps: stop the service,
`pg_restore --clean --if-exists --no-owner --no-acl -d "$DATABASE_URL" <dump>`,
then migrate (the systemd unit's `ExecStartPre` does that on start).

`pg_restore` exits non-zero and prints `cannot drop inherited constraint` once
per partition of the partitioned tables; that is expected with `--clean` on
this schema and the data still lands (the API path filters it out for you).
Any other error is real. Without the `migrate` service, set
`AUTO_MIGRATE=true` on the server or run `goose -dir internal/db/migrations
postgres "$DATABASE_URL" up`.

## 5. Verify

All of these should pass before you call the drill done:

1. `curl -fsS http://localhost:7090/health/ready` returns 200 (database and
   Valkey reachable, no pending migrations).
2. The counts from step 1 match, run against the drill database
   (`docker compose -p onscreen-drill exec postgres psql -U onscreen onscreen`).
   Watch events and items taken after the dump are the only acceptable
   difference.
3. A production account signs in, sees its libraries, its Continue Watching
   row and its watch history.
4. The `SECRET_KEY` check: **Settings ▸ General** shows the TMDB key as set,
   **Refresh metadata** on one title succeeds, and the server log has no
   `settings get: decrypt failed` lines. A decryption failure means the key
   in your password manager is not the production one.
5. A title plays (with media mounted), or at least its detail page shows
   artwork and file info.
6. **Settings ▸ Tasks** lists production's tasks, including the backup task.

Write down the date, the dump's age and size, how long the restore took and
anything that failed. That time is your real recovery time.

## 6. Tear down

```bash
docker compose -p onscreen-drill down -v
```

`-v` deletes the drill's volumes, including its copy of the database. The
drill server held production data (password hashes included), so don't leave
it running.

## Restoring for real

The same steps, against the real server: restore the newest good dump through
**Settings ▸ Backup** (or the manual path if the server can't start), then
verify as in step 5. Sessions in Valkey from before the restore keep working
only if their accounts exist in the dump; everyone else signs in again.
