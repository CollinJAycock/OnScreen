# Federation design (draft)

Status: **draft for discussion, 2026-11-05.** Not scheduled: not in the v3
scope ([v3-plan.md](v3-plan.md)), a candidate for v3.x or v4.

Federation lets two independent OnScreen servers, each with its own
database and its own admin, share libraries. A user on server A sees
libraries that server B shared with A, and plays them **directly from B**.
There's no load balancer, shared database or central service: only the two
servers and the user's app. This is a different problem from HA and
multi-site ([ha-roadmap.md](ha-roadmap.md)), which assume one organisation
and one database.

## Goals

- A server admin can pair with another server and share chosen libraries
  with it, with the same controls as sharing with a local user (rating
  ceiling, how many streams).
- Users on the receiving server browse those libraries alongside their own,
  and play them from the server that holds the files.
- **Video never passes through the user's home server.** It goes from the
  content server to the app.
- No central service: no OnScreen account, no relay, no cloud.
- Either admin can end the arrangement at once.

## Non-goals (for now)

- More than a handful of peers per server, or public discovery of servers.
- Sharing watch history, ratings or users between servers.
- Writing to a peer's library (no Fix Match, no metadata edits on remote
  items).
- Live TV and DVR across servers.
- A relay for content servers the app can't reach (see Later phases).

## Terms

| Term | Meaning |
|---|---|
| **Home server** (A) | The server the user signs in to. Owns the user's account, sessions and watch state. |
| **Content server** (B) | The server that has the files and shares a library with A. |
| **Peer** | A paired server, from either side's point of view. Usually each is both. |
| **Slip** | A short-lived, single-use token A signs so the app can start one stream on B. |
| **Session** | B's normal playback session, with B's own token, kept alive by the player. |

## Overview

```
 App ──(sign-in, browse, progress)──►  A (home)  ◄──(signed peer API: catalog sync)──►  B (content)
  │                                     │                                                ▲
  │◄──────────── slip (signed by A) ────┘                                                │
  └────────────── present slip, then HLS / direct stream with B's session token ────────┘
```

1. **Pairing:** the admins exchange server identities and public keys once.
2. **Sharing:** B's admin shares libraries with A; A's admin chooses which
   of A's users see them.
3. **Catalog sync:** A keeps a local copy of what B shares, so browsing is
   fast and works while B is offline.
4. **Playback:** A gives the app a slip; the app takes it to B; B checks it
   against what B shared and starts its normal session; the app streams
   from B.
5. **Progress:** the app reports progress to A, as today.

## 1. Server identity and pairing

**Identity.** Each server gets a federation key pair (Ed25519) on first
use, stored encrypted in `server_settings` like the other keys, and is
identified by its existing `machine_id` (already returned by
`/system/capabilities`). The public key and `machine_id` together are the
server's identity; the private key never leaves it.

**Pairing** reuses the shape of device pairing (`/auth/pair/code`,
`/poll`, `/claim`):

1. Admin A opens Settings › Federation › Pair a server and gets a PIN plus
   A's public address.
2. Admin B enters A's address and the PIN on B.
3. B calls A over the hardened HTTP client (`internal/safehttp`) and
   presents the PIN. Each side sends its `machine_id`, display name, public
   key and URL, signed with its own key.
4. Both admins see the other server's name and key fingerprint and confirm.
   The pairing is active only when both have confirmed.

The PIN is single-use, expires in 10 minutes and is rate-limited, like
device pairing's. Pairing is symmetric: once paired, either side may share
with the other, but each side shares nothing until its admin chooses to.

**Server-to-server calls** (catalog sync, revocation notices) carry a
PASETO **v4.public** token signed by the caller, with `iss` = caller's
`machine_id`, `aud` = callee's `machine_id`, a 60-second `exp` and a `jti`.
The callee verifies it with the stored public key. These are separate from
user tokens, which stay v4.local.

## 2. Sharing and access

**On B (sharing out):** for each peer, the admin picks libraries and sets:

- a **rating ceiling** for everything that peer's users play;
- a **concurrent stream cap** for the peer as a whole, and optionally a
  transcode cap, so one household can't use all of B's GPU;
- optional **labels** to exclude, as with local users.

**On A (sharing in):** the peer's shared libraries appear in Settings ›
Libraries as remote libraries. The admin grants them to users the same way
as local libraries; nobody sees them until granted. A user's own rating
ceiling still applies.

