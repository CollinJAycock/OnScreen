package main

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/audit"
)

// viewAsAuditAdapter satisfies middleware.ViewAsAuditor by writing a
// single audit row per successful impersonation. The middleware can't
// import internal/audit directly (auth-stack isolation), so this thin
// wrapper bridges the two.
//
// Detail map captures the hit path so an investigator can reconstruct
// what surface the admin browsed (`/items/{id}`, `/libraries/{id}`,
// `/users/me/history`, etc.) without retroactively reading every
// request log line keyed to the synthetic target user.
type viewAsAuditAdapter struct {
	audit *audit.Logger
}

func (a *viewAsAuditAdapter) LogImpersonate(r *http.Request, adminID *uuid.UUID, targetID uuid.UUID) {
	if a == nil || a.audit == nil {
		return
	}
	a.audit.Log(
		r.Context(),
		adminID,
		audit.ActionImpersonateBegin,
		targetID.String(),
		map[string]any{"path": r.URL.Path, "method": r.Method},
		audit.ClientIP(r),
	)
}
