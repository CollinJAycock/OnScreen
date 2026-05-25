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

### 1. PostgreSQL primary
Reads scale via replicas, but a primary failure stops writes.

**Close it:** streaming replication + automated failover. Options, easiest first:
- Managed HA Postgres (RDS / Cloud SQL Multi-AZ, Crunchy Bridge) — failover is the provider's problem.
- Self-managed: **Patroni** (or **CloudNativePG** on k8s) for leader election + automated promotion, with **pgBouncer/HAProxy** pointed at a floating primary endpoint.

**App work:** minimal — the RW/RO split already exists. Mostly ops: a virtual
primary endpoint pgBouncer can follow on promotion, and confirming the pool
reconnects cleanly across a failover.

### 2. Valkey
Holds sessions, the **leader lock**, transcode dispatch counters, rate limits,
and caches. If it dies, the singleton tier loses its lock and sessions drop —
so it must be HA *before* the leader-election guarantee actually means anything.

**Close it:**
- **Valkey Sentinel** — failover with minimal change (cheapest; recommended first step).
- **Valkey Cluster** — sharded, for when one node can't hold the keyspace/throughput.

**App work:** the client must accept a Sentinel/cluster topology (connection
string + failover-aware client). Confirm the master-lock + rate-limit paths
tolerate a failover blip (they already TTL-expire and retry).

### 3. Storage
Streaming is `http.ServeFile(file.FilePath)` — a local filesystem path. That's a
SPOF *and* the scaling ceiling, and it's the largest net-new piece of work.

**Close it:** a storage abstraction behind the current `FilePath`, with backends
for local FS, network FS, and **object storage (S3/GCS)**.

```go
// MediaStore abstracts where media bytes live so the streaming + scan tiers
// stop assuming a local filesystem path. Local wraps os.Open; S3/GCS issues
// range reads — or, better, hands back a presigned URL the client/CDN fetches
// directly, taking the bytes off the app tier entirely.
type MediaStore interface {
    // Open returns a range-seekable reader for playback input (direct play,
    // remux/transcode source, scan probe). Backs HTTP Range via Seek.
    Open(ctx context.Context, key string) (io.ReadSeekCloser, error)

    // Stat returns size + modtime for Content-Length, caching, and the
    // mtime+size hash-skip in the scanner (ADR-011).
    Stat(ctx context.Context, key string) (FileInfo, error)

    // SignedURL returns a short-lived URL a CDN or client can fetch directly,
    // bypassing the app tier. Returns "" when the backend can't offload (local
    // FS), so the caller falls back to streaming through the app.
    SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
```

`SignedURL` is the hinge for CDN offload (§ below). Local FS returns `""` → the
app serves via `ServeFile` exactly as today, so this is a non-breaking refactor
that *enables* object storage rather than requiring it.

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

## Suggested sequencing

1. **Valkey Sentinel** — failover for the lock/session/cache tier (cheap, makes the existing leader-election guarantee real).
2. **Postgres automated failover** — Patroni / CloudNativePG / managed Multi-AZ behind a floating endpoint.
3. **`MediaStore` abstraction + object-storage backend** — unblocks everything downstream; non-breaking (local FS stays the default).
4. **CDN + `SignedURL` offload** for artwork / direct-play / static assets.
5. **Static-ABR pre-encode for popular titles** — the real concurrency unlock.
6. *(deferred)* multi-region; *(separate track, only if pursuing path B)* multi-tenancy + billing + DRM.

Park multi-region and DRM until a concrete customer needs them — both are large
and shouldn't be built speculatively.
