# High Availability & Scale Roadmap

**Status:** planning. This is the path from "horizontally-scalable single-cluster"
(where OnScreen is today) to "no single point of failure, scales to very large
concurrent load." It is deliberately split from the fantasy of *being Netflix* —
see [Non-goals](#non-goals).

## Goal

Run OnScreen such that **any single node can fail without an outage**, and the
serving tier scales with concurrent streams rather than with one box's limits.
Concretely: survive the loss of an API instance, a Postgres node, a Valkey node,
a transcode worker, or a storage node, with automatic recovery and no manual
intervention.

## Non-goals

- **Being Netflix's *catalog*.** A licensed studio catalog requires certified DRM
  (Widevine L1 / PlayReady SL3000 / FairPlay) plus device attestation. An
  open-source, self-hostable server structurally cannot hold those keys, so a
  licensed-content service is out of scope. OnScreen's model is *content you own
  or have rights to* — no DRM required. This is a positioning choice, not a
  limitation to "fix."
- **Multi-region active/active writes** (single Postgres primary makes this hard).
  Read-local replicas + CDN edge cover most of the latency win; defer true
  multi-region until a concrete global customer needs it.

---

## Where we already are

A large share of the HA architecture already exists — this is the structural
advantage over SQLite-based servers (Plex/Emby/Jellyfin), which cannot do any of
the below:

| Capability | Status | Where |
|---|---|---|
| Stateless API tier, N replicas behind a load balancer | ✅ | `docker/docker-compose.ha.yml` (3 replicas + pgBouncer) |
| Connection pooling | ✅ | pgBouncer session mode (ADR-020) |
| Automatic leader failover for singleton work | ✅ | `internal/worker/master.go` — Valkey lease, 15s TTL, any instance takes over |
| Read/write DB pool split (read replicas) | ✅ | `DATABASE_RO_URL` (ADR-021) |
| Distributed transcode tier | ✅ | `cmd/worker` fleet, cost- + capability-aware dispatch |
| Event-sourced state + materialized views | ✅ | `watch_events` + `watch_state` / `hub_recently_added` / `watch_plays` |
| Cacheable, signed asset delivery (CDN-shaped) | ✅ | purpose-scoped asset token in `?token=` URLs |

The singleton background work (hub/matview refresh, partition maintenance,
scheduled scans) is already HA: it runs only on the leader, and leadership
re-elects automatically when the holder's Valkey lease expires.

---

## The three remaining single points of failure

Even `docker-compose.ha.yml` runs these as singletons.

### 1. PostgreSQL primary  ✅ app-side + substrate landed
Reads scale via replicas, but a primary failure stops writes.

**Close it:** streaming replication + automated failover. Options, easiest first:
- Managed HA Postgres (RDS / Cloud SQL Multi-AZ, Crunchy Bridge) — failover is the provider's problem.
- Self-managed: **Patroni** (or **CloudNativePG** on k8s) for leader election + automated promotion, with **pgBouncer/HAProxy** pointed at a floating primary endpoint.

**Done (app side):** `DATABASE_URL` accepts a multi-host failover DSN
(`primary,replica/db?target_session_attrs=read-write`); pgx records the extra
hosts as fallbacks, connects to whichever is read-write, and re-homes to a
promoted primary. The pool shortens `MaxConnLifetime` to 60s when fallbacks are
present (`buildPoolConfig`, [`internal/db/db.go`](../internal/db/db.go)) so writes
re-home within ~1 min of a graceful switchover; a crash failover recovers at once.
The RW/RO split (`DATABASE_RO_URL`) already exists.

**Done (substrate):** a 1-primary + 1-streaming-standby stack is in
[`docker/docker-compose.postgres-ha.yml`](../docker/docker-compose.postgres-ha.yml)
(standby self-bootstraps via `pg_basebackup -R`). Validated locally: primary→replica
propagation, replica rejects writes, and a `read-write` DSN listing the replica
first still lands on the primary.

**Still to do (ops):** the **automatic promotion** layer — managed Multi-AZ, or
Patroni/repmgr/CloudNativePG — on top of this substrate, plus a floating primary
endpoint. The app side needs nothing further.

### 2. Valkey  ✅ client support landed
Holds sessions, the **leader lock**, transcode dispatch counters, rate limits,
and caches. If it dies, the singleton tier loses its lock and sessions drop —
so it must be HA *before* the leader-election guarantee actually means anything.

**Done:** the client speaks **Sentinel** — set `VALKEY_SENTINEL_ADDRS` (+
`VALKEY_SENTINEL_MASTER`) and the server/worker connect via `valkey.NewFailover`
(go-redis `FailoverClient`, same `*redis.Client` downstream, so the master lock /
rate-limiter / caches are unchanged). `VALKEY_URL` still supplies auth/db. A
deployable stack is in [`docker/docker-compose.valkey-ha.yml`](../docker/docker-compose.valkey-ha.yml)
(1 master + 2 replicas + 3 sentinels, quorum 2, ~10s failover < the 15s lock TTL).

**Validated:** topology + replication on Docker; full master-loss promotion must
be exercised on a stable-clock Linux host (Docker Desktop's VM clock trips
Sentinel's TILT guard and suppresses local failover).

**Still to do:** **Valkey Cluster** (sharding) for when one node can't hold the
keyspace/throughput — Sentinel covers failover but not horizontal data scale.

### 3. Storage  ✅ abstraction landed (local backend); object-storage + offload next
Streaming was `http.ServeFile(file.FilePath)` — a local filesystem path. That's a
SPOF *and* the scaling ceiling, and it's the largest net-new piece of work.

**Close it:** a storage abstraction behind the current `FilePath`, with backends
for local FS, network FS, and **object storage (S3/GCS)**.

**Done:** the abstraction is [`internal/mediastore`](../internal/mediastore/mediastore.go) —
a `Store` interface (`Open` / `Stat` / `SignedURL`) plus a `Local` backend (wraps
`os.Open`) and a `Serve` helper.

```go
type Store interface {
    Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
    Stat(ctx context.Context, key string) (FileInfo, error)
    // "" (nil err) means "can't offload" → caller streams/reads through the app.
    SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

Byte paths routed through it so far:
- **Direct play / download** — `StreamFile`, `Download` in
  [`internal/api/v1/items.go`](../internal/api/v1/items.go) call `mediastore.Serve`,
  which streams with full Range support today, or — when a backend returns a
  non-empty `SignedURL` — 302-redirects the client to a CDN.
- **Transcode source** — `buildSourceURL` in
  [`internal/api/v1/transcode.go`](../internal/api/v1/transcode.go) prefers the
  store's `SignedURL`, so a worker reads source straight from object storage / a
  CDN; otherwise it falls back to the existing LAN stream-token URL.
- **Artwork** — the `/artwork/*` route + `artwork.Manager.Resize`
  ([`internal/artwork/artwork.go`](../internal/artwork/artwork.go)) read source
  images (existence check, full-size serve, and resize decode) through the store,
  so posters/fanart stored next to media (ADR-006) resolve from the bucket. The
  resize cache stays local — it's a regenerable server-side cache, not media bytes.

Every site is opt-in via `WithMediaStore`, and `Local.SignedURL` returns `""`
(can't offload), so single-node and shared-storage installs are byte-for-byte the
pre-abstraction behaviour: a non-breaking refactor that *enables* object storage
rather than requiring it.

**Object-storage backend — landed.** [`internal/mediastore/s3.go`](../internal/mediastore/s3.go)
is an S3-compatible `Store` (AWS S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare
R2) on minio-go: `Open`/`Stat` range-read the bucket, `SignedURL` returns a CDN
URL when a CDN base is set else a presigned bucket URL, and `Ping` backs the
admin "Test connection". A `mediastore.Provider` makes the active backend
hot-swappable, so enabling object storage applies without a restart. Config lives
in `server_settings` (encrypted) and is set from **Settings ▸ Integrations ▸
Storage** ([`web/src/routes/settings/storage`](../web/src/routes/settings/storage/+page.svelte))
via `GET`/`PUT`/`POST /settings/storage[/test]`. Default stays local FS.

**Listing primitive — landed.** The discovery gap the scanner needs is now an
optional `Lister` capability on the store: `Walk(ctx, prefix, fn)` enumerates
objects under a prefix. `Local` walks the FS tree; `S3` lists the bucket and maps
object keys back to the FilePath namespace so a walked key feeds straight into
`Open`/`Stat`; `Provider` delegates (clear error if the backend can't list). Unit-
and MinIO-integration-tested. Also the enumerator static-ABR pre-encode will use.

**Still to do — wire the scanner onto it.** The scanner is the last and most
entangled byte consumer; with `Walk` in place the remaining work is mechanical but
spread across ~12 files:
- swap the discovery `filepath.WalkDir` (`internal/scanner/scanner.go`) for
  `store.Walk`, applying the dir-skip rules per key segment;
- probe over a **presigned URL** (`ffprobe -i <signed-url>`) instead of a local
  path when the source isn't local;
- route the hash (`hash.go`) and sidecar reads (NFO, `folder.jpg`, EXIF, embedded
  covers) through `Open`/`Stat`;
- the fsnotify **watcher** stays local-only — object storage has no inotify; live
  ingest there needs bucket event notifications, a separate effort.

**Also still to do:** a stable content *key* (today the key is the absolute
`FilePath`) for multi-site portability — see the "Content addressing" gap under
Multi-Site.

`SignedURL` is the hinge for CDN offload (§ below).

Closing all three = genuine HA: no SPOF, rolling deploys, survive any single node.

---

## Scaling out (beyond HA): the byte-delivery problem

At high concurrency the bottleneck isn't the database — it's **moving bytes**.
Today every byte flows through the app/fleet tier.

### CDN in front of cacheable assets
Artwork, direct-play files, and *static* segments are cacheable. The
purpose-scoped asset token (`?token=` signed URLs) is already CDN-shaped, and
`MediaStore.SignedURL` lets the app hand the client/CDN a direct fetch. Put
CloudFront / Cloudflare / Fastly in front of those and the app tier stops being a
byte pipe for the cacheable majority.

### Static ABR for popular titles — the highest-leverage scale change
Live ABR transcode segments are **per-session** and don't cache. The fix is to
**pre-encode the ABR ladder once** for popular titles, store the segments in
object storage, and serve them from the CDN forever; only the long tail is
live-transcoded on the fleet.

- Popularity signal already exists: top-played from `watch_plays` (the analytics
  matview) picks the pre-encode set.
- The encode jobs reuse the existing fleet + dispatcher.
- Result: the expensive live-transcode tier only handles cold/rare content, and
  the hot path is static CDN bytes — the difference between "scales with GPUs"
  and "scales with cache."

### Multi-region (deferred)
True global low-latency needs read-local replicas per region + CDN edge +
geo-routing. Single-primary writes make active/active hard. Do read-replica +
CDN first; only chase multi-region for a real global customer.

---

## Two credible "higher sights"

"Take on Netflix" only resolves into a coherent target once you separate tech
from catalog:

- **(A) The best self-hosted HA platform** — for orgs running real deployments at
  scale: a university, a hotel chain, a creator with a large owned library, a
  community server. Pure engineering; the roadmap above gets there.
- **(B) A multi-tenant "streaming-platform engine"** — OnScreen as the backend a
  studio / creator / regional service runs to stream content *they* hold rights
  to. You sell infrastructure, not a licensed catalog, so you sidestep the DRM
  wall. Needs multi-tenancy + billing + (optional) DRM integration on top of the
  HA base. This is the legitimately-ambitious path.

---

## Multi-Site / Geo-Distribution

The tier *above* single-cluster HA: two (or more) physical locations, each with
an identical copy of the library, so a site can serve locally and survive the
loss of the *other* site. This is where OnScreen's foundations create a moat the
SQLite-based competitors structurally can't cross.

"Identical libraries at two locations" splits into two problems of very
different difficulty.

### The easy half — identical content at both sites
- **TrueNAS/ZFS snapshot replication** to a second box is a built-in, scheduled
  feature; artwork rides along because it lives next to the media (ADR-006).
- The **`MediaStore`** abstraction (HA step 3) is the cloud equivalent: content
  keyed in object storage with cross-region replication.

So the bytes being identical at both sites is essentially a storage-layer task.

### The hard half — shared state across sites
Postgres holds the metadata, users, auth, and watch state. Three models, by
ascending difficulty:

| Model | What it gives | Cost |
|---|---|---|
| **Active / passive (DR)** | One primary site; a warm standby with async Postgres streaming replication + ZFS-replicated content. Promote on site failure. | Low — it's "Postgres failover" (step 2) stretched across a WAN. **Do this first.** |
| **Active / active *reads*** | Both sites serve playback + browse locally (content + a read replica are local); writes (progress, scans, account edits) go to one primary site. Cross-site write latency is invisible on a progress beacon. | Medium — rides directly on the existing RW/RO pool split (`DATABASE_RO_URL` → a local replica per site). |
| **Active / active *writes*** | Either site accepts writes, no primary. | High — Postgres isn't multi-master. See the event-sourcing note below; the rest needs conflict handling or commercial multi-master (EDB BDR). Don't chase without a hard requirement. |

### Why OnScreen is unusually suited to this
- **PostgreSQL, not SQLite** — WAN streaming replication is first-class. Plex /
  Emby / Jellyfin have no consistent multi-site story; this is structural, not a
  feature gap they can patch.
- **Event-sourced watch state** — `watch_events` is append-only, so it *merges*
  across sites without conflict: each site appends its own events and they
  reconcile. The state that's normally hardest to distribute is the easiest here
  — the one piece that could even support active/active writes.
- **Stateless API + PASETO + signed asset/stream tokens** — a token minted at
  site A is valid at site B (shared `SECRET_KEY`); no sticky sessions.
- **TrueNAS/ZFS** — content replication out of the box.

### Gaps to close (beyond the HA tiers)
1. **Content addressing.** The scanner stores absolute `FilePath`s, which differ
   per site. `MediaStore`'s stable *key* (resolved locally at each site) is the
   fix — this is the linchpin that makes content portable across sites.
2. **Per-site Valkey.** Sessions/locks are per-site, so cross-site "play on this
   device" / live-supersede won't span sites without a federation layer. Minor —
   per-site is acceptable for almost everything.
3. **Geo-routing.** DNS/anycast or per-site hostnames so users land on the
   nearest site; session epoch (revocation) propagates with replication lag.

### Sequence
Finish within-site HA → **active/passive DR across two TrueNAS sites** (cheap,
high value, leans on ZFS + Postgres streaming replication) → **active/active
reads** if both sites should be live. Active/active writes only if forced.

---

## Suggested sequencing

1. **Valkey Sentinel** — ✅ client support + deployable stack landed; failover for the lock/session/cache tier makes the existing leader-election guarantee real.
2. **Postgres failover** — ✅ app-side (multi-host failover DSN + lifetime tuning) + replication substrate (`docker-compose.postgres-ha.yml`) landed & validated. Remaining: the **automatic promotion** orchestrator (Patroni / CloudNativePG / managed Multi-AZ) behind a floating endpoint — pure ops.
3. **`MediaStore` abstraction + object-storage backend** — ✅ abstraction + `Local` backend + direct-play integration landed (`internal/mediastore`), non-breaking (local FS stays the default). Remaining: the S3/GCS backend + routing transcode/scan/artwork byte paths through it. Unblocks everything downstream.
4. **CDN + `SignedURL` offload** for artwork / direct-play / static assets.
5. **Static-ABR pre-encode for popular titles** — the real concurrency unlock.
6. **Multi-site active/passive DR** — two TrueNAS sites, ZFS + Postgres streaming replication; the first step into [geo-distribution](#multi-site--geo-distribution). Then active/active reads.
7. *(deferred)* multi-region (many regions / global); *(separate track, only if pursuing path B)* multi-tenancy + billing + DRM.

Park multi-region and DRM until a concrete customer needs them — both are large
and shouldn't be built speculatively. Multi-site DR (step 6), by contrast, is a
natural payoff of the HA tiers + ZFS and worth doing once `MediaStore` lands.
