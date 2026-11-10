package notification

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/i18n"
)

// DB defines the database operations the notification service needs.
type DB interface {
	CreateNotification(ctx context.Context, arg gen.CreateNotificationParams) (gen.Notification, error)
	ListAllUserIDs(ctx context.Context) ([]uuid.UUID, error)
	ListNotifiableUserIDsForLibrary(ctx context.Context, libraryID uuid.UUID) ([]uuid.UUID, error)
	ListAdminUserIDs(ctx context.Context) ([]uuid.UUID, error)
	// GetUser reads a recipient's ui_language, the language their copy of a
	// notice is written in.
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
}

// Service creates notifications and publishes them via SSE.
type Service struct {
	db     DB
	broker *Broker
	logger *slog.Logger
	// agents, when attached (SetAgentDispatcher), receives every event once
	// for the outbound notification agents (Discord, ntfy, …). Atomic:
	// attached at startup while scans may already be notifying.
	agents atomic.Pointer[agentHolder]
}

// NewService constructs a notification Service.
func NewService(db DB, broker *Broker, logger *slog.Logger) *Service {
	return &Service{db: db, broker: broker, logger: logger}
}

// Notify creates a notification for a single user and publishes it via SSE.
// typ must be one of the Type* constants (types.go); text is written in the
// user's language (see Text).
func (s *Service) Notify(ctx context.Context, userID uuid.UUID, typ string, text Text, itemID *uuid.UUID) {
	s.forwardToAgents(ctx, AgentScopeUser, &userID, typ, text, itemID)
	s.notifyOne(ctx, userID, typ, text, itemID)
}

// langOf is the language userID reads notices in: their ui_language, else
// English. A failed lookup is English too: the notice still goes out.
func (s *Service) langOf(ctx context.Context, userID uuid.UUID) i18n.Lang {
	u, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return i18n.English
	}
	return i18n.ForUser(u.UiLanguage, i18n.English)
}

// notifyOne persists and publishes one user's copy of a notice, written in
// their language. The fan-out helpers call it per recipient so the outbound
// agents hear each event once.
func (s *Service) notifyOne(ctx context.Context, userID uuid.UUID, typ string, text Text, itemID *uuid.UUID) {
	title, body := text(s.langOf(ctx, userID))
	if !IsKnownType(typ) {
		// Still attempted — the DB only checks the format — but an undeclared
		// type means the web client has no icon/link for it.
		s.logger.WarnContext(ctx, "notification type not declared in notification/types.go", "type", typ)
	}
	itemPG := pgtype.UUID{}
	if itemID != nil {
		itemPG = pgtype.UUID{Bytes: [16]byte(*itemID), Valid: true}
	}
	n, err := s.db.CreateNotification(ctx, gen.CreateNotificationParams{
		UserID: userID,
		Type:   typ,
		Title:  title,
		Body:   body,
		ItemID: itemPG,
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "create notification", "type", typ, "err", err)
		return
	}
	var itemStr *string
	if n.ItemID.Valid {
		id := uuid.UUID(n.ItemID.Bytes).String()
		itemStr = &id
	}
	s.broker.Publish(userID, Event{
		ID:        n.ID.String(),
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		ItemID:    itemStr,
		Read:      n.Read,
		CreatedAt: n.CreatedAt.Time.UnixMilli(),
	})
}

// NotifyAllUsers creates a notification for every non-managed user.
func (s *Service) NotifyAllUsers(ctx context.Context, typ string, text Text, itemID *uuid.UUID) {
	s.forwardToAgents(ctx, AgentScopeAll, nil, typ, text, itemID)
	ids, err := s.db.ListAllUserIDs(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "list user ids for broadcast", "err", err)
		return
	}
	for _, uid := range ids {
		s.notifyOne(ctx, uid, typ, text, itemID)
	}
}

// NotifyAdmins creates a notification for every admin account (top-level users
// with is_admin; managed profiles can never be admins). Used for alerts that
// need an admin's action, such as a media request waiting for approval.
func (s *Service) NotifyAdmins(ctx context.Context, typ string, text Text, itemID *uuid.UUID) {
	s.forwardToAgents(ctx, AgentScopeAdmins, nil, typ, text, itemID)
	ids, err := s.db.ListAdminUserIDs(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "list admin ids for notification", "type", typ, "err", err)
		return
	}
	for _, uid := range ids {
		s.notifyOne(ctx, uid, typ, text, itemID)
	}
}

// NotifyScanComplete sends a "scan_complete" notification to the users who can
// see the library (admins, everyone for a public library, grantees for a
// private one). Broadcasting it to every account leaked the name and activity
// of private libraries to users who have no access to them.
func (s *Service) NotifyScanComplete(ctx context.Context, libraryID uuid.UUID, libraryName string, newItems int) {
	if newItems == 0 {
		return
	}
	// {count} as plain digits, as the English notice has always written it
	// ("1234 new items", not "1,234").
	count := itoa(newItems)
	text := func(l i18n.Lang) (string, string) {
		return i18n.T(l, "notify.scanComplete.title", nil),
			i18n.TN(l, "notify.scanComplete.body", newItems, i18n.Params{"library": libraryName, "count": count})
	}
	ids, err := s.db.ListNotifiableUserIDsForLibrary(ctx, libraryID)
	if err != nil {
		s.logger.ErrorContext(ctx, "list library recipients for scan notification", "library_id", libraryID, "err", err)
		return
	}
	// Not forwarded to the outbound agents: they announce new titles in their
	// own batched new_content message (notifyagents).
	for _, uid := range ids {
		s.notifyOne(ctx, uid, TypeScanComplete, text, nil)
	}
}

// NotifyNewContent sends a "new_content" notification to all users.
func (s *Service) NotifyNewContent(ctx context.Context, title string, itemID uuid.UUID) {
	s.NotifyAllUsers(ctx, TypeNewContent, Keys("notify.newContent.title", i18n.Params{"title": title}, "", nil), &itemID)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 10)
	for n > 0 {
		b = append(b, byte('0'+n%10))
		n /= 10
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