**Effective ceiling** = the stricter of B's ceiling for the peer and A's
ceiling for the user. B enforces its own; A passes the user's in the slip,
and B applies the stricter.

**Privacy:** B never learns A's user names, emails or user ids. A sends a
**peer-scoped pseudonymous id**, `HMAC(A's federation key, user_id ‖ B's
machine_id)`, stable per user and peer so B can count streams per user, and
meaningless to anyone else.

## 3. Catalog sync

A keeps a local copy of B's shared catalog in `remote_items`, so Home rows,
library grids, search and the A–Z rail work at local speed and still list
remote items while B is offline (playing them needs B).

- **Pull, incremental:** a scheduled task on A (every 15 minutes, and on
  demand) calls B's `GET /api/v1/federation/catalog?library=…&since=<cursor>`.
  B returns items changed since the cursor, plus tombstones for removed ones,
  and a new cursor. The first sync pages through everything.
- **What's copied:** what browsing needs (id, type, parent, title, sort
  title, year, index numbers, duration, summary, genres, content rating,
  artwork paths, version count, `updated_at`). Not file paths, not watch
  state, not B's users.
- **Untrusted input:** every string from a peer gets the validation the
  plugin host applies to plugin-returned metadata (`internal/plugin`:
  trimming, length limits, no NUL bytes, date and URL checks), plus
  stripping control and bidi-override characters, which is new.
- **Artwork** is fetched by the app straight from B with an artwork slip
  (section 5). A doesn't download B's images, which saves A's bandwidth too.

`remote_items` rows carry `peer_id` and B's item id. A's existing queries
join them in where a user has the remote library granted; the API marks
each remote item with `source: {peer_id, name}`.

## 4. Playback: the slip

When the user presses Play on a remote item, the app asks A
(`POST /api/v1/items/{id}/play` as today). A sees the item is remote and
returns a **slip** and B's address instead of a session.

**The slip** is a PASETO **v4.public** token signed with A's key:

| Claim | Value |
|---|---|
| `iss` | A's `machine_id` |
| `aud` | B's `machine_id` (unusable on any other server) |
| `sub` | the peer-scoped pseudonymous user id |
| `item` | B's item id |
| `scope` | `play` |
| `max_rating` | the user's rating ceiling on A (or none) |
| `exp` | now + 2 minutes |
| `jti` | random id, for single use |

**The app presents the slip to B** with its usual `X-Client-Capabilities`
header (`POST /api/v1/federation/play`). B:

1. verifies the signature with A's stored key, and `aud`, `exp` (with 60 s
   clock leeway) and that `jti` hasn't been used (kept in Valkey until
   `exp`);
2. checks the pairing is active and that the item is in a library B shared
   with A;
3. applies the stricter rating ceiling and B's own labels;
4. checks the peer's stream and transcode caps, counting per `sub`;
5. runs its **normal play decision** for the app's capability header:
   direct play, remux or transcode;
6. returns the usual session: absolute playlist / file URLs carrying **B's
   own session token**.

**A's slip only gets the user in.** After step 6 the slip is spent. The
stream runs on B's session token, which B can end at any time.

## 5. Keeping the stream alive

The two-minute slip doesn't limit the stream: it only has to reach B once.

- **HLS (remux / transcode):** the player fetches a segment every few
  seconds with B's session token. Each fetch keeps the session active. This
  is how OnScreen sessions work today: active while requests arrive, idle
  after 2 minutes without one (`transcode.ActiveSessionWindow`), gone after
  4 hours at most (`sessionTTL`).
- **Direct play:** the player reads the file in ranges; each range request
  carries the token and keeps the session active.
- **Pause:** a short pause keeps the session. After a long pause B has ended
  it, so on Play the app gets a fresh slip from A and resumes at the saved
  position, the same as resuming any expired session today.
- **Seeking** stays inside B's session.

**Artwork slips:** the same mechanism with `scope = artwork`, covering a
library instead of an item, a 1-hour `exp` and no single-use rule. The app
sends it with image requests to B.

## 6. Watch state

Watch state belongs to the home server. The app reports progress to **A**
exactly as for a local item; A records watch events against the remote
item, so Continue Watching, resume points, history and statistics include
it. B keeps only what it needs to run and limit sessions. B's own users'
history is untouched.

## 7. Revocation and failure

