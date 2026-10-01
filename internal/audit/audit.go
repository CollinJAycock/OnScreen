// Package audit provides a lightweight audit logger that writes security-
// relevant events to the audit_log table. Logging is fire-and-forget: failures
// are logged but never propagated to callers.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Predefined audit actions.
const (
	ActionUserCreate     = "user.create"
	ActionUserDelete     = "user.delete"
	ActionUserRoleChange = "user.role_change"
	// ActionUserRatingChange records a change to a user's or managed
	// profile's parental content-rating ceiling. Distinct from
	// role_change so an operator can audit parental-control edits —
	// loosening a child's ceiling is exactly the action worth reviewing.
	ActionUserRatingChange = "user.rating_change"
	ActionPasswordReset    = "user.password_reset"
	ActionLibraryCreate    = "library.create"
	ActionLibraryDelete    = "library.delete"
	ActionLibraryScan      = "library.scan"
	// ActionLibraryTrickplay: admin queued seek-bar thumbnail generation
	// for a whole library ("Generate now").
	ActionLibraryTrickplay = "library.trickplay_generate"
	ActionSettingsUpdate   = "settings.update"
	ActionInviteCreate     = "invite.create"
	ActionLoginSuccess     = "auth.login_success"
	ActionLoginFailed      = "auth.login_failed"
	ActionItemEnrich       = "item.enrich"
	ActionItemMatchApply   = "item.match_apply"
	// ActionItemDelete is the soft-delete-subtree admin action on
	// /items/{id}. Earlier code labelled this as item.match_apply, which
	// is the wrong action — match_apply is the metadata re-match action
	// and an admin reviewing the audit log can't distinguish a delete
	// from a re-match. Distinct action so retention/alerting on
	// destructive ops actually works.
	ActionItemDelete = "item.delete"

	// Webhook CRUD audit actions. Webhooks carry an encrypted secret
	// and an outbound URL — both privileged config — so create/update/
	// delete are recorded the same way library and arr-service CRUD are.
	ActionWebhookCreate  = "webhook.create"
	ActionWebhookUpdate  = "webhook.update"
	ActionWebhookDelete  = "webhook.delete"
	ActionTranscodeStart = "transcode.start"
	ActionTranscodeStop  = "transcode.stop"
	ActionBackupDownload = "backup.download"
	ActionBackupRestore  = "backup.restore"

	// arr-service admin CRUD — captures who configured an outbound arr
	// instance, since the api_key in the row is a privileged credential.
	ActionArrServiceCreate     = "arr_service.create"
	ActionArrServiceUpdate     = "arr_service.update"
	ActionArrServiceDelete     = "arr_service.delete"
	ActionArrServiceSetDefault = "arr_service.set_default"

	// Media-request admin actions — record who approved/declined what.
	ActionRequestApprove = "request.approve"
	ActionRequestDecline = "request.decline"
	ActionRequestDelete  = "request.delete"
	// A change to a user's media-request auto-approval toggles — granting one
	// lets that user's requests reach Radarr/Sonarr with no admin review.
	ActionUserRequestPermsChange = "user.request_permissions_change"

	// Media-issue ("Report a problem") admin actions. issue.update records a
	// report being resolved or dismissed; item.regrab records an admin asking
	// Radarr/Sonarr to search again (optionally blocklisting the last grab).
	ActionIssueUpdate = "issue.update"
	ActionItemRegrab  = "item.regrab"

	// Outbound notification agents (Discord / Telegram / ntfy / Gotify /
	// email). Each carries an encrypted credential and an outbound
	// destination, so CRUD and test sends are recorded like webhooks.
	ActionNotificationAgentCreate = "notification_agent.create"
	ActionNotificationAgentUpdate = "notification_agent.update"
	ActionNotificationAgentDelete = "notification_agent.delete"
	ActionNotificationAgentTest   = "notification_agent.test"

	// Admin impersonation. ViewAs lets an admin browse the home/library/etc.
	// surfaces as another user; without an audit trail the request log
	// attributes the GETs to the *target* user (since the synthetic claims
	// carry their identity), giving an admin a privacy-violating read
	// without any forensic record. Logged once per (admin, target, request)
	// tuple by the ViewAs middleware.
	ActionImpersonateBegin = "admin.impersonate"

	// ActionSessionsRevoked records the server itself wiping a user's whole
	// session family — every refresh session deleted and the session epoch
	// bumped, i.e. signed out on every device — with detail.reason saying why
	// (refresh_token_reuse, idp_admin_sync) and detail.sessions_deleted /
	// detail.epoch_bumped whether each half actually landed (false = a failed
	// revocation, also logged by the server). Both wipes used to leave only a
	// server-log line, so an operator asked "why was I logged out everywhere?"
	// found nothing on the audit page to answer with. user_id and target are
	// the affected user, not an actor: no admin did this.
	ActionSessionsRevoked = "auth.sessions_revoked"
)

