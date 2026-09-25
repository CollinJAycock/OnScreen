# Security & Operational Hardening

This document records OnScreen's server-side security posture: the network
surfaces an operator must control, the behaviors that differ from a naive
default, and the residual risks that are accepted by design. It reflects the
hardening pass tracked in the codebase as of v2.4.

## Ports & network exposure

| Port (default) | Env | Purpose | Exposure guidance |
|---|---|---|---|
| `:7070` | `LISTEN_ADDR` | HTTP/S API + web UI | Public (front with TLS / reverse proxy) |
| `127.0.0.1:7071` | `METRICS_ADDR` | Prometheus `/metrics` **and unauthenticated `/debug/pprof`** | Loopback by default — keep private. pprof leaks heap/goroutine memory (live tokens), cmdline, and webhook URLs. Only widen behind a firewall. |
| `:1935` | `RTMP_LISTEN_ADDR` (if `RTMP_ENABLED`) | RTMP "go live" ingest | Reachable by broadcasters; publishes require a valid stream key. |
| `:7073` | `WORKER_ADDR` (standalone `cmd/worker`) | Transcode-fleet HLS segment server | **Now requires a `SECRET_KEY`-derived bearer** (see below). The embedded worker binds loopback. |
| `:7368/udp` | `DISCOVERY_PORT` | LAN auto-discovery | Unauthenticated; answers only loopback / private / link-local senders with server name / machine-id / version. Set `DISCOVERY_ENABLED=false` on untrusted L2 segments. |

## Behavior changes from the hardening pass

These differ from prior behavior; operators upgrading should be aware:

1. **Metrics/pprof bind to loopback.** `METRICS_ADDR` defaults to
   `127.0.0.1:7071`. Remote Prometheus scraping requires setting it explicitly
   (e.g. `:7071`) and firewalling the port.
2. **Worker segment server authentication.** Each transcode worker's
   `/segments` + `/seghead` endpoints require an `Authorization: Bearer` token
   derived from `SECRET_KEY` (`transcode.SegmentProxyToken`). The API attaches
   it automatically. **All fleet nodes must share the same `SECRET_KEY`** (they
   already need it). A worker started without `SECRET_KEY` leaves its segment
   server open (no breakage, but unprotected) — always set it.
3. **SMTP requires transport security to remote relays.** Port `465` uses
   implicit TLS; otherwise STARTTLS is used when offered. If a **non-loopback**
   relay does not offer STARTTLS, the send is **refused** rather than
   transmitting password-reset / invite tokens in cleartext. Use port 465 or a
   TLS-capable relay. A loopback relay (local MTA) is exempt.
4. **PIN-switch cannot enter admin accounts.** `POST /auth/pin-switch` now
   rejects a target whose account is an admin — a 4-digit PIN must not grant
   admin privileges. Admins authenticate with full credentials. Non-admin
   household-profile switching is unchanged.
5. **Subtitle language is allowlisted.** The external-subtitle download
   `language` field must match an ISO-639-style tag; it is interpolated into an
   on-disk filename and was previously a path-traversal vector.
6. **Static-ABR enforces the content-rating ceiling** and fails closed (requires
   authenticated claims), matching the live streaming path.
7. **Image decode is bounded.** Source images are rejected before decode when
   their declared dimensions exceed ~100 MP (and, for artwork, encoded size
   exceeds 64 MiB) — guards against decompression / pixel-flood OOM.
8. **First-run setup is Host-checked.** While no account exists, `POST
   /auth/register` additionally requires the browser to have addressed the
   server by IP, `localhost`, a single-label name or a local suffix (`.local`,
   `.lan`, `.home`, `.home.arpa`, `.internal`) — a DNS-rebinding page on a
   public domain is refused. Complete setup via the IP once, or set
   `ALLOW_PUBLIC_SETUP=true`.
9. **Credential endpoints refuse cross-origin form posts.** Login, LDAP login,
   TOTP verify, register and invite-accept reject a cross-origin request whose
   body is not `application/json` (login CSRF). Every first-party client sends
   JSON; native clients send no browser headers and are unaffected.
