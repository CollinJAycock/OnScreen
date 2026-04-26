// Package email provides SMTP-based email sending for OnScreen.
//
// SMTP credentials live in server_settings (admin Settings → Email), so the
// Sender resolves Config on every Send rather than caching at startup. That
// lets admins flip SMTP on/off and rotate credentials without restarting
// the server. Callers should call Enabled(ctx) before invoking flows that
// require email — the handler responds with a friendly "not configured"
// error instead of a doomed SMTP dial.
package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// Config holds SMTP connection parameters resolved from server settings.
type Config struct {
	Enabled  bool
	Host     string
	Port     int
	Username string
	Password string
	From     string // e.g. "OnScreen <noreply@example.com>"
}

// complete returns true when every field required to dial+send is populated.
// Username/Password are optional (open relay or anonymous SMTP submission).
func (c Config) complete() bool {
	return c.Host != "" && c.Port != 0 && c.From != ""
}

// ConfigFunc resolves the current SMTP configuration. Called on every
// Enabled / Send invocation so that admin-side credential changes take
// effect without a server restart.
type ConfigFunc func(ctx context.Context) Config

// ErrNotConfigured is returned by Send when SMTP is disabled or incomplete.
var ErrNotConfigured = errors.New("email: SMTP is not configured")

// Sender sends emails via SMTP using credentials resolved per-call from a
// ConfigFunc. Always non-nil; check Enabled(ctx) for the live state.
type Sender struct {
	config ConfigFunc
}

// NewSender wires a Sender against a config provider.
func NewSender(config ConfigFunc) *Sender {
	if config == nil {
		config = func(context.Context) Config { return Config{} }
	}
	return &Sender{config: config}
}

// Enabled reports whether the current SMTP config is complete and toggled on.
// Handlers gate their flows on this — the user-facing copy says "Email is
// not configured on this server" rather than failing at SMTP-dial time.
func (s *Sender) Enabled(ctx context.Context) bool {
	cfg := s.config(ctx)
	return cfg.Enabled && cfg.complete()
}

// Send sends an email with the given subject and HTML body to the recipients.
// Returns ErrNotConfigured when SMTP is off or incomplete.
//
// Each recipient is parsed with net/mail.ParseAddress before being sent
// to the server. Go's net/smtp Rcpt method substitutes the address into
// the SMTP RCPT TO command without escaping, so a malformed address
// containing CRLF could otherwise inject arbitrary SMTP commands.
// ParseAddress also catches admin typos at the API layer rather than at
// the SMTP server.
func (s *Sender) Send(ctx context.Context, to []string, subject, htmlBody string) error {
	cfg := s.config(ctx)
	if !cfg.Enabled || !cfg.complete() {
		return ErrNotConfigured
	}
	for _, addr := range to {
		if _, err := mail.ParseAddress(addr); err != nil {
			return fmt.Errorf("email: invalid recipient %q: %w", addr, err)
		}
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	msg := buildMessage(cfg.From, to, subject, htmlBody)

	// Connect to the SMTP server with a bounded deadline.
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	// STARTTLS if supported.
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: cfg.Host}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	// Authenticate if credentials provided.
	if cfg.Username != "" && cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	// Set sender.
	if err := c.Mail(extractEmail(cfg.From)); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}

	// Set recipients.
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("email: rcpt %s: %w", addr, err)
		}
	}

	// Send the message body.
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("email: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close: %w", err)
	}

	return c.Quit()
}

// buildMessage constructs an RFC 2822 email with HTML content.
func buildMessage(from string, to []string, subject, htmlBody string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return b.String()
}

// extractEmail pulls the bare email from "Name <email>" format.
func extractEmail(from string) string {
	if i := strings.Index(from, "<"); i != -1 {
		if j := strings.Index(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return from
}
