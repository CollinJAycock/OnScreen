// Package webhook provides shared webhook delivery utilities.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// SafeTransport returns an *http.Transport that rejects connections to
// private, loopback, link-local, multicast, and unspecified addresses
// at dial time, preventing user-configured webhook URLs from being
// turned into a probe of the operator's internal network. The check
// fires post-resolution via the dialer's Control hook, which closes
// the DNS-rebinding TOCTOU window — Go won't re-resolve between the
// check and the connect.
//
// This is a thin wrapper around safehttp.NewDialer so the webhook
// path and every other outbound fetch (TMDB, LRCLIB, Schedules
// Direct, plugin egress) share one SSRF policy.
func SafeTransport() *http.Transport {
	return &http.Transport{
		DialContext:           safehttp.NewDialer(safehttp.DialPolicy{}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
	}
}

// Deliver POSTs body to ep.Url with optional HMAC-SHA256 signing.
// If the endpoint has an encrypted secret, it is decrypted and used to sign
// the payload. On decrypt failure the request is delivered unsigned.
func Deliver(ctx context.Context, client *http.Client, enc *auth.Encryptor, ep gen.WebhookEndpoint, body []byte) error {
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
		} else {
			slog.WarnContext(ctx, "webhook decrypt failed, delivering unsigned", "url", ep.Url, "err", decErr)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain the body so the underlying TCP connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("non-2xx response: %d", resp.StatusCode)
	}
	return nil
}