10. **TOTP codes are single-use.** A second action within the same 30 s
    window needs the next code. Step-up re-auth and TOTP disable have their own
    per-user failure cap (10 per 15 min), separate from the login second-factor
    cap, so a stolen session cannot lock the real user out of signing in.
11. **SAML sign-in is bound to the starting browser** by a short-lived
    `onscreen_saml_bind` cookie (`SameSite=None; Secure` over HTTPS). An
    assertion completed in a different browser is refused.
12. **Parental ceiling now covers photos, EXIF, lyrics, subtitles and
    watch-status.** Unrated items rank most restrictive, so a capped profile no
    longer sees unrated photos' titles/GPS or unrated tracks' lyrics.
13. **Library-scoped leaks closed.** `/jobs`, scan-complete notifications,
    server-owned collections and watch-status now respect library ACLs.
14. **Live TV quotas.** At most 6 concurrent live sessions (`HLSConfig`),
    8 concurrent DVR captures (3 per user), 100 schedules per user, bounded
    padding, 12 h max recording; non-admins cannot list or tune disabled
    channels. Media requests are capped at 25 pending per user.
15. **Unsafe library files are skipped.** A "media" file that is really an HLS
    / DASH / concat / SDP playlist is refused at scan and at playback (415
    `UNSUPPORTED_CONTAINER`), closing ffmpeg reference-following.
16. **Operator-facing detail moved off the public port.** `/health/version`
    publicly returns only the version; build time and Go version are on the
    metrics port.
17. **Stored credentials don't follow a changed endpoint.** Changing an *arr
    service's `base_url`, the SMTP host, the LDAP host or the OIDC issuer URL
    now requires re-entering (or clearing) that integration's secret in the
    same save; otherwise the save is refused with 422 and nothing is written.
18. **First-run "local" peers** also include Tailscale/CGNAT (`100.64.0.0/10`)
    and addresses inside this host's own IPv6 /64s, so dual-stack LANs can
    complete setup over IPv6.

## Deployment, packaging & supply-chain hardening

Changes to the shipped compose files, reverse-proxy sample, installers and CI.
Items marked **upgrade** need operator action on existing installs.

**Reverse proxy (`docker/nginx.conf`, HA stack)**

- All `proxy_set_header` directives now live at server level. nginx only
  inherits them into a location that sets none of its own, so the old
  per-location `Upgrade`/`Range` headers silently dropped `Host`,
  `X-Forwarded-For/Proto` and `X-Real-IP` on `/api/`, `/media/` and `/`. The
  server then saw every client as nginx's private IP: the local-only first-run
  setup gate was reachable from the internet, all per-IP rate limits shared one
  bucket, and auth cookies lost `Secure`. **Do not add `proxy_set_header` or
  `add_header` inside a location**; a CI step in `ci.yml` fails if one does.
- nginx is the edge, so `X-Forwarded-For` is *replaced* with `$remote_addr`
  (not appended). If you put a CDN/proxy in front of nginx, configure
  `real_ip_header` / `set_real_ip_from` there.
- `/artwork/` no longer adds `Cache-Control: public, immutable`; the server's
  per-response `public`/`private` decision (rating ceiling, private libraries)
  passes through, so shared caches can't replay restricted art.
- The access log omits query strings and the Referer query (`?token=` stream
  credentials were being logged). The *error* log can still contain full
  request lines; treat it as sensitive.
- `server_tokens off`; HSTS without `includeSubDomains; preload` (opt in
  yourself); deprecated `X-XSS-Protection` removed; the restore upload gets its
  own 2 GiB body limit. **upgrade:** reload nginx.

**Compose files**

- `DB_PASS` is required (`${DB_PASS:?}`) everywhere; the public default
  `onscreen` is gone (`docker-compose.yml`, `.truenas.yml`, `.gpu.yml`,
  `.workers.yml`, installer `docker-compose.deps.yml`). **upgrade:** an existing
  Postgres volume keeps the password it was created with — set `DB_PASS` to it
  (almost certainly `onscreen`) or rotate first with
  `ALTER ROLE onscreen PASSWORD '...'`. The installer `start-deps` scripts
  generate a random `DB_PASS` for new stacks and keep `onscreen` (with a
  warning) when they find an existing volume.
