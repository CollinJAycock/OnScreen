package v1

import (
	"bufio"
	"context"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/email"
)

// Which language each account email is written in, checked on the wire
// through a loopback SMTP relay: a password reset in the account's language
// (else the asking screen's), an invite and the SMTP test in the language of
// the admin's request.

// smtpCapture is a loopback SMTP relay that hands each message it receives
// to got. Loopback needs no STARTTLS (email.Sender allows cleartext there).
type smtpCapture struct {
	ln  net.Listener
	got chan string
}

func newSMTPCapture(t *testing.T) *smtpCapture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	c := &smtpCapture{ln: ln, got: make(chan string, 4)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go c.serve(conn)
		}
	}()
	return c
}

func (c *smtpCapture) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s)) }
	w("220 capture ESMTP\r\n")
	var msg strings.Builder
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				c.got <- msg.String()
				w("250 ok\r\n")
				continue
			}
			msg.WriteString(line)
			continue
		}
		switch up := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			w("250-capture\r\n250 SIZE 0\r\n")
		case up == "DATA":
			inData = true
			w("354 go ahead\r\n")
		case up == "QUIT":
			w("221 bye\r\n")
			return
		default:
			w("250 ok\r\n")
		}
	}
}

func (c *smtpCapture) sender() *email.Sender {
	port := c.ln.Addr().(*net.TCPAddr).Port
	return email.NewSender(func(context.Context) email.Config {
		return email.Config{Enabled: true, Host: "127.0.0.1", Port: port, From: "OnScreen <noreply@example.com>"}
	})
}

// subject waits for the next message and returns its Subject header,
// decoded as a mail client does (a subject that isn't plain ASCII is sent as
// RFC 2047 encoded-words).
func (c *smtpCapture) subject(t *testing.T) string {
	t.Helper()
	select {
	case msg := <-c.got:
		for line := range strings.SplitSeq(msg, "\r\n") {
			if v, ok := strings.CutPrefix(line, "Subject: "); ok {
				dec, err := new(mime.WordDecoder).DecodeHeader(v)
				if err != nil {
					t.Fatalf("subject %q: %v", v, err)
				}
				return dec
			}
		}
		t.Fatalf("message has no Subject:\n%s", msg)
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the relay")
	}
	return ""
}

func TestForgotPassword_EmailLanguage(t *testing.T) {
	es, fr := "es", "fr"
	cases := []struct {
		name       string
		uiLanguage *string
		accept     string
		want       string
	}{
		{"account language wins", &es, "zh-CN,zh;q=0.9", "OnScreen — Restablecer contraseña"},
		{"else the request's language", nil, "zh-CN,zh;q=0.9", "OnScreen — 重置密码"},
		{"else English", nil, "", "OnScreen — Password Reset"},
		{"an unshipped account language falls back", &fr, "zh-CN", "OnScreen — 重置密码"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			relay := newSMTPCapture(t)
			addr := "alice@example.com"
			db := &mockPasswordResetDB{user: PRUser{ID: uuid.New(), Username: "alice", Email: &addr, UILanguage: tc.uiLanguage}}
			h := newPRHandler(db, relay.sender())
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(`{"email":"alice@example.com"}`))
			if tc.accept != "" {
				req.Header.Set("Accept-Language", tc.accept)
			}
			h.ForgotPassword(httptest.NewRecorder(), req)
			if got := relay.subject(t); got != tc.want {
				t.Errorf("subject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInviteCreate_EmailInAdminsLanguage(t *testing.T) {
	relay := newSMTPCapture(t)
	h := NewInviteHandler(&mockInviteDB{}, relay.sender(), "http://localhost:3000", slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader(`{"email":"bob@example.com"}`))
	req.Header.Set("Accept-Language", "es-MX,es;q=0.9,en;q=0.5")
	req = withAdminClaims(req, uuid.New(), "admin")
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, want := relay.subject(t), "Te invitaron a OnScreen"; got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

func TestEmailSendTest_RequestLanguage(t *testing.T) {
	for accept, want := range map[string]string{
		"zh-Hans": "OnScreen — SMTP 测试",
		"":        "OnScreen — SMTP Test",
	} {
		relay := newSMTPCapture(t)
		h := NewEmailHandler(relay.sender(), slog.Default())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/email/test", strings.NewReader(`{"to":"admin@example.com"}`))
		if accept != "" {
			req.Header.Set("Accept-Language", accept)
		}
		rec := httptest.NewRecorder()
		h.SendTest(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status %d: %s", accept, rec.Code, rec.Body)
		}
		if got := relay.subject(t); got != want {
			t.Errorf("%q: subject = %q, want %q", accept, got, want)
		}
	}
}
