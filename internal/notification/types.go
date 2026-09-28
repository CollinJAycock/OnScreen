package notification

import "regexp"

// Notification types stored in notifications.type and sent as Event.Type.
// Every persisted notification must use one of these — never a bare string
// literal (TestNotifyCallSitesUseKnownTypes enforces it across the repo).
//
// The DB only checks the format (TypePattern, migration 00021): the squashed
// init once pinned the column to three values, and every request_* notice the
// request workflow sent failed that CHECK and was dropped with nothing but an
// error log. Adding a type is now: add a constant here, add it to knownTypes,
// and teach the web NotificationPanel an icon/link for it.
const (
	// TypeNewContent: a new title was added (item_id = the item).
	TypeNewContent = "new_content"
	// TypeScanComplete: a library scan finished with new items.
	TypeScanComplete = "scan_complete"
	// TypeSystem: generic server message.
	TypeSystem = "system"

	// TypeRequestCreated tells a requester their request is queued for an admin.
	TypeRequestCreated = "request_created"
	// TypeRequestPending tells every admin a request is waiting for approval.
	TypeRequestPending = "request_pending"
	// TypeRequestApproved tells a requester their request was approved (by an
	// admin or automatically) and handed to Radarr/Sonarr.
	TypeRequestApproved = "request_approved"
	// TypeRequestDeclined tells a requester their request was declined.
	TypeRequestDeclined = "request_declined"
	// TypeRequestAvailable tells a requester the title is in the library
	// (item_id = the fulfilled item).
	TypeRequestAvailable = "request_available"
	// TypeRequestFailed tells a requester the download failed upstream.
	TypeRequestFailed = "request_failed"
	// TypeRequestSeasonAvailable tells a requester one season of a
	// multi-season TV request is complete in the library while others are
	// still coming (item_id = the show). Sent once per season; the request's
	// last season sends TypeRequestAvailable instead.
	TypeRequestSeasonAvailable = "request_season_available"
)

// Media-issue ("Report a problem") notices.
const (
	// TypeIssueReported tells every admin a user reported a problem with an
	// item (item_id = the reported item).
	TypeIssueReported = "issue_reported"
	// TypeIssueResolved tells a reporter an admin resolved or dismissed their
	// report (item_id = the reported item).
	TypeIssueResolved = "issue_resolved"
)

// TypePattern is the format the notifications_type_check constraint enforces
// (migration 00021). Kept in lockstep with the SQL so a constant that the DB
// would refuse fails a unit test instead of silently failing every insert.
var TypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var knownTypes = map[string]struct{}{
	TypeNewContent:             {},
	TypeScanComplete:           {},
	TypeSystem:                 {},
	TypeRequestCreated:         {},
	TypeRequestPending:         {},
	TypeRequestApproved:        {},
	TypeRequestDeclined:        {},
	TypeRequestAvailable:       {},
	TypeRequestFailed:          {},
	TypeRequestSeasonAvailable: {},
	TypeIssueReported:          {},
	TypeIssueResolved:          {},
}

// KnownTypes returns every persisted notification type.
func KnownTypes() []string {
	out := make([]string, 0, len(knownTypes))
	for t := range knownTypes {
		out = append(out, t)
	}
	return out
}

// IsKnownType reports whether t is one of the declared notification types.
func IsKnownType(t string) bool {
	_, ok := knownTypes[t]
	return ok
}
