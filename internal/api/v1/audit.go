package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// auditQuerier is the DB subset needed by AuditHandler.
type auditQuerier interface {
	ListAuditLog(ctx context.Context, arg gen.ListAuditLogParams) ([]gen.ListAuditLogRow, error)
}

// AuditHandler handles GET /api/v1/audit.
type AuditHandler struct {
	db     auditQuerier
	logger *slog.Logger
}

// NewAuditHandler creates an AuditHandler.
func NewAuditHandler(db auditQuerier, logger *slog.Logger) *AuditHandler {
	return &AuditHandler{db: db, logger: logger}
}

type auditEntry struct {
	ID        uuid.UUID       `json:"id"`
	UserID    *uuid.UUID      `json:"user_id"`
	Action    string          `json:"action"`
	Target    *string         `json:"target"`
	Detail    json.RawMessage `json:"detail"`
	IPAddr    *string         `json:"ip_addr"`
	CreatedAt string          `json:"created_at"`
}

func pgUUIDToPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

func tsToTimeAudit(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

// List handles GET /api/v1/audit — returns paginated audit log entries.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int32(50)
	offset := int32(0)

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	rows, err := h.db.ListAuditLog(r.Context(), gen.ListAuditLogParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "audit: list", "err", err)
		respond.InternalError(w, r)
		return
	}

	entries := make([]auditEntry, len(rows))
	for i, row := range rows {
		var ip *string
		if row.IpAddr != "" {
			ip = &row.IpAddr
		}
		entries[i] = auditEntry{
			ID:        row.ID,
			UserID:    pgUUIDToPtr(row.UserID),
			Action:    row.Action,
			Target:    row.Target,
			Detail:    row.Detail,
			IPAddr:    ip,
			CreatedAt: tsToTimeAudit(row.CreatedAt).Format(time.RFC3339),
		}
	}

	respond.Success(w, r, entries)
}
