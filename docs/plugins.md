# OnScreen Plugin Authoring Guide

OnScreen calls out to external **MCP servers** you configure as plugins.
OnScreen is the MCP *client*; your plugin is the MCP *server*. OnScreen never
accepts inbound MCP connections: plugins are reached outbound only.

A plugin registers as exactly one **role**, and OnScreen calls it only in
that role:

| Role           | Tool it must advertise | When OnScreen calls it                                     |
|----------------|------------------------|------------------------------------------------------------|
| `notification` | `notify`               | Playback and library events (fire-and-forget)              |
| `metadata`     | `enrich_item`          | Once per item during enrichment, after the built-in agents |
| `task`         | `run_task`             | On the plugin's schedule in **Settings → Tasks**           |

This guide covers the wire contract for each role.

## Prerequisites

Your plugin must:

- Expose an **MCP Streamable HTTP** endpoint. The deprecated HTTP+SSE
  transport is **not** supported.
- Be reachable from the OnScreen server over a **public IP**. Plugins bound
  to loopback, RFC1918, or link-local addresses are rejected at dial time.
- Advertise the tool for its role via the standard `tools/list` handshake.

## Registering a plugin

Admins register plugins through **Settings → Plugins** in the web UI, or via:

```
POST /api/v1/admin/plugins
Content-Type: application/json

{
  "name": "My Slack notifier",
  "role": "notification",
  "endpoint_url": "https://plugin.example.com/mcp",
  "allowed_hosts": ["cdn.example.com"]
}
```

The role can't be changed after creation. The endpoint's own host is always
allowlisted. `allowed_hosts` lists *additional* hosts:

- OnScreen can't police calls your plugin makes on its own side; for those
  the list tells operators what your plugin is expected to reach.
- It *is* enforced for URLs OnScreen fetches on your plugin's behalf: a
  metadata plugin's `poster_url` must be on the endpoint host or one of
  these hosts, or it is dropped.

The **Test** button sends a `notification` plugin a synthetic `media.play`
event. For `metadata` and `task` plugins it only connects, runs the MCP
handshake and checks that the role's tool is advertised, because a real call
would change an item or run the task.

## The `notification` role

### Required tool

Your plugin **must** advertise a tool named `notify`. Missing it is an
operator-visible error but does not trip the breaker (the plugin is healthy,
just mis-configured for this role).

### `notify` arguments

OnScreen calls `tools/call` with `name: "notify"` and arguments shaped
like this:

```json
{
  "correlation_id": "a1b2c3d4-...",
  "event":          "media.play",
  "user_id":        "9f8e7d6c-...",
  "media_id":       "1234abcd-...",
  "title":          "Blade Runner 2049",
  "body":           ""
}
```

| Field            | Type   | Notes                                                          |
|------------------|--------|----------------------------------------------------------------|
| `correlation_id` | string | UUID, unique per dispatch. Log this for cross-system tracing.  |
| `event`          | string | Canonical name. See **Events** below.                          |
| `user_id`        | string | UUID. Empty for server-wide events.                            |
| `media_id`       | string | UUID. Empty for events not about a specific item.              |
| `title`          | string | Short human-readable label (media title, library name, etc.).  |
| `body`           | string | Longer description when appropriate. Often empty.              |

### Events

| Event                    | Fires when                                            |
|--------------------------|-------------------------------------------------------|
| `media.play`             | A user starts playback                                |
| `media.pause`            | Playback pauses                                       |
| `media.resume`           | Playback resumes after pause                          |
| `media.stop`             | Playback stops                                        |
| `media.scrobble`         | Playback passes the watched threshold                 |
| `library.scan.complete`  | A library scan finishes                               |

The set grows over time. Your plugin should **tolerate unknown event names**
rather than rejecting them — filter server-side instead of OnScreen-side.

### Return value

Return an ordinary MCP `CallToolResult`. The content is ignored for
`notification`-role plugins (this is a fire-and-forget channel). Set
`isError: true` to indicate a logical failure; OnScreen records the result
in the audit log but does **not** retry.

## The `metadata` role

### Required tool

Your plugin **must** advertise a tool named `enrich_item`. As with `notify`,
a missing tool is logged but does not trip the breaker.

### When it is called

Enrichment runs when a scan finds new files (or files whose metadata is
still incomplete) and when an admin presses **Refresh metadata**. For each
item, the built-in agents (TMDB, TVDB, AniList, MusicBrainz / TheAudioDB,
NFO files) run first and write their results. Then every enabled metadata
plugin is called **in name order**, one after another, with the item as it
now stands. In practice that means:

