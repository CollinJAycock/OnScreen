# OnScreen HA & Disaster-Recovery Runbook

Operational procedures for running OnScreen in a high-availability and/or
multi-site configuration: enabling each tier, monitoring it, and recovering from
failures. For the *why* and the design, see [ha-roadmap.md](ha-roadmap.md) and
[../ARCHITECTURE.md](../ARCHITECTURE.md). For baseline single-node deploys, see
[deployment.md](deployment.md).

> **Single-node deployments need none of this.** Every HA/multi-site feature is
> opt-in and off by default — a standard install behaves exactly as before.
>
> **Env vs UI:** cluster-wide toggles (ABR, public asset cache, static-ABR
> enable, server name, retention, TMDB rate limit) are editable in **Settings ▸
> System** — the env vars below are the initial default, and a saved override
> wins. Node/site-specific config (connection strings, `SECRET_KEY`, bind
> addresses, paths, `SITE_ID`, per-worker `TRANSCODE_QSV_DECODE`,
> `STATIC_ABR_ROOT`) stays env-only because `server_settings` replicates across
> sites.

## Contents
- [Topology at a glance](#topology-at-a-glance)
- [Health & status endpoints](#health--status-endpoints)
- [Tier 1 — Valkey Sentinel](#tier-1--valkey-sentinel)
- [Tier 2 — PostgreSQL replication & failover](#tier-2--postgresql-replication--failover)
- [Tier 3 — Object storage (MediaStore)](#tier-3--object-storage-mediastore)
- [CDN offload](#cdn-offload)
- [Static-ABR pre-encode](#static-abr-pre-encode)
- [Multi-site active/passive DR](#multi-site-activepassive-dr)
- [Failover runbook](#failover-runbook)
- [Fail-back runbook](#fail-back-runbook)
- [Backup & restore](#backup--restore)

---

## Topology at a glance

A fully-HA single site:

```
            ┌── API replica ──┐
 clients ──▶│   (stateless)   │──┐   leader-elected singleton work
            ├── API replica ──┤  │   (Valkey lease, internal/worker/master.go)
            └── API replica ──┘  │
                   │             │
        ┌──────────┼─────────────┼──────────┐
        ▼          ▼             ▼          ▼
   Valkey      Postgres      Postgres    object store
   Sentinel    primary  ───▶ standby     (S3/MinIO) + CDN
   (1m+2r+3s)  (RW)  stream  (RO)
```

Multi-site active/passive adds a second copy of the above at site B, with the
primary's Postgres streaming over the WAN and content replicated by ZFS.

---

## Health & status endpoints

| Endpoint | Port | Meaning |
|---|---|---|
| `GET /health/live` | API + metrics | process is up (Docker/TrueNAS healthcheck) |
| `GET /health/ready` | API | DB + Valkey reachable **and** no pending migrations |
| `GET /health/cluster` | API | `{site_id, role, replication_lag_seconds}` — which site, primary vs standby, lag |

`/health/cluster` is the multi-site signal. Example on a standby:

```bash
curl -s http://site-b:7070/health/cluster
# {"site_id":"site-b","role":"standby","replication_lag_seconds":1.8}
```

`role` is `primary` (writable), `standby` (read-only replica), or `unknown` (DB
unreachable → 503). Geo-routing and promotion decisions key off this.

---

## Tier 1 — Valkey Sentinel

Valkey holds the leader lock, sessions, transcode-dispatch counters, rate limits,
and caches. Make it HA *first* — the leader-election guarantee isn't real until
the lock store survives a node loss.

**Deploy:** [`docker/docker-compose.valkey-ha.yml`](../docker/docker-compose.valkey-ha.yml)
(1 master + 2 replicas + 3 sentinels, quorum 2, ~10s failover < the 15s lock TTL).

**Point OnScreen at it:**
```bash
VALKEY_SENTINEL_ADDRS=sentinel1:26379,sentinel2:26379,sentinel3:26379
VALKEY_SENTINEL_MASTER=onscreen          # default
VALKEY_URL=redis://:password@ignored:6379  # still supplies auth/db; host ignored in Sentinel mode
```
When `VALKEY_SENTINEL_ADDRS` is set the client connects via `valkey.NewFailover`
and re-homes to the promoted master automatically.

**Verify:** `redis-cli -p 26379 sentinel master onscreen` shows the current
master and `num-other-sentinels`.

> ⚠️ Sentinel's TILT guard trips on clock jumps — don't run the Sentinel quorum
> on a Docker Desktop VM (validate on a stable-clock Linux/TrueNAS host).

---

## Tier 2 — PostgreSQL replication & failover

**Substrate:** [`docker/docker-compose.postgres-ha.yml`](../docker/docker-compose.postgres-ha.yml)
— a primary + a streaming standby that self-bootstraps via `pg_basebackup`.

**Point OnScreen at both with a multi-host failover DSN** — pgx connects to
whichever host is read-write and re-homes after a promotion:
```bash
DATABASE_URL="postgres://onscreen:pass@pg-primary:5432,pg-standby:5432/onscreen?target_session_attrs=read-write&sslmode=disable"
DATABASE_RO_URL="postgres://onscreen:pass@pg-standby:5432,pg-primary:5432/onscreen?target_session_attrs=any&sslmode=disable"
```
With fallbacks present the pool shortens `MaxConnLifetime` to 60s so writes
re-home within ~1 min of a *graceful* switchover; a crash failover recovers at
once (broken connections evict immediately).

**Automatic promotion** is the one piece the app doesn't do — put an orchestrator
on top of this substrate:
- managed Multi-AZ (RDS / Cloud SQL / Crunchy Bridge) — failover is the provider's job;
- Patroni / repmgr / CloudNativePG — self-hosted leader election + auto-promote;
- or operator-driven `pg_ctl promote` (see [Failover runbook](#failover-runbook)).

**Monitor lag:** `/health/cluster` → `replication_lag_seconds`, or on the primary
`SELECT client_addr, state, sync_state FROM pg_stat_replication;`.

---

## Tier 3 — Object storage (MediaStore)

Removes the local-disk SPOF and is the prerequisite for CDN offload + multi-site
content portability. Configured at runtime from **Settings ▸ Integrations ▸
Storage** (encrypted in `server_settings`):

1. Enable object storage, backend **S3-compatible**.
2. Endpoint (no scheme), region, bucket, access key, secret key, TLS toggle.
3. **Media root** — the local path prefix stripped to form the object key
   (`/mnt/media` → key `Movies/x.mkv`). **Path prefix** — prepended inside the bucket.
4. **CDN base URL** (optional) — segments/assets are served from here (see below).
5. **Test connection**, then **Save** (the live backend hot-swaps, no restart).

Backends: AWS S3, MinIO, Backblaze B2, Wasabi, Cloudflare R2. Default (disabled)
serves from the local filesystem exactly as before. Every read path — direct
play, download, transcode source, artwork, scanner discovery — and the artwork/
cover write paths route through the store.

---

## CDN offload

- **Object storage:** with a CDN base configured, every serve path 302-redirects
  the client/worker to a signed bucket/CDN URL — the bytes never touch the app
  tier. No extra config beyond the Storage settings.
- **Local-disk assets:** set `PUBLIC_ASSET_CACHE=true` so immutable resized
  artwork emits `Cache-Control: public`, lettting a CDN fronting the app cache
  it. Configure the CDN to key `/artwork` on the URL (ignoring the `?token=`
  param, since the resized bytes are identical for every user).

---

## Static-ABR pre-encode

Pre-encodes the ABR ladder for the most-played titles to the store so their
segments serve statically from object storage / CDN, leaving the live-transcode
fleet for the cold tail. **Only worthwhile with object storage + a CDN.**

```bash
STATIC_ABR_ENABLED=true
STATIC_ABR_ROOT=                 # empty = bucket-relative (object storage); a dir for a local static root
```
A daily off-peak cron (`static_abr_preencode`) runs the pass; on a hit, playback
`Start` returns the static master instead of starting a live session. Re-encodes
automatically when a source's content hash changes. Watch the Tasks UI / logs for
`static-abr: N candidates, M planned, K encoded`.

---

## Multi-site active/passive DR

Two sites, each with a full library copy; site B is a warm standby promoted on
loss of site A. This leans on tiers 2–3 stretched across a WAN.

### Prerequisites
- **Content** replicated A→B: TrueNAS/ZFS snapshot replication (artwork rides
  along — it lives next to media), or object storage with cross-region replication.
- **Postgres** at B is a streaming standby of A (the Tier-2 substrate, cross-WAN).
- Same `SECRET_KEY` at both sites (tokens minted at A validate at B).
- `SITE_ID` set distinctly per site (`site-a`, `site-b`).

### Content addressing (different mount points)
The replicated DB carries site A's absolute `FilePath`s. Resolve them at site B:
- **Object storage:** set each site's **Media root** so the same content keys to
  the same object — nothing else to do.
- **Local/ZFS:** in **Settings ▸ Storage ▸ Multi-site path mappings** at site B,
  map the primary's prefix to B's mount, e.g. `/mnt/site-a/media=/mnt/site-b/media`.

### Steady state
- Site A: primary, read-write, serving.
- Site B: standby. Its DB is read-only (`/health/cluster` → `standby`); a stray
  write returns 503 (`respond.IsReadOnlyError`), not a 500. Typically B serves no
  user traffic until promoted (warm standby), or serves **reads only** if you run
  active/active reads (point B's `DATABASE_RO_URL` at its local replica and
  `DATABASE_URL` at A).

---

## Failover runbook

**Trigger:** site A (or its Postgres primary) is down. Goal: make B writable and
take traffic.

1. **Confirm A is actually down** (avoid split-brain). Check `/health/live` and
   `/health/cluster` for site A from an independent vantage point. If A's DB is
   merely unreachable from B but A is still serving clients, do **not** promote.

2. **Check the standby's lag before promoting** (data-loss window):
   ```bash
   curl -s http://site-b:7070/health/cluster   # replication_lag_seconds
   ```
   Async replication means up to `lag` seconds of the most recent writes may be
   lost. Acceptable for a media server (a progress beacon); note it.

3. **Promote site B's Postgres:**
   - Managed Multi-AZ / Patroni / repmgr: promotion is automatic or one command —
     follow that tool. Skip to step 5.
   - Manual: `pg_ctl promote -D $PGDATA` (or `SELECT pg_promote();`). The standby
     exits recovery and becomes read-write.

4. **Verify B is now primary:**
   ```bash
   curl -s http://site-b:7070/health/cluster   # role:"primary"
   ```
   OnScreen at B re-homes automatically: with a multi-host `DATABASE_URL` listing
   B, pgx finds the read-write node within ~1 min (graceful) or immediately
   (crash). No app restart needed. If B's `DATABASE_URL` points only at A,
   repoint it to B and restart.

5. **Send traffic to B:** update DNS / geo-routing / load balancer to site B.
   Tokens already validate (shared `SECRET_KEY`).

6. **Announce** the data-loss window from step 2 (if any) and the new primary.

---

## Fail-back runbook

Once site A is healthy again, return to A-primary (or leave B primary and make A
the new standby — symmetric).

1. **Rebuild A as a standby of B.** A's old data diverged at the failover point,
   so re-seed it from B: `pg_basebackup -h site-b -D $PGDATA -R` (the
   postgres-ha standby entrypoint does this on a fresh data dir — `down -v` the A
   stack and bring it back up against B as primary).
2. **Re-sync content** B→A (ZFS reverse replication / object-storage re-sync) for
   anything written while B was primary.
3. **Confirm A has caught up:** `/health/cluster` at A shows `standby` with low
   `replication_lag_seconds`.
4. **Switch back** (a *planned* switchover, low-risk):
   - quiesce writes at B (brief), confirm A's lag ≈ 0,
   - promote A, demote B to standby of A,
   - repoint DNS/routing to A.
5. Verify both: A `primary`, B `standby`.

---

## Backup & restore

HA/DR is not a backup — replication faithfully copies deletions and corruption.
Keep independent backups:
- **Database:** the `backup_database` scheduled task (Settings ▸ Tasks), or
  `pg_dump`. Restore into a fresh primary, then re-seed standbys.
- **Content + artwork:** filesystem/ZFS snapshots, or object-storage versioning.
- **`SECRET_KEY`:** back it up out-of-band — without it, encrypted
  `server_settings` (storage creds, SMTP, OIDC, etc.) and existing tokens are
  unrecoverable.

Test restores periodically; an untested backup is a hypothesis.
