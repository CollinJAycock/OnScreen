# stub-notify

A minimal reference notification plugin for OnScreen. Speaks MCP over
Streamable HTTP, exposes a single `notify` tool that logs received events
to stderr.

## Run

```
go run ./testdata/plugins/stub-notify
```

Listens on `:8091`; the MCP endpoint is at `/mcp`.

## Register in OnScreen

Settings → Plugins → Add Plugin

- **Role:** notification
- **Endpoint URL:** `http://<lan-ip>:8091/mcp`

The OnScreen egress guard blocks loopback, so use a LAN IP, a Docker-host
IP (`host.docker.internal` won't work — it resolves to loopback from inside
containers), or a tunnel like ngrok.

## What it does

Logs every `notify` call to stderr. Perfect for verifying the pipeline
end-to-end before you wire up a real integration.

## Using it as a template

1. Copy this directory.
2. Replace the body of the `notify` handler with your real logic.
3. Keep the tool signature identical — OnScreen's capability cache depends
   on the tool being named exactly `notify`.