- the movie, episode or track whose file the scan found;
- an episode's show, the first time a scan enriches that show;
- a track's album and artist, once per server run;
- any item, shows and seasons included, on **Refresh metadata**.

Photos and other personal media are not sent. Plugins are called even when
no TMDB key is set, so a plugin can be an item's only source.

### `enrich_item` arguments

```json
{
  "correlation_id":    "a1b2c3d4-...",
  "item_id":           "1234abcd-...",
  "library_id":        "9f8e7d6c-...",
  "item_type":         "episode",
  "title":             "Hello, Ms. Cobel",
  "original_title":    "",
  "year":              2025,
  "index":             1,
  "parent_title":      "Season 2",
  "parent_index":      2,
  "grandparent_title": "Severance",
  "provider_ids":      { "tmdb": 95396, "imdb": "tt11280740" },
  "file_name":         "Severance.S02E01.mkv",
  "locked_fields":     ["summary"]
}
```

| Field                             | Type     | Notes                                                                 |
|-----------------------------------|----------|-----------------------------------------------------------------------|
| `correlation_id`                  | string   | UUID, unique per item lookup.                                         |
| `item_id`, `library_id`           | string   | UUIDs.                                                                |
| `item_type`                       | string   | `movie`, `show`, `season`, `episode`, `artist`, `album`, `track`, `book` or `audiobook`. |
| `title`, `original_title`, `year` |          | The item's current values, after the built-in agents. Omitted / empty when unknown. |
| `index`                           | int      | Episode, season or track number. Omitted when unset.                  |
| `parent_title`, `parent_index`    |          | The parent row: an episode's season, a track's album, a season's show. |
| `grandparent_title`               | string   | An episode's show or a track's artist.                                |
| `provider_ids`                    | object   | `tmdb`, `tvdb`, `anilist`, `mal` (ints), `imdb`, `musicbrainz` (strings); only the ones OnScreen has. |
| `file_name`                       | string   | Basename of the item's file, when the call came from one. Never a full path. |
| `locked_fields`                   | string[] | Fields an admin locked. Values you return for them are ignored.       |

### Return value

Return a JSON object, either as `structuredContent` (preferred) or as the
text content of the result. Every field is optional; return `{}` (or
nothing) when you have no match.

```json
{
  "mode":                    "fill",
  "title":                   "Hello, Ms. Cobel",
  "original_title":          "",
  "year":                    2025,
  "summary":                 "Mark returns to Lumon…",
  "tagline":                 "",
  "rating":                  8.6,
  "content_rating":          "TV-MA",
  "genres":                  ["Drama", "Mystery"],
  "originally_available_at": "2025-01-17",
  "poster_url":              "https://cdn.example.com/severance-s2e1.jpg",
  "provider_ids":            { "tvdb": 10353218 }
}
```

Invalid values are dropped one field at a time (and logged); the rest of the
answer still applies.

| Field                     | Accepted values                                                |
|---------------------------|----------------------------------------------------------------|
| `mode`                    | `fill` (default) or `override`. Anything else reads as `fill`. |
| `title`, `original_title` | Trimmed text, at most 500 bytes.                               |
| `summary`                 | At most 20 000 bytes.                                          |
| `tagline`                 | At most 1 000 bytes.                                           |
| `content_rating`          | At most 32 bytes.                                              |
| `year`                    | 1800–2200.                                                     |
| `rating`                  | 0–10.                                                          |
| `genres`                  | At most 32 entries of 100 bytes; duplicates are removed.       |
| `originally_available_at` | `YYYY-MM-DD`.                                                  |
| `poster_url`              | Absolute `http`/`https` URL on the endpoint host or an `allowed_hosts` entry, without credentials, at most 2 048 bytes. |
| `provider_ids`            | `tmdb`, `tvdb`, `anilist`, `mal` as positive integers; `0` means "not supplied". |

Empty strings mean "not supplied", not "clear this field": a plugin can't
blank out a value.

### Precedence and locks

1. The built-in agents write first, and plugins see their result.
2. **Locked fields are never changed.** An admin locks a field by editing
   it (the v2.6 metadata locks); a locked title also keeps its sort title.
   The database enforces the same rule, so no write path can get around it.
3. `"mode": "fill"` sets a field only where the item has no value yet.
   Plugins run in name order, so the **first** plugin to fill a field wins.
