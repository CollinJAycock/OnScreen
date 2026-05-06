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
	"strconv"
	"time"

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
		DialContext:       safehttp.NewDialer(safehttp.DialPolicy{}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      20,
	}
}

// SafeClient wraps SafeTransport in an *http.Client whose CheckRedirect
// refuses to follow 3xx responses. Webhook deliveries that 307 to a
// different host would otherwise leak the signed POST body to whatever
// destination the receiver names — including a public URL the operator
// never approved. Returning ErrUseLastResponse stops the redirect chase
// and surfaces the 3xx to the caller, which Deliver treats as a non-2xx
// error and retries / logs.
//
// Mirrors the posture of the plugin egress path (internal/plugin/egress.go).
// Use SafeClient instead of building http.Client{Transport: SafeTransport()}
// directly so every webhook caller picks up redirect refusal automatically.
func SafeClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: SafeTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Deliver POSTs body to ep.Url with optional HMAC-SHA256 signing.
// If the endpoint has an encrypted secret, it is decrypted and used to sign
// the payload. On decrypt failure the request is delivered unsigned.
//
// Signature scheme (Stripe-shaped, replay-safe):
//
//	X-OnScreen-Timestamp: <unix-seconds>
//	X-OnScreen-Signature: sha256=<hex(HMAC(secret, "{ts}.{body}"))>
//
// Receivers MUST:
//   1. Reject timestamps outside a small window (e.g. ±5 minutes) to
//      defeat replay of captured-and-cached requests.
//   2. Recompute the HMAC over `{header_ts}.{request_body}` and compare
//      with constant-time equality. The timestamp is part of the
//      signed input, so an attacker can't change `X-OnScreen-Timestamp`
//      without invalidating the MAC.
//
// The earlier sha256(secret || body) form was replayable indefinitely
// — anyone who captured a valid (sig, body) pair could replay it
// forever with no protocol-level defense.
func Deliver(ctx context.Context, client *http.Client, enc *auth.Encryptor, ep gen.WebhookEndpoint, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if ep.Secret != nil && *ep.Secret != "" {
		if rawSecret, decErr := enc.Decrypt(*ep.Secret); decErr == nil {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(rawSecret))
			// Stripe pattern: sign "{ts}.{body}" so the timestamp is
			// part of the authenticated input — receiver detects
			// tampering by recomputing the MAC, not by trusting the
			// header in isolation.
			mac.Write([]byte(ts))
			mac.Write([]byte("."))
			mac.Write(body)
			req.Header.Set("X-OnScreen-Timestamp", ts)
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
