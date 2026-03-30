package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/webhook"
)

type webhookQuerier interface {
	ListWebhookEndpoints(ctx context.Context) ([]gen.WebhookEndpoint, error)
	GetWebhookEndpoint(ctx context.Context, id uuid.UUID) (gen.WebhookEndpoint, error)
	CreateWebhookEndpoint(ctx context.Context, arg gen.CreateWebhookEndpointParams) (gen.WebhookEndpoint, error)
	UpdateWebhookEndpoint(ctx context.Context, arg gen.UpdateWebhookEndpointParams) (gen.WebhookEndpoint, error)
	DeleteWebhookEndpoint(ctx context.Context, id uuid.UUID) error
}

type webhookService struct {
	db     webhookQuerier
	enc    *auth.Encryptor
	client *http.Client
	logger *slog.Logger
}

func newWebhookService(db webhookQuerier, enc *auth.Encryptor, logger *slog.Logger) *webhookService {
	return &webhookService{
		db:     db,
		enc:    enc,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
	}
}

func (s *webhookService) List(ctx context.Context) ([]v1.WebhookEndpoint, error) {
	eps, err := s.db.ListWebhookEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	out := make([]v1.WebhookEndpoint, len(eps))
	for i, ep := range eps {
		out[i] = genWebhookToV1(ep)
	}
	return out, nil
}

func (s *webhookService) Get(ctx context.Context, id uuid.UUID) (*v1.WebhookEndpoint, error) {
	ep, err := s.db.GetWebhookEndpoint(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, v1.ErrWebhookNotFound
		}
		return nil, fmt.Errorf("get webhook: %w", err)
	}
	v := genWebhookToV1(ep)
	return &v, nil
}

func (s *webhookService) Create(ctx context.Context, url, secret string, events []string) (*v1.WebhookEndpoint, error) {
	secretPtr, err := encryptSecret(s.enc, secret)
	if err != nil {
		return nil, err
	}
	ep, err := s.db.CreateWebhookEndpoint(ctx, gen.CreateWebhookEndpointParams{
		Url:    url,
		Secret: secretPtr,
		Events: events,
	})
	if err != nil {
		return nil, fmt.Errorf("create webhook: %w", err)
	}
	v := genWebhookToV1(ep)
	return &v, nil
}

func (s *webhookService) Update(ctx context.Context, id uuid.UUID, url, secret string, events []string, enabled bool) (*v1.WebhookEndpoint, error) {
	secretPtr, err := encryptSecret(s.enc, secret)
	if err != nil {
		return nil, err
	}
	ep, err := s.db.UpdateWebhookEndpoint(ctx, gen.UpdateWebhookEndpointParams{
		ID:      id,
		Url:     url,
		Secret:  secretPtr,
		Events:  events,
		Enabled: enabled,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, v1.ErrWebhookNotFound
		}
		return nil, fmt.Errorf("update webhook: %w", err)
	}
	v := genWebhookToV1(ep)
	return &v, nil
}

func (s *webhookService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.GetWebhookEndpoint(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v1.ErrWebhookNotFound
		}
		return fmt.Errorf("get webhook: %w", err)
	}
	return s.db.DeleteWebhookEndpoint(ctx, id)
}

// SendTest delivers a test payload to the endpoint synchronously (single attempt).
func (s *webhookService) SendTest(ctx context.Context, id uuid.UUID) error {
	ep, err := s.db.GetWebhookEndpoint(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return v1.ErrWebhookNotFound
		}
		return fmt.Errorf("get webhook: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"event": "media.test",
		"user":  true,
		"owner": true,
	})
	return deliverWebhook(ctx, s.client, s.enc, ep, body)
}

// ── Shared helpers ────────────────────────────────────────────────────────────

func genWebhookToV1(ep gen.WebhookEndpoint) v1.WebhookEndpoint {
	return v1.WebhookEndpoint{
		ID:      ep.ID,
		URL:     ep.Url,
		Events:  ep.Events,
		Enabled: ep.Enabled,
	}
}

func encryptSecret(enc *auth.Encryptor, secret string) (*string, error) {
	if secret == "" {
		return nil, nil
	}
	encrypted, err := enc.Encrypt(secret)
	if err != nil {
		return nil, fmt.Errorf("encrypt webhook secret: %w", err)
	}
	return &encrypted, nil
}

// deliverWebhook delegates to the shared webhook.Deliver helper.
func deliverWebhook(ctx context.Context, client *http.Client, enc *auth.Encryptor, ep gen.WebhookEndpoint, body []byte) error {
	return webhook.Deliver(ctx, client, enc, ep, body)
}