// AuditDB is the minimal database interface for writing audit log entries.
type AuditDB interface {
	InsertAuditLog(ctx context.Context, arg gen.InsertAuditLogParams) error
}

// Logger writes audit events to the database.
type Logger struct {
	q  AuditDB
	lg *slog.Logger
}

// New creates a new audit Logger.
func New(q AuditDB, lg *slog.Logger) *Logger {
	return &Logger{q: q, lg: lg}
}

// Log records an audit event. It serialises detail to JSON, then fires a
// goroutine so the caller is never blocked by the DB write. Errors are logged
// but never returned.
func (l *Logger) Log(ctx context.Context, userID *uuid.UUID, action string, target string, detail map[string]any, ipAddr string) {
	var detailBytes []byte
	if detail != nil {
		var err error
		detailBytes, err = json.Marshal(detail)
		if err != nil {
			l.lg.Warn("audit: marshal detail", "err", err, "action", action)
			detailBytes = nil
		}
	}

	var targetPtr *string
	if target != "" {
		targetPtr = &target
	}

	var ipPtr *netip.Addr
	if ipAddr != "" {
		if a, err := netip.ParseAddr(ipAddr); err == nil {
			ipPtr = &a
		}
	}

	var pgUID pgtype.UUID
	if userID != nil {
		pgUID = pgtype.UUID{Bytes: *userID, Valid: true}
	}

	params := gen.InsertAuditLogParams{
		UserID: pgUID,
		Action: action,
		Target: targetPtr,
		Detail: detailBytes,
		IpAddr: ipPtr,
	}

	// Fire-and-forget — use a background context so the write completes even
	// if the HTTP request context is cancelled, but cap at 5 s so we don't
	// block shutdown indefinitely.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := l.q.InsertAuditLog(bgCtx, params); err != nil {
			l.lg.Error("audit: insert failed", "err", err, "action", action)
		}
	}()
}

// ClientIP extracts the client IP address for the audit log.
//
// It returns r.RemoteAddr with any port stripped, and does NOT read
// X-Forwarded-For itself. Two bugs are fixed here:
//
//  1. It used to return the raw X-Forwarded-For header whenever present, with
//     no trusted-peer gate — so any client could forge its own audit-log IP,
//     including on failed-login records. Its comment claimed the header was
//     "already validated by chi's RealIP middleware", but this codebase
//     deliberately removed chi's RealIP (which honours the header
//     unconditionally) in favour of middleware.TrustedRealIP. That middleware
//     has already rewritten RemoteAddr from X-Forwarded-For when — and only
//     when — the peer is a trusted proxy, so reading the header again here
//     both duplicated and undermined the check.
//
//  2. It returned "host:port", which netip.ParseAddr rejects, so Logger.Log
//     silently stored NULL for ip_addr on every non-proxied deployment. The
//     audit log's IP column was effectively always empty.
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