4. `"mode": "override"` replaces the current value, including one a
   built-in agent just wrote. The **last** overriding plugin wins. The title
   is never empty, so only an override changes it; a new title also becomes
   the sort title unless the sort title is locked.
5. `provider_ids` and `poster_url` are **fill-only** in both modes. A plugin
   can add a missing ID or poster, but it can't re-point a matched item
   (that is Fix Match's job) or replace a poster (posters aren't lockable,
   so an override would undo an admin's poster pick on every refresh).
6. If a new title or year collides with another item in the same library,
   OnScreen keeps the old title and year and applies the rest.

OnScreen downloads the poster with its artwork fetcher, which has the same
SSRF guard as every other outbound fetch (no loopback, private, link-local
or CGNAT addresses; a capped number of redirects). The file is stored next
to the media as `<item-id>-poster.jpg`.

### Failures and timeouts

A metadata plugin can never fail a scan. A timeout (10 s per call), a
transport error, an `isError` result or an unreadable answer just means no
data from that plugin for that item. Each is logged and counts toward the
plugin's circuit breaker (see **Security model**); while the breaker is
open the plugin is skipped. The list of enabled metadata plugins is cached
for 15 seconds, so adding, editing or disabling one takes effect within
that window.

## The `task` role

### Required tool

Your plugin **must** advertise a tool named `run_task`.

### Scheduling

Registering a task plugin creates a scheduled task named `Plugin: <name>`
(type `plugin_task`, config `{"plugin_id": "<uuid>"}`) that runs **daily at
04:30** server time. In **Settings → Tasks** you can change its schedule,
disable it or run it now, like any other task; its last run time, status
and error show there, and the run history keeps each run's output.

- Deleting the plugin deletes its task.
- While the plugin itself is disabled, runs succeed with the output
  `skipped: plugin "<name>" is disabled`.
- Task plugins registered before v2.7 get their task at the next server
  start.

### `run_task` arguments

```json
{
  "correlation_id": "a1b2c3d4-...",
  "plugin_id":      "5e6f7a8b-...",
  "started_at":     "2026-10-19T04:30:00Z",
  "server":         { "name": "OnScreen", "version": "2.7.0" },
  "libraries": [
    { "id": "0c1d2e3f-...", "name": "Movies", "type": "movie" },
    { "id": "4a5b6c7d-...", "name": "Music",  "type": "music" }
  ]
}
```

Nothing secret is sent: no filesystem paths, API keys, users or URLs.

### Return value

The text content of the result becomes the run's output (trimmed, at most
4 096 bytes). Set `isError: true` to fail the run: its text becomes the
task's last error, and the failure sends the `task_failed` notification
like any other failed task.

### Timeouts

A `run_task` call may take up to **30 seconds**, the plugin HTTP client's
hard ceiling. A plugin with more work than that should accept the run,
return straight away saying what it started, and carry on in the
background. A timed-out or failed run counts toward the circuit breaker.
A run attempted while the breaker is open fails with a "circuit breaker
open" error without reaching the plugin.

## Security model

- **All string fields are untrusted input.** Titles come from filenames and
  scraped metadata. Do not feed them into LLM prompts, shell commands, or
  SQL without sanitisation. OnScreen treats what plugins return the same
  way: it validates every field before storing it.
- OnScreen's DNS-rebinding guard dials the *validated IP literal*, not the
  hostname. If your endpoint has a TTL-0 record pointing at mixed
  public/private IPs, the request is refused.
- HTTP redirects are **not followed**. If your endpoint returns a 302,
  delivery fails.
- Connection timeout is 5 s. The per-call timeout is 10 s for `notify` and
  `enrich_item` and 30 s for `run_task`; the outer client ceiling is 30 s.
  A plugin that can't respond in time is treated as failed.
- Three consecutive failures (transport errors, timeouts, `isError`
  results) open a per-plugin **circuit breaker** for five minutes; a missing
  tool doesn't count. While it is open, notifications are dropped with an
  audit row, metadata lookups skip the plugin, and task runs fail without
  calling it. The first call after the five minutes is a probe: success
  closes the breaker, failure re-opens it. Opening and closing are audited
  (`plugin.notify.breaker_open` / `_close`, `plugin.metadata.*`,
  `plugin.task.*`).

## Reference implementation

A minimal Go stub lives at [testdata/plugins/stub-notify/](../testdata/plugins/stub-notify/).
Clone it, edit the `notify` handler, and you have a working plugin. For the
other roles, rename the tool to `enrich_item` or `run_task` and return the
shapes above.