| Situation | What happens |
|---|---|
| Either admin unpairs | That side deletes the peer's key and tells the other side (signed notice). B ends any sessions started from A's slips; A hides the remote libraries. New slips are refused immediately. |
| B's admin stops sharing a library | Next sync removes it on A; B refuses new slips for it at once. |
| B is offline | A still lists the catalog (marked offline); Play shows "server B is unreachable". |
| A is offline | Users can't start new remote streams (no slips). Streams already running continue, since they only need B. |
| Key rotation | A server can rotate its key; it sends the new public key, signed with the old one, to each peer. |
| Clock skew | 60 s leeway on `exp`. Pairing warns if the two clocks differ by more than that. |
| A leaked slip | Useless after 2 minutes, after one use, or on any other server (`aud`). |

## 8. Security

- **Outbound requests to peers** go only through `internal/safehttp` (the
  SSRF-guarded client the plugin host uses), so a peer address can't be
  used to probe either server's network. Federation is the "user-controlled
  URL" feature the deferred SSRF hardening was waiting for.
- **Peer API rate limits**, per peer, on catalog and play endpoints.
- **Least data:** no file paths, no user details, no watch history cross
  servers.
- **Peers' content is untrusted** (titles, summaries, artwork).
- **Admin-only setup**, audited like other admin actions.
- **HTTPS required** for a peer's URL, except on private networks.

## 9. App changes

Every first-party app needs the same small set:

- Remote items carry `source`; show a small "from <server>" label on cards
  and title pages.
- On Play, handle the "remote" response: present the slip to B with the
  capability header, then play B's absolute URLs. Today's apps assume one
  server origin and one token.
- Send artwork requests for remote items to B with the artwork slip.
- The web app loads B's media cross-origin. B's token-URL routes already
  answer `Access-Control-Allow-Origin: *` for the Chromecast receiver; the
  new federation endpoints need the same.

**Fire TV:** Amazon has rejected builds over "media from third-party
sources". Content from someone else's server may draw the same objection,
so the Fire TV flavor should ship with federation off, as it leaves out Live
TV.

## 10. Schema (new tables)

Migrations from 00063:

| Table | On | Columns (main) |
|---|---|---|
| `federation_identity` | each server | key pair (private key encrypted), created_at |
| `federation_peers` | each server | id, machine_id, display_name, url, public_key, status (pending / active / revoked), confirmed_at, last_seen_at |
| `federation_shares` | content server | peer_id, library_id, max_rating, stream_cap, transcode_cap, excluded_labels |
| `remote_libraries` | home server | peer_id, remote_library_id, name, type, sync_cursor, last_synced_at |
| `remote_library_access` | home server | remote_library_id, user_id |
| `remote_items` | home server | peer_id, remote_id, remote_library_id, type, parent_remote_id, title, sort_title, year, index, duration, summary, genres, content_rating, artwork paths, updated_at |

Watch events for remote items reference `remote_items` (a nullable
`remote_item_id` beside `item_id`).

## 11. API surface (new)

On every server, `/api/v1/federation/…`:

| Endpoint | Caller | Purpose |
|---|---|---|
| `POST /pair/code`, `POST /pair/claim`, `POST /pair/confirm` | admins / peer | Pairing handshake |
| `GET /peers`, `DELETE /peers/{id}` | admin | List / unpair |
| `PUT /peers/{id}/shares` | admin (content side) | What to share with a peer |
| `GET /catalog` | peer (signed) | Incremental catalog sync |
| `POST /notify` | peer (signed) | Unpair / share-change / key-rotation notices |
| `POST /play` | app with a slip | Start a session for a remote user |

Plus the home server's existing play endpoint learns to return a slip for
remote items.

## 12. Phases

| Phase | Contents | Size |
|---|---|---|
| **1. Core** | Identity, pairing, sharing, catalog sync, slips, playback on B, progress on A, revocation; web app and one Android app | L (about 4–6 weeks) |
| **2. All apps** | The app changes in every client, artwork slips | M |
| **3. Polish** | One card for a title both servers have (prefer local, via the versions picker), search across peers, catalog-offline UX | M |
| **Later** | A relay through A when the app can't reach B; more than two peers at scale; sharing a "watched" state | L |

## Open questions

1. **Discovery:** should a server be able to advertise itself on the LAN
   for pairing (like LAN discovery for apps), or always by address and PIN?
2. **Default caps:** what stream and transcode caps should a new share get?
3. **Who pays for transcoding:** should B be able to share "direct play
   only", refusing transcodes for a peer's users?
4. **Downloads:** may A's users download B's files to the phone app for
   offline viewing, or only stream?
5. **Duplicates:** when both servers have a title, always prefer local, or
   let the user choose (higher quality on the peer)?
