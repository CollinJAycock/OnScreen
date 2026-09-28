package notifyagents

import (
	"slices"

	"github.com/onscreen/onscreen/internal/notification"
)

// Event keys an agent can subscribe to (notification_agents.events).
const (
	EventRequestPending   = "request_pending"
	EventRequestApproved  = "request_approved"
	EventRequestAvailable = "request_available"
	// EventRequestSeasonAvailable: some (not all) requested seasons of a show
	// landed. Emitted by the season-request workflow under the notification
	// type of the same name.
	EventRequestSeasonAvailable = "request_season_available"
	EventRequestFailed          = "request_failed"
	EventIssueReported          = "issue_reported"
	EventNewContent             = "new_content"
	EventTaskFailed             = "task_failed"
	EventBackupFailed           = "backup_failed"
	EventWorkerFleetDown        = "worker_fleet_down"
	// EventTest is the admin "Send test" message. Not subscribable.
	EventTest = "test"
)

// EventInfo describes a subscribable event for the settings UI.
type EventInfo struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Group buckets the checkboxes: requests | library | server.
	Group string `json:"group"`
	// DefaultFor lists the kinds a new agent subscribes to this event by
	// default.
	DefaultFor []Kind `json:"default_for"`
}

var (
	chatKinds = []Kind{KindDiscord, KindTelegram, KindNtfy, KindGotify}
	allKinds  = []Kind{KindDiscord, KindTelegram, KindNtfy, KindGotify, KindEmail}
)

// catalog is every subscribable event, in UI order. Defaults: request, issue
// and failure events on for chat kinds; email (an admin mailbox) only gets
// what needs an admin; new_content is always opt-in — it's the chattiest.
var catalog = []EventInfo{
	{EventRequestPending, "New request", "A request is waiting for approval.", "requests", allKinds},
	{EventRequestApproved, "Request approved", "A request was approved (by an admin or automatically) and sent to Radarr/Sonarr.", "requests", chatKinds},
	{EventRequestAvailable, "Request available", "A requested title is ready to watch, with who asked for it.", "requests", chatKinds},
	{EventRequestSeasonAvailable, "Requested seasons available", "Some of a show's requested seasons are ready to watch.", "requests", chatKinds},
	{EventRequestFailed, "Request failed", "Radarr/Sonarr couldn't download a request.", "requests", allKinds},
	{EventIssueReported, "Problem reported", "A user reported a problem with a title.", "library", allKinds},
	{EventNewContent, "New content", "New titles were added — one message per batch, never one per file. Public libraries only.", "library", nil},
	{EventTaskFailed, "Scheduled task failed", "A scheduled task run failed (repeats suppressed until it succeeds again).", "server", allKinds},
	{EventBackupFailed, "Backup failed", "A scheduled database backup failed.", "server", allKinds},
	{EventWorkerFleetDown, "No transcode worker", "No transcode worker is available (and when one is back).", "server", allKinds},
}

// Events returns the subscribable events for the settings UI.
func Events() []EventInfo {
	out := make([]EventInfo, len(catalog))
	for i, e := range catalog {
		e.DefaultFor = slices.Clone(e.DefaultFor)
		if e.DefaultFor == nil {
			e.DefaultFor = []Kind{}
		}
		out[i] = e
	}
	return out
}

// IsEvent reports whether key is a subscribable event.
func IsEvent(key string) bool {
	return slices.ContainsFunc(catalog, func(e EventInfo) bool { return e.Key == key })
}

// DefaultEvents returns the events a new agent of kind k subscribes to when
// the create request names none.
func DefaultEvents(k Kind) []string {
	out := []string{}
	for _, e := range catalog {
		if slices.Contains(e.DefaultFor, k) {
			out = append(out, e.Key)
		}
	}
	return out
}

// forwardedScope maps the in-app notification types the agents announce to
// the ONE fan-out scope whose copy they take. A download failure sends the
// requester a user notice and every admin an admin notice (with the *arr
// reason); the channel takes the admin copy — it exists for every request,
// an admin's own included — minus the reason (privacy.go). A per-user copy of an admin
// notice (Notify inside NotifyAdmins) never reaches the agents — the
// notification service forwards each call once, not each recipient.
var forwardedScope = map[string]notification.AgentScope{
	notification.TypeRequestPending:   notification.AgentScopeAdmins,
	notification.TypeRequestApproved:  notification.AgentScopeUser,
	notification.TypeRequestAvailable: notification.AgentScopeUser,
	EventRequestSeasonAvailable:       notification.AgentScopeUser,
	notification.TypeRequestFailed:    notification.AgentScopeAdmins,
	notification.TypeIssueReported:    notification.AgentScopeAdmins,
	// new_content is deliberately absent: an agent hears about new titles
	// only through the batched LibraryHasNewContent path, never per item.
}

// eventFor maps an in-app notification to the agent event it announces.
func eventFor(ev notification.AgentEvent) (string, bool) {
	scope, ok := forwardedScope[ev.Type]
	if !ok || scope != ev.Scope {
		return "", false
	}
	// Notification types and agent event keys share their names.
	return ev.Type, true
}

// Severity drives the message color / priority.
type Severity string

// Severities.
const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// eventSeverity is each event's default severity.
func eventSeverity(key string) Severity {
	switch key {
	case EventRequestAvailable, EventRequestSeasonAvailable:
		return SeveritySuccess
	case EventIssueReported:
		return SeverityWarning
	case EventRequestFailed, EventTaskFailed, EventBackupFailed, EventWorkerFleetDown:
		return SeverityError
	}
	return SeverityInfo
}

// eventPath is the web route an event's "Open in OnScreen" link points at
// when it isn't an item page (mirrors web notificationHref).
func eventPath(key string) string {
	switch key {
	case EventRequestPending, EventRequestApproved, EventRequestAvailable,
		EventRequestSeasonAvailable, EventRequestFailed:
		return "/requests"
	case EventIssueReported:
		return "/settings/library-health"
	case EventTaskFailed, EventBackupFailed:
		return "/settings/tasks"
	case EventWorkerFleetDown:
		return "/settings/transcode"
	}
	return "/"
}