- App containers (server, worker, migrate) run with
  `no-new-privileges` and `cap_drop: [ALL]` (they are already non-root);
  nginx gets `no-new-privileges` only.
- `docker-compose.postgres-ha.yml`: the replication role can use its own
  `REPLICATION_PASSWORD` (falls back to `DB_PASS`), the password is passed to
  SQL as a quoted psql variable (it used to be spliced into the statement), and
  replication is accepted only from `REPLICATION_ALLOWED_CIDR` (default
  `samenet`, i.e. the compose network). **upgrade:** these apply at first
  init only; an existing cluster keeps its role/`pg_hba.conf` — rotate with
  `ALTER ROLE replicator PASSWORD ...` and edit `pg_hba.conf` by hand. Set the
  standby's IP/32 for a cross-host DR standby, and put that link on TLS.

**Windows installers**

- MSI: new clusters are initialised with `--auth=scram-sha-256` (initdb's
  default was `trust`: any local process could connect as the Postgres
  superuser, then run OS commands via `COPY ... TO PROGRAM`). Upgrades rewrite
  `trust` lines in `pg_hba.conf`, verify the `.env` password authenticates, and
  roll back with a logged warning if it doesn't.
- MSI: `%ProgramData%\OnScreen` (pgdata, logs) is no longer `Users: Modify`;
  it and the install directory are reset to SYSTEM + Administrators (Users
  read/execute on the install dir), including on upgrade. `.env` and the
  service XMLs (which carry `SECRET_KEY` and the DB DSN) are SYSTEM +
  Administrators only. Opening the logs folder from a non-elevated Explorer
  now prompts for admin.
