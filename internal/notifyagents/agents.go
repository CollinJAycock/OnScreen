// Package notifyagents delivers server-wide events — media requests, problem
// reports, new content and ops failures — to admin-configured outbound
// channels: a Discord webhook, a Telegram bot, an ntfy topic, a Gotify server
// or an email list (Settings → Notifications). The use case is the household
// group chat: one message per event, not one per account.
//
// Events arrive three ways:
//   - internal/notification forwards every Notify / NotifyAdmins /
//     NotifyAllUsers call once (DispatchAgentEvent), and this package keeps
//     the ones an agent can subscribe to;
//   - the library scanner reports libraries with new items
//     (LibraryHasNewContent), batched into one message per window;
//   - ops sources call in directly: the scheduler's result hook (TaskResult,
//     task_failed / backup_failed) and the worker-fleet monitor
//     (FleetMonitor, worker_fleet_down).
//
// Delivery is asynchronous: a bounded event queue feeds one sequential
// worker per agent with a per-kind rate limit and three attempts with
// backoff. Secrets (the Discord webhook URL, the Telegram bot token, the
// ntfy / Gotify tokens) are encrypted at rest bound to the agent's row and
// are never returned by the API or written to logs or last_error.
package notifyagents

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Kind is an agent's channel type (notification_agents.kind).
type Kind string

// Agent kinds.
const (
	KindDiscord  Kind = "discord"
	KindTelegram Kind = "telegram"
	KindNtfy     Kind = "ntfy"
	KindGotify   Kind = "gotify"
	KindEmail    Kind = "email"
)

// Kinds lists every agent kind, in UI order.
var Kinds = []Kind{KindDiscord, KindTelegram, KindNtfy, KindGotify, KindEmail}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// selfHostable reports whether the kind may point at a LAN server
// (allow_private_network). Discord and Telegram are fixed public services.
func (k Kind) selfHostable() bool { return k == KindNtfy || k == KindGotify }

// Config holds an agent's NON-secret per-kind fields (the config jsonb
// column). Only the fields of the agent's kind are set.
type Config struct {
	// ChatID is the Telegram chat: a numeric id (groups are negative) or a
	// public @channelname.
	ChatID string `json:"chat_id,omitempty"`
	// ServerURL is the ntfy or Gotify server, e.g. https://ntfy.sh.
	ServerURL string `json:"server_url,omitempty"`
	// Topic is the ntfy topic.
	Topic string `json:"topic,omitempty"`
	// Recipients are the email addresses (bare addresses, at most 10).
	Recipients []string `json:"recipients,omitempty"`
}

// Agent is the API view of an agent. It never carries the secret —
// SecretConfigured says whether one is stored.
type Agent struct {
	ID                  uuid.UUID  `json:"id"`
	Kind                Kind       `json:"kind"`
	Name                string     `json:"name"`
	Enabled             bool       `json:"enabled"`
	Events              []string   `json:"events"`
	Config              Config     `json:"config"`
	SecretConfigured    bool       `json:"secret_configured"`
	AllowPrivateNetwork bool       `json:"allow_private_network"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastError           *string    `json:"last_error"`
	LastErrorAt         *time.Time `json:"last_error_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateInput is the body of POST /admin/notification-agents.
type CreateInput struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
	// Enabled defaults to true.
	Enabled *bool `json:"enabled"`
	// Events defaults to the kind's defaults (EventInfo.DefaultFor) when nil.
	Events []string `json:"events"`
	Config Config   `json:"config"`
	// Secret is the kind's credential: the Discord webhook URL, the Telegram
	// bot token, the ntfy access token (optional) or the Gotify app token.
	// Email has none.
	Secret              string `json:"secret"`
	AllowPrivateNetwork bool   `json:"allow_private_network"`
}

// UpdateInput is the body of PATCH /admin/notification-agents/{id}. nil
// fields are unchanged.
type UpdateInput struct {
	// Kind may be sent (the edit form round-trips it) but can't change.
	Kind    *Kind    `json:"kind"`
	Name    *string  `json:"name"`
	Enabled *bool    `json:"enabled"`
	Events  []string `json:"events"`
	// Config, when present, replaces the whole config.
	Config *Config `json:"config"`
	// Secret: nil or "" keeps the stored secret ("configured — leave blank to
	// keep"); a value replaces it.
	Secret *string `json:"secret"`
	// ClearSecret removes an optional stored secret (the ntfy access token).
	ClearSecret         bool  `json:"clear_secret"`
	AllowPrivateNetwork *bool `json:"allow_private_network"`
}

// ErrNotFound is returned when no agent has the requested id.
var ErrNotFound = errors.New("notification agent not found")

// ValidationError is a rejected create/update (HTTP 422). Msg is safe to show
// the admin; it never contains the secret.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(msg string) error { return &ValidationError{Msg: msg} }
