// Package audit provides a lightweight audit logger that writes security-
// relevant events to the audit_log table. Logging is fire-and-forget: failures
// are logged but never propagated to callers.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
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
	ActionPasswordReset  = "user.password_reset"
	ActionLibraryCreate  = "library.create"
	ActionLibraryDelete  = "library.delete"
	ActionLibraryScan    = "library.scan"
	ActionSettingsUpdate = "settings.update"
	ActionInviteCreate   = "invite.create"
	ActionLoginSuccess   = "auth.login_success"
	ActionLoginFailed    = "auth.login_failed"
	ActionItemEnrich     = "item.enrich"
	ActionItemMatchApply = "item.match_apply"
	ActionTranscodeStart = "transcode.start"
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

// ClientIP extracts the client IP address from an HTTP request.
// It prefers X-Forwarded-For (already validated by chi's RealIP middleware)
// and falls back to r.RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
