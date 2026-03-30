package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// WebhookServerInfo identifies this server in webhook payloads.
type WebhookServerInfo struct {
	Title     string
	MachineID string
}

// webhookDeliveryDB is the subset of DB operations needed by the dispatcher.
type webhookDeliveryDB interface {
	ListWebhookEndpoints(ctx context.Context) ([]gen.WebhookEndpoint, error)
	CreateWebhookFailure(ctx context.Context, arg gen.CreateWebhookFailureParams) (gen.WebhookFailure, error)
}

// webhookMediaDB is used to enrich payloads with media metadata.
type webhookMediaDB interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
}

// maxConcurrentDeliveries limits the number of concurrent webhook delivery goroutines.
// Each goroutine may sleep up to 5m30s during retries, so bounding this prevents
// unbounded memory/goroutine growth under heavy webhook load.
const maxConcurrentDeliveries = 20

// WebhookDispatcher delivers webhook events asynchronously to all subscribed endpoints.
type WebhookDispatcher struct {
	db     webhookDeliveryDB
	media  webhookMediaDB
	enc    *auth.Encryptor
	server WebhookServerInfo
	client *http.Client
	logger *slog.Logger
	sem    chan struct{} // concurrency limiter for delivery goroutines
}

// NewWebhookDispatcher creates a WebhookDispatcher.
func NewWebhookDispatcher(
	db webhookDeliveryDB,
	media webhookMediaDB,
	enc *auth.Encryptor,
	server WebhookServerInfo,
	logger *slog.Logger,
) *WebhookDispatcher {
	return &WebhookDispatcher{
		db:     db,
		media:  media,
		enc:    enc,
		server: server,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
		sem:    make(chan struct{}, maxConcurrentDeliveries),
	}
}

// Dispatch fires eventType to all enabled, subscribed endpoints.
// Non-blocking — each delivery runs in its own goroutine.
func (d *WebhookDispatcher) Dispatch(eventType string, userID, mediaID uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		endpoints, err := d.db.ListWebhookEndpoints(ctx)
		if err != nil {
			d.logger.Warn("webhook dispatch: list endpoints", "err", err)
			return
		}

		payload := d.buildPayload(ctx, eventType, userID, mediaID)
		body, err := json.Marshal(payload)
		if err != nil {
			d.logger.Warn("webhook dispatch: marshal payload", "err", err)
			return
		}

		for _, ep := range endpoints {
			if !ep.Enabled || !slices.Contains(ep.Events, eventType) {
				continue
			}
			ep := ep // capture loop var
			// Acquire semaphore slot — blocks if maxConcurrentDeliveries are in-flight.
			d.sem <- struct{}{}
			go func() {
				defer func() { <-d.sem }()
				d.deliverWithRetry(ep, body)
			}()
		}
	}()
}

// WebhookPayload is the webhook payload sent to external endpoints (Overseerr, Tautulli, etc.).
type WebhookPayload struct {
	Event    string           `json:"event"`
	User     bool             `json:"user"`
	Owner    bool             `json:"owner"`
	Server   WebhookServer    `json:"Server"`
	Metadata *WebhookMetadata `json:"Metadata,omitempty"`
}

// WebhookServer describes the server in a webhook payload.
type WebhookServer struct {
	Title string `json:"title"`
	UUID  string `json:"uuid"`
}

// WebhookMetadata is the media item portion of a webhook payload.
type WebhookMetadata struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Year  *int   `json:"year,omitempty"`
	Key   string `json:"key"`
}

func (d *WebhookDispatcher) buildPayload(ctx context.Context, eventType string, userID, mediaID uuid.UUID) WebhookPayload {
	hasUser := userID != uuid.Nil
	p := WebhookPayload{
		Event: webhookEvent(eventType),
		User:  hasUser,
		Owner: hasUser,
		Server: WebhookServer{
			Title: d.server.Title,
			UUID:  d.server.MachineID,
		},
	}
	if mediaID != uuid.Nil {
		if item, err := d.media.GetItem(ctx, mediaID); err == nil {
			p.Metadata = &WebhookMetadata{
				Type:  item.Type,
				Title: item.Title,
				Year:  item.Year,
				Key:   "/api/v1/items/" + mediaID.String(),
			}
		}
	}
	return p
}

// deliverWithRetry attempts delivery up to 3 times.
// Delays: attempt 1 immediate, attempt 2 +30s, attempt 3 +5min.
// On total failure writes to webhook_failures.
func (d *WebhookDispatcher) deliverWithRetry(ep gen.WebhookEndpoint, body []byte) {
	delays := []time.Duration{0, 30 * time.Second, 5 * time.Minute}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := deliverWebhookHTTP(ctx, d.client, d.enc, ep, body)
		cancel()
		if err == nil {
			return
		}
		lastErr = err
		d.logger.Warn("webhook delivery failed",
			"url", ep.Url, "attempt", attempt+1, "err", err)
	}

	// All attempts exhausted — record failure.
	ctx := context.Background()
	if _, err := d.db.CreateWebhookFailure(ctx, gen.CreateWebhookFailureParams{
		EndpointID:   ep.ID,
		Url:          ep.Url,
		Payload:      body,
		LastError:    lastErr.Error(),
		AttemptCount: 3,
	}); err != nil {
		d.logger.Error("record webhook failure", "url", ep.Url, "err", err)
	}
}

// deliverWebhookHTTP POSTs body to ep.Url, signing with HMAC-SHA256 if secret is set.
func deliverWebhookHTTP(ctx context.Context, client *http.Client, enc *auth.Encryptor, ep gen.WebhookEndpoint, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.Secret != nil && *ep.Secret != "" {
		if rawSecret, decErr := enc.Decrypt(*ep.Secret); decErr == nil {
			mac := hmac.New(sha256.New, []byte(rawSecret))
			mac.Write(body)
			req.Header.Set("X-OnScreen-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain the response body to enable TCP connection reuse by net/http.
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("non-2xx response: %d", resp.StatusCode)
	}
	return nil
}

func webhookEvent(eventType string) string {
	switch eventType {
	case "play":
		return "media.play"
	case "pause":
		return "media.pause"
	case "resume":
		return "media.resume"
	case "stop":
		return "media.stop"
	case "scrobble":
		return "media.scrobble"
	default:
		// Already fully-qualified event names (e.g. "library.scan.complete")
		// are passed through as-is.
		if strings.Contains(eventType, ".") {
			return eventType
		}
		return "media." + eventType
	}
}
