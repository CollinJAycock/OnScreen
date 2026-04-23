package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Sender — Enabled / Send gating
// ---------------------------------------------------------------------------

func staticConfig(c Config) ConfigFunc {
	return func(context.Context) Config { return c }
}

func TestSender_Enabled_RequiresEnabledFlag(t *testing.T) {
	s := NewSender(staticConfig(Config{
		Host: "smtp.example.com", Port: 587, From: "OnScreen <noreply@example.com>",
	}))
	if s.Enabled(context.Background()) {
		t.Fatal("expected Enabled=false when Config.Enabled is false")
	}
}

func TestSender_Enabled_IncompleteHost(t *testing.T) {
	s := NewSender(staticConfig(Config{
		Enabled: true, Port: 587, From: "OnScreen <noreply@example.com>",
	}))
	if s.Enabled(context.Background()) {
		t.Fatal("expected Enabled=false when Host is empty")
	}
}

func TestSender_Enabled_IncompletePort(t *testing.T) {
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: "smtp.example.com", From: "OnScreen <noreply@example.com>",
	}))
	if s.Enabled(context.Background()) {
		t.Fatal("expected Enabled=false when Port is 0")
	}
}

func TestSender_Enabled_IncompleteFrom(t *testing.T) {
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: "smtp.example.com", Port: 587,
	}))
	if s.Enabled(context.Background()) {
		t.Fatal("expected Enabled=false when From is empty")
	}
}

func TestSender_Enabled_FullConfig(t *testing.T) {
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: "smtp.example.com", Port: 587,
		From: "OnScreen <noreply@example.com>",
	}))
	if !s.Enabled(context.Background()) {
		t.Fatal("expected Enabled=true with complete enabled config")
	}
}

func TestSender_Send_ReturnsErrNotConfiguredWhenDisabled(t *testing.T) {
	s := NewSender(staticConfig(Config{}))
	err := s.Send(context.Background(), []string{"a@b.c"}, "hi", "<p>x</p>")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestNewSender_NilConfigFuncSafe(t *testing.T) {
	s := NewSender(nil)
	if s.Enabled(context.Background()) {
		t.Fatal("nil ConfigFunc should resolve to disabled")
	}
}

// ---------------------------------------------------------------------------
// extractEmail
// ---------------------------------------------------------------------------

func TestExtractEmail_AngleBrackets(t *testing.T) {
	got := extractEmail("OnScreen <noreply@example.com>")
	if got != "noreply@example.com" {
		t.Fatalf("extractEmail angle brackets: got %q, want %q", got, "noreply@example.com")
	}
}

func TestExtractEmail_RawAddress(t *testing.T) {
	got := extractEmail("noreply@example.com")
	if got != "noreply@example.com" {
		t.Fatalf("extractEmail raw: got %q, want %q", got, "noreply@example.com")
	}
}

// ---------------------------------------------------------------------------
// buildMessage
// ---------------------------------------------------------------------------

func TestBuildMessage_Headers(t *testing.T) {
	from := "OnScreen <noreply@example.com>"
	to := []string{"alice@example.com", "bob@example.com"}
	subject := "Test Subject"
	body := "<h1>Hello</h1>"

	msg := buildMessage(from, to, subject, body)

	checks := []struct {
		label string
		want  string
	}{
		{"From header", "From: " + from + "\r\n"},
		{"To header", "To: alice@example.com, bob@example.com\r\n"},
		{"Subject header", "Subject: Test Subject\r\n"},
		{"MIME-Version", "MIME-Version: 1.0\r\n"},
		{"Content-Type", `Content-Type: text/html; charset="UTF-8"` + "\r\n"},
		{"header/body separator", "\r\n\r\n"},
		{"HTML body", body},
	}

	for _, c := range checks {
		if !strings.Contains(msg, c.want) {
			t.Errorf("buildMessage missing %s: want substring %q in message", c.label, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Template functions
// ---------------------------------------------------------------------------

func TestPasswordResetEmail(t *testing.T) {
	subject, body := PasswordResetEmail("alice", "https://example.com/reset?token=abc")

	if subject != "OnScreen — Password Reset" {
		t.Fatalf("subject = %q, want %q", subject, "OnScreen — Password Reset")
	}
	if !strings.Contains(body, "alice") {
		t.Error("body should contain the username")
	}
	if !strings.Contains(body, "https://example.com/reset?token=abc") {
		t.Error("body should contain the reset URL")
	}
}

func TestInviteEmail(t *testing.T) {
	subject, body := InviteEmail("Bob", "https://example.com/invite/xyz")

	if subject != "You're invited to OnScreen" {
		t.Fatalf("subject = %q, want %q", subject, "You're invited to OnScreen")
	}
	if !strings.Contains(body, "Bob") {
		t.Error("body should contain the inviter name")
	}
	if !strings.Contains(body, "https://example.com/invite/xyz") {
		t.Error("body should contain the invite URL")
	}
}

func TestWelcomeEmail(t *testing.T) {
	subject, body := WelcomeEmail("carol", "https://example.com/login")

	if subject != "Welcome to OnScreen" {
		t.Fatalf("subject = %q, want %q", subject, "Welcome to OnScreen")
	}
	if !strings.Contains(body, "carol") {
		t.Error("body should contain the username")
	}
	if !strings.Contains(body, "https://example.com/login") {
		t.Error("body should contain the login URL")
	}
}

func TestTestEmail(t *testing.T) {
	subject, body := TestEmail()

	if subject != "OnScreen — SMTP Test" {
		t.Fatalf("subject = %q, want %q", subject, "OnScreen — SMTP Test")
	}
	if !strings.Contains(body, "SMTP Configuration Works") {
		t.Error("body should contain the heading text")
	}
}
