package email

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestNotificationEmail(t *testing.T) {
	text, html := NotificationEmail("Now available: <Dune>", "line one\n<b>line two</b>", "https://media.example.com/watch/1")
	if !strings.Contains(text, "Now available: <Dune>\n\nline one\n<b>line two</b>") ||
		!strings.Contains(text, "Open in OnScreen: https://media.example.com/watch/1") {
		t.Errorf("text = %q", text)
	}
	if strings.Contains(html, "<Dune>") || strings.Contains(html, "<b>line two") {
		t.Error("html not escaped")
	}
	if !strings.Contains(html, "line one<br>&lt;b&gt;line two&lt;/b&gt;") {
		t.Error("html lost the line break")
	}
	if !strings.Contains(html, `href="https://media.example.com/watch/1"`) {
		t.Error("html missing the link")
	}
	_, html = NotificationEmail("h", "b", "javascript:alert(1)")
	if strings.Contains(html, "javascript:") {
		t.Error("non-http link rendered")
	}
}

// TestBuildAlternativeMessage: multipart/alternative with text then HTML,
// quoted-printable parts, a CRLF-folded and RFC 2047-encoded subject.
func TestBuildAlternativeMessage(t *testing.T) {
	raw, err := buildAlternativeMessage("OnScreen <noreply@example.com>", []string{"a@example.com"},
		"[OnScreen] Amélie\r\nBcc: victim@example.com", "plain body é", "<p>html body</p>")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("subject newline injected a header")
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subj != "[OnScreen] Amélie Bcc: victim@example.com" {
		t.Errorf("subject = %q, %v", subj, err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content-type = %q", msg.Header.Get("Content-Type"))
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types, bodies []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p) // multipart decodes quoted-printable
		types = append(types, p.Header.Get("Content-Type"))
		bodies = append(bodies, string(b))
	}
	if len(types) != 2 || !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Fatalf("parts = %v", types)
	}
	if bodies[0] != "plain body é" || bodies[1] != "<p>html body</p>" {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestSendWithText_DeliversMultipart(t *testing.T) {
	f := newFakeSMTP(t, "127.0.0.1", false)
	s := NewSender(staticConfig(Config{Enabled: true, Host: "127.0.0.1", Port: f.port(), From: "OnScreen <noreply@example.com>"}))
	if err := s.SendWithText(context.Background(), []string{"to@example.com"}, "Subj", "text", "<p>html</p>"); err != nil {
		t.Fatalf("SendWithText: %v", err)
	}
	select {
	case body := <-f.received:
		if !strings.Contains(body, "multipart/alternative") || !strings.Contains(body, "Subject: Subj") {
			t.Errorf("delivered = %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing delivered")
	}
	if err := NewSender(nil).SendWithText(context.Background(), []string{"a@b.c"}, "s", "t", "h"); err != ErrNotConfigured {
		t.Errorf("unconfigured = %v", err)
	}
	if err := s.SendWithText(context.Background(), []string{"bad\r\naddr"}, "s", "t", "h"); err == nil {
		t.Error("invalid recipient accepted")
	}
}
