package email

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"localhost":   true,
		"LOCALHOST":   true,
		"127.0.0.1":   true,
		"127.0.0.5":   true,
		"::1":         true,
		"example.com": false,
		"10.0.0.5":    false,
		"8.8.8.8":     false,
		"":            false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// fakeSMTP is a minimal in-process SMTP server for exercising Send without a
// real relay. advertiseSTARTTLS controls whether EHLO offers STARTTLS.
type fakeSMTP struct {
	ln       net.Listener
	starttls bool
	received chan string
}

func newFakeSMTP(t *testing.T, listenIP string, advertiseSTARTTLS bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(listenIP, "0"))
	if err != nil {
		t.Fatalf("fake smtp listen on %s: %v", listenIP, err)
	}
	f := &fakeSMTP{ln: ln, starttls: advertiseSTARTTLS, received: make(chan string, 1)}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s)) }
	w("220 fake ESMTP\r\n")
	var body strings.Builder
	inData := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				w("250 ok\r\n")
				select {
				case f.received <- body.String():
				default:
				}
				continue
			}
			body.WriteString(line)
			continue
		}
		up := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			if f.starttls {
				w("250-fake\r\n250-STARTTLS\r\n250 SIZE 0\r\n")
			} else {
				w("250-fake\r\n250 SIZE 0\r\n")
			}
		case up == "DATA":
			w("354 end data with <CR><LF>.<CR><LF>\r\n")
			inData = true
		case up == "QUIT":
			w("221 bye\r\n")
			return
		default:
			// MAIL FROM, RCPT TO, RSET, NOOP, …
			w("250 ok\r\n")
		}
	}
}

// TestSend_LoopbackNoSTARTTLS_Succeeds: a loopback relay that doesn't offer
// STARTTLS is allowed (no network to MITM), and the full MAIL/RCPT/DATA
// exchange completes.
func TestSend_LoopbackNoSTARTTLS_Succeeds(t *testing.T) {
	f := newFakeSMTP(t, "127.0.0.1", false)
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: "127.0.0.1", Port: f.port(),
		From: "OnScreen <noreply@example.com>",
	}))
	if err := s.Send(context.Background(), []string{"to@example.com"}, "Subj", "<p>hi</p>"); err != nil {
		t.Fatalf("Send (loopback, no STARTTLS) should succeed: %v", err)
	}
	select {
	case body := <-f.received:
		if !strings.Contains(body, "Subj") {
			t.Errorf("delivered body missing subject: %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fake SMTP never received the message body")
	}
}

// TestSend_NonLoopbackNoSTARTTLS_Refuses: a non-loopback relay that doesn't
// offer STARTTLS must be refused rather than sending reset/invite tokens in
// cleartext (the STARTTLS-strip guard). Skipped when the host has no
// non-loopback IPv4 interface to bind the fake relay to.
func TestSend_NonLoopbackNoSTARTTLS_Refuses(t *testing.T) {
	ip := nonLoopbackIPv4()
	if ip == "" {
		t.Skip("no non-loopback IPv4 interface available")
	}
	f := newFakeSMTP(t, ip, false)
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: ip, Port: f.port(),
		From: "OnScreen <noreply@example.com>",
	}))
	err := s.Send(context.Background(), []string{"to@example.com"}, "Subj", "<p>hi</p>")
	if err == nil || !strings.Contains(err.Error(), "refusing to send over cleartext") {
		t.Fatalf("Send (non-loopback, no STARTTLS) must refuse; got %v", err)
	}
}

func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		// GlobalUnicast excludes loopback, link-local (169.254.x APIPA),
		// multicast, and unspecified — leaving routable/private addresses.
		if !ok || !ipnet.IP.IsGlobalUnicast() {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil {
			continue
		}
		// Confirm it's actually bindable in this environment before using it.
		ln, err := net.Listen("tcp", net.JoinHostPort(v4.String(), "0"))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return v4.String()
	}
	return ""
}
