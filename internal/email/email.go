// Package email provides SMTP-based email sending for OnScreen.
// All email is optional — if SMTP is not configured, the Sender is nil
// and callers should check before calling Send.
package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// Config holds SMTP connection parameters.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // e.g. "OnScreen <noreply@example.com>"
}

// Sender sends emails via SMTP.
type Sender struct {
	cfg Config
}

// NewSender creates a new SMTP sender. Returns nil if the config is incomplete.
func NewSender(cfg Config) *Sender {
	if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" {
		return nil
	}
	return &Sender{cfg: cfg}
}

// Send sends an email with the given subject and HTML body to the recipients.
func (s *Sender) Send(to []string, subject, htmlBody string) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))

	msg := buildMessage(s.cfg.From, to, subject, htmlBody)

	// Connect to the SMTP server.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	// STARTTLS if supported.
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: s.cfg.Host}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	// Authenticate if credentials provided.
	if s.cfg.Username != "" && s.cfg.Password != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	// Set sender.
	if err := c.Mail(extractEmail(s.cfg.From)); err != nil {
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