- Portable zip: `install-service.ps1` applies the same ACLs to its folder (a
  folder under `C:\` inherits *Authenticated Users: Modify*, which let any user
  replace the LocalSystem service's binaries).
- `devtoken` (mints admin tokens from `SECRET_KEY`) is no longer shipped in
  any installer; MSI upgrades and `install-service.ps1` delete old copies.
- Build scripts verify a pinned SHA-256 for every bundled third-party download
  *and* cached copy, and fail on mismatch. goose's pin is vendor-verified; the
  WinSW, Gyan ffmpeg, EDB PostgreSQL and Redis pins were taken from the
  maintainer's existing caches (those vendors publish no checksums), so they
  pin "what we already ship" rather than attest it. The Linux build's optional
  johnvansickle ffmpeg is a moving URL and stays unpinned (warns).

**Linux tarball**

- `install-service.sh` makes `.env` `0600` owned by the run user and warns
  when that user is in `docker`/`lxd`/`sudo`/`wheel`/`admin`.
- The systemd unit adds `ProtectKernelTunables/Modules`,
  `ProtectControlGroups`, `ProtectHostname`, `ProtectProc=invisible`,
  `RestrictNamespaces/Realtime/SUIDSGID`, `LockPersonality`,
  `SystemCallArchitectures=native` and `RestrictAddressFamilies`. It leaves
  out options that break GPU transcoding or common layouts (see the unit's
  comments). **upgrade:** re-run `install-service.sh`.
- DB DSNs are passed to goose via `GOOSE_DBSTRING` (environment) instead of
  argv in `migrate.sh`, `docker/deploy-gpu.sh`, the Makefile and `deploy.ps1`.

**Images & CI**

- `Dockerfile.ffmpeg` verifies the FFmpeg tarball's PGP signature against the
  pinned release-key fingerprint and checks out nv-codec-headers, Vulkan-Headers
  and libplacebo at pinned commit SHAs; the build fails on any mismatch.
  Bumping a version means bumping its `*_COMMIT` too.
- Dockerfiles and CI use `npm ci` (lockfile-exact) instead of `npm install`.
- Every workflow defaults to `permissions: contents: read`; the release is
  split so only small publish jobs (no project/dependency code) hold
  `contents:write` / `packages:write`. Third-party and first-party actions are
  pinned to commit SHAs (Dependabot updates them), golangci-lint and
  govulncheck are pinned, and release binaries and the image carry signed
  build-provenance attestations
  (`gh attestation verify <file> --repo <owner>/<repo>`).
- Dependabot now also covers the Android Gradle projects, the client npm
  projects and the desktop client's Cargo crates.
- `make dev` no longer uses a published constant `SECRET_KEY`; it generates a
  random one into `.dev-secret-key` (gitignored). **upgrade:** to keep a dev
  DB's encrypted settings readable, put the old key in that file.

### Deferred (need coordinated operator steps)

- **The app connects to Postgres as a superuser** in every shipped deployment
  (Docker `POSTGRES_USER` is created as superuser; the MSI uses `postgres`).
  A crafted restore dump or any SQL injection can then reach the OS
  (`COPY ... TO PROGRAM`). Plan: create a `NOSUPERUSER NOCREATEROLE` app role
  that owns the database, keep the superuser for init/`CREATE EXTENSION` and
  migrations (`MIGRATE_DATABASE_URL`), and run `pg_restore --no-owner
  --no-privileges --role=<app>`. Existing installs need a one-time
  `CREATE ROLE` + `REASSIGN OWNED` and a `DATABASE_URL` change.
- **Windows services run as LocalSystem** (OnScreen, Postgres, Redis). Plan:
  `<serviceaccount>` = a virtual account (`NT SERVICE\OnScreen`), with modify
  on its data/log dirs and read on media; test NVENC/QSV probing and network
  share access under that account first. The worker scheduled task still runs
  with `RunLevel Highest` because it writes its log in Program Files and reads
  the admin-only `.env`; moving the log to `%ProgramData%` lets it drop to
  `Limited`.
- **Bundled Redis-for-Windows (tporadowski 5.0.14.1) is unmaintained** and
  has no `requirepass`; it is bound to 127.0.0.1, so any local process can use
  it. Replace it with a maintained store and add a password. PostgreSQL 17.5
  should move to the current 17.x minor (update the pinned hash with it).
- Installers and binaries are not Authenticode/minisign signed.
- Base images are tag-pinned, not digest-pinned (the Dockerfiles deliberately
  track Go/Alpine patch tags); the HA stack's `bitnami/pgbouncer` image is no
  longer maintained upstream.

## SSRF posture

Outbound HTTP goes through `internal/safehttp`, which validates the
**post-DNS-resolution** IP at dial time (closing DNS-rebinding) and re-checks
every redirect hop. The default policy blocks loopback, RFC1918, RFC6598 CGNAT
(`100.64/10`), link-local (incl. cloud-metadata `169.254.169.254`),
unspecified, multicast, and IPv4 embedded in **NAT64 (`64:ff9b::/96`) / 6to4
(`2002::/16`)** wrappers. The plugin egress path shares this denylist.

**Accepted exceptions** (admin-configured destinations that legitimately live on
the LAN for a self-hosted deployment): OIDC/SAML metadata, LDAP, Sonarr/Radarr,
and S3/MinIO clients permit RFC1918 + loopback, and the HDHomeRun stream client
additionally permits link-local for tuner auto-config. In every case the
**cloud-metadata link-local range stays blocked** for the non-HDHomeRun paths,
so these cannot be used to reach `169.254.169.254`. The residual risk — a
compromised admin using the server to probe its own LAN, or a same-LAN attacker
spoofing an HDHomeRun — is accepted for the self-hosted threat model.

## Accepted residual risks (by design)

- **LAN discovery** (`:7368/udp`) answers any local-network prober with server
  name, machine-id, and version, unauthenticated. This is inherent to
  zero-config discovery; disable it (`DISCOVERY_ENABLED=false`) on untrusted
  networks.
- **LDAP `skip_tls_verify`** is an explicit admin opt-in for dev/self-signed
  directories. When enabled, the bind is exposed to a TLS MITM. Do not enable
  in production; documented in the Settings UI.
- **`SECRET_KEY` is returned to admins** by the worker-credentials reveal
  endpoint (needed to provision fleet workers). It is gated by step-up
  re-authentication (password + TOTP) and audit-logged, and its
  confidentiality then depends on transport security — **serve the API over
  TLS** anywhere this endpoint is reachable.
- **Session-epoch revocation fails open during a DB outage.** A just-revoked
  token can survive until the DB recovers or the token's ~1 h TTL elapses. This
  is deliberate — a transient DB blip must not log every user out. Deleted
  users still fail closed.
- **Live TV streaming is not gated by the content-rating ceiling** (channels
  carry no rating in the schema). Parental controls cover on-demand media only;
  a per-profile "no Live TV" switch is the planned follow-up.
- **`TRUSTED_PROXIES` unset trusts private-range peers' forwarding headers.**
  This keeps the common LAN reverse-proxy setup working out of the box; on a
  network where untrusted hosts share the server's private range, set
  `TRUSTED_PROXIES` to the proxy's exact address.
- **The token pair is also returned in the JSON body** of login/refresh, not
  only as httpOnly cookies. Native and TV clients have no cookie jar and need
  it; the web client keeps it in memory only. CSP (nonce `script-src`) is the
  control against script running on the origin.
- **Download links carry a `?token=`**, because mobile download managers
  cannot send cookies. It is a file-bound stream token (`purpose=stream`,
  single `file_id`, session-epoch checked), never a general access token; the
  nginx sample strips query strings from access logs.
- **Single-device logout does not revoke HLS segment tokens**; segment tokens
  are revocable only per user, and revoking them would stop the user's other
  devices. Refresh-token theft, password change/reset, demotion and deletion do
  revoke them. Per-device revocation needs a session id in the access token.
- **The first-run setup gate trusts the peer address.** Behind Docker
  Desktop, rootless Podman, or docker-proxy on the IPv6 path, every client
  appears to come from the (private) bridge gateway, so an internet client can
  pass the local-peer check on a fresh, unconfigured install that is already
  published. Finish setup immediately after first start, and don't expose an
  unconfigured instance. A one-time setup token printed to the server log is
  the planned hardening.
- **`https://www.gstatic.com` is allowed on `script-src`/`frame-src`** for the
  Google Cast sender SDK. It is not narrowed to a path: the SDK loads further
  scripts from several gstatic paths, and a wrong path silently breaks Cast.
  The nonce-based `script-src` means an injected `<script>` still needs a
  gstatic-hosted gadget.
- **ABR rung children are not counted separately** against the per-user
  stream cap. Each parent session has a fixed ladder (`findRung` rejects
  unknown rungs) and forced restarts are rate-floored, so the worst case is
  cap × ladder size. ABR is off by default (`TRANSCODE_ABR=false`).
- **`/admin/debug/explain/{name}`** runs `EXPLAIN (ANALYZE)`, which *executes*
  the (allowlisted, parameterized) query. Admin-only.

## Operator checklist

- [ ] Serve the API over TLS (reverse proxy or built-in `TLS_CERT_FILE`/`TLS_KEY_FILE`).
- [ ] Rotate `SECRET_KEY` (`cmd/rotate-key`) if it was ever a placeholder or the
      old `make dev` default — the server now refuses to boot with either.
- [ ] Keep `METRICS_ADDR` on loopback or firewall `:7071`.
- [ ] Set the same `SECRET_KEY` on every fleet worker; never use a dev placeholder.
- [ ] Use a TLS SMTP relay (port 465 or STARTTLS); cleartext to a remote relay is refused.
- [ ] Disable LAN discovery on untrusted segments.
- [ ] Leave LDAP `skip_tls_verify` off in production.
- [ ] Set a strong `DB_PASS` (compose files refuse to start without one).
- [ ] Behind a reverse proxy, confirm it forwards `X-Forwarded-For`/`-Proto` on
      every route (the first-run setup gate and rate limits depend on it); set
      `TRUSTED_PROXIES` to the proxy's address when you know it.
- [ ] Windows: install under `%ProgramFiles%` (or re-run `install-service.ps1`
      so the folder ACL is reset); keep `.env` admin-only.
