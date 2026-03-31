package notification

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// DB defines the database operations the notification service needs.
type DB interface {
	CreateNotification(ctx context.Context, arg gen.CreateNotificationParams) (gen.Notification, error)
	ListAllUserIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Service creates notifications and publishes them via SSE.
type Service struct {
	db     DB
	broker *Broker
	logger *slog.Logger
}

// NewService constructs a notification Service.
func NewService(db DB, broker *Broker, logger *slog.Logger) *Service {
	return &Service{db: db, broker: broker, logger: logger}
}

// Notify creates a notification for a single user and publishes it via SSE.
func (s *Service) Notify(ctx context.Context, userID uuid.UUID, typ, title, body string, itemID *uuid.UUID) {
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
		s.logger.ErrorContext(ctx, "create notification", "err", err)
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
func (s *Service) NotifyAllUsers(ctx context.Context, typ, title, body string, itemID *uuid.UUID) {
	ids, err := s.db.ListAllUserIDs(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "list user ids for broadcast", "err", err)
		return
	}
	for _, uid := range ids {
		s.Notify(ctx, uid, typ, title, body, itemID)
	}
}

// NotifyScanComplete sends a "scan_complete" notification to all users.
func (s *Service) NotifyScanComplete(ctx context.Context, libraryName string, newItems int) {
	if newItems == 0 {
		return
	}
	title := "Library scan complete"
	body := libraryName + ": " + itoa(newItems) + " new item"
	if newItems != 1 {
		body += "s"
	}
	body += " added"
	s.NotifyAllUsers(ctx, "scan_complete", title, body, nil)
}

// NotifyNewContent sends a "new_content" notification to all users.
func (s *Service) NotifyNewContent(ctx context.Context, title string, itemID uuid.UUID) {
	s.NotifyAllUsers(ctx, "new_content", "New: "+title, "", &itemID)
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
