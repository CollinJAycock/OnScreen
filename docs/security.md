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
| `:7368/udp` | `DISCOVERY_PORT` | LAN auto-discovery | Unauthenticated; broadcasts server name / machine-id / version. Set `DISCOVERY_ENABLED=false` on untrusted L2 segments. |

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

- **LAN discovery** (`:7368/udp`) answers any prober with server name,
  machine-id, and version, unauthenticated. This is inherent to zero-config
  discovery; disable it (`DISCOVERY_ENABLED=false`) on untrusted networks.
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
  carry no rating in the schema). Parental controls cover on-demand media only.
- **`/admin/debug/explain/{name}`** runs `EXPLAIN (ANALYZE)`, which *executes*
  the (allowlisted, parameterized) query. Admin-only.

## Operator checklist

- [ ] Serve the API over TLS (reverse proxy or built-in `TLS_CERT_FILE`/`TLS_KEY_FILE`).
- [ ] Keep `METRICS_ADDR` on loopback or firewall `:7071`.
- [ ] Set the same `SECRET_KEY` on every fleet worker; never use a dev placeholder.
- [ ] Use a TLS SMTP relay (port 465 or STARTTLS); cleartext to a remote relay is refused.
- [ ] Disable LAN discovery on untrusted segments.
- [ ] Leave LDAP `skip_tls_verify` off in production.
