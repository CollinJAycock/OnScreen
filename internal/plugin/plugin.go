// Package plugin runs OnScreen's outbound MCP plugin system.
//
// Plugins are external MCP servers that OnScreen calls out to. Each plugin
// registers as exactly one role (notification, metadata, task) and OnScreen
// invokes it only in that role. The wire protocol is MCP over Streamable
// HTTP (the SSE transport, deprecated in MCP spec 2025-03-26, is not used).
//
// This file holds the domain types. The runtime lives in client.go,
// egress.go, and dispatcher.go; the registry/DB layer lives in registry.go.
package plugin

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Role is a plugin's capability slot. A plugin registers as exactly one role
// and OnScreen invokes it only in that role.
type Role string

const (
	RoleNotification Role = "notification"
	RoleMetadata     Role = "metadata"
	RoleTask         Role = "task"
)

// ValidRole reports whether r is one of the recognised role strings.
func ValidRole(r string) bool {
	switch Role(r) {
	case RoleNotification, RoleMetadata, RoleTask:
		return true
	}
	return false
}

// Transport is the wire protocol used to reach the plugin. Only "http"
// (Streamable HTTP) is implemented in v1; the column exists so adding stdio
// later doesn't require a migration.
type Transport string

const TransportHTTP Transport = "http"

// Plugin is the domain view of a row in the plugins table. The DB layer
// stores allowed_hosts as a JSONB array; we expose it as []string here so
// callers don't have to remember the encoding.
type Plugin struct {
	ID           uuid.UUID
	Name         string
	Role         Role
	Transport    Transport
	EndpointURL  string
	AllowedHosts []string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// fromGen converts a sqlc-generated row into the domain type. allowed_hosts
// JSON parse errors degrade to an empty allowlist — the dialer treats that
// as "endpoint host only," which is the safe default.
func fromGen(g gen.Plugin) Plugin {
	var hosts []string
	if len(g.AllowedHosts) > 0 {
		_ = json.Unmarshal(g.AllowedHosts, &hosts)
	}
	return Plugin{
		ID:           g.ID,
		Name:         g.Name,
		Role:         Role(g.Role),
		Transport:    Transport(g.Transport),
		EndpointURL:  g.EndpointUrl,
		AllowedHosts: hosts,
		Enabled:      g.Enabled,
		CreatedAt:    g.CreatedAt.Time,
		UpdatedAt:    g.UpdatedAt.Time,
	}
}

// NotificationEvent is the payload OnScreen passes to a notification plugin's
// `notify` tool. Field names mirror the existing webhook payload to keep the
// two surfaces visually similar for plugin authors.
//
// Plugins should treat all string fields as untrusted external content and
// not feed them back into LLM contexts unsanitised — see the project's
// plugin threat model.
type NotificationEvent struct {
	// CorrelationID lets logs across OnScreen and the plugin be cross-referenced
	// for a single dispatch. Generated per-Dispatch call, not per-plugin.
	CorrelationID string `json:"correlation_id"`
	// Event is the canonical event name (e.g. "media.play", "library.scan.complete").
	Event string `json:"event"`
	// UserID is the user who triggered the event, or empty for server-wide events.
	UserID string `json:"user_id,omitempty"`
	// MediaID, if present, identifies the media item the event is about.
	MediaID string `json:"media_id,omitempty"`
	// Title is a short human-readable label for the event/media.
	Title string `json:"title,omitempty"`
	// Body is a longer human-readable description, when one is appropriate.
	Body string `json:"body,omitempty"`
}

// NotifyToolName is the tool a notification plugin must advertise for
// OnScreen to dispatch to it. Plugins missing this tool are skipped.
const NotifyToolName = "notify"
