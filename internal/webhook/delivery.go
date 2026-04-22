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
	"net"
	"net/http"
	"time"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// SafeTransport returns an *http.Transport that rejects connections to private,
// loopback, and link-local IP addresses at dial time. This prevents DNS
// rebinding attacks where a hostname resolves to a public IP at validation
// time but is re-pointed to an internal IP before the actual HTTP request is
// made.
//
// Closes the TOCTOU window by dialing the validated IP literal, not the
// hostname: Go's dialer won't re-resolve an IP, so the second lookup an
// attacker could otherwise rebind simply never happens. TLS SNI and cert
// verification still use the URL host via http.Transport's TLSClientConfig,
// which is derived from the request URL rather than the dial address.
func SafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("split host port: %w", err)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", host, err)
			}
			// Reject-if-ANY-non-public: split-horizon DNS returning a mix
			// of public and private records is treated as hostile.
			for _, ip := range ips {
				if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() {
					return nil, fmt.Errorf("webhook target %s resolves to private address %s", host, ip.IP)
				}
			}
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("webhook: no usable IPs for %s", host)
			}
			return nil, lastErr
		},
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
