# OnScreen Plugin Authoring Guide

OnScreen dispatches events to external **MCP servers** you configure as
plugins. OnScreen is the MCP *client*; your plugin is the MCP *server*.
This guide covers the wire contract for the `notification` role (the only
role implemented in v1).

## Prerequisites

Your plugin must:

- Expose an **MCP Streamable HTTP** endpoint. The deprecated HTTP+SSE
  transport is **not** supported.
- Be reachable from the OnScreen server over a **public IP**. Plugins bound
  to loopback, RFC1918, or link-local addresses are rejected at dial time.
- Advertise the tools listed below via the standard `tools/list` handshake.

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

The endpoint's own host is always allowlisted. `allowed_hosts` is for any
*additional* hosts your plugin may dial (e.g. to fetch artwork) — OnScreen
never actually enforces this for outbound calls *from* your plugin; it's
metadata that tells operators what your plugin is expected to reach.

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

## Security model

- **All string fields are untrusted input.** Titles come from filenames and
  scraped metadata. Do not feed them into LLM prompts, shell commands, or
  SQL without sanitisation.
- OnScreen's DNS-rebinding guard dials the *validated IP literal*, not the
  hostname. If your endpoint has a TTL-0 record pointing at mixed
  public/private IPs, the request is refused.
- HTTP redirects are **not followed**. If your endpoint returns a 302,
  delivery fails.
- Connection timeout is 5 s; per-call timeout is 10 s; the outer client
  ceiling is 30 s. A plugin that can't respond within 10 s is treated as
  failed.
- Three consecutive failures open a per-plugin **circuit breaker** for
  five minutes; dispatches during the open window are dropped with an
  audit row.

## Reference implementation

A minimal Go stub lives at [testdata/plugins/stub-notify/](../testdata/plugins/stub-notify/).
Clone it, edit the `notify` handler, and you have a working plugin.
