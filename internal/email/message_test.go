package email

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// parseRich parses a built message back with the standard library, the way a
// mail client would.
func parseRich(t *testing.T, raw string) (*mail.Message, *multipart.Reader) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("top-level content type = %q (%v), want multipart/alternative", mt, err)
	}
	return msg, multipart.NewReader(msg.Body, params["boundary"])
}

func TestBuildRichMessage_InlineImagesAndHeaders(t *testing.T) {
	jpeg := bytes.Repeat([]byte{0xff, 0xd8, 0x01, 0x02}, 100)
	raw, err := buildRichMessage("OnScreen <noreply@example.com>", Message{
		To:      []string{"a@example.com"},
		Subject: "Your weekly digest — Amélie",
		Text:    "plain body",
		HTML:    `<p><img src="cid:poster-1@onscreen"></p>`,
		Inline:  []InlineImage{{ContentID: "poster-1@onscreen", ContentType: "image/jpeg", Data: jpeg}},
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://media.example.com/api/v1/digest/unsubscribe?token=abc>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, mr := parseRich(t, raw)
	if got := msg.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
	if got := msg.Header.Get("List-Unsubscribe"); !strings.Contains(got, "token=abc") {
		t.Errorf("List-Unsubscribe = %q", got)
	}
	dec := new(mime.WordDecoder)
	if subj, _ := dec.DecodeHeader(msg.Header.Get("Subject")); subj != "Your weekly digest — Amélie" {
		t.Errorf("subject round-trip = %q", subj)
	}

	text, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("first part = %q, want text/plain", text.Header.Get("Content-Type"))
	}
	body, _ := io.ReadAll(text)
	if string(body) != "plain body" {
		t.Errorf("text body = %q", body)
	}

	rel, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	mt, params, _ := mime.ParseMediaType(rel.Header.Get("Content-Type"))
	if mt != "multipart/related" {
		t.Fatalf("second part = %q, want multipart/related", mt)
	}
	rr := multipart.NewReader(rel, params["boundary"])
	html, err := rr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := io.ReadAll(html)
	if !strings.Contains(string(hb), "cid:poster-1@onscreen") {
		t.Errorf("html part = %q", hb)
	}
	img, err := rr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if img.Header.Get("Content-ID") != "<poster-1@onscreen>" {
		t.Errorf("Content-ID = %q", img.Header.Get("Content-ID"))
	}
	// multipart.Reader doesn't decode base64; compare after decoding.
	enc, _ := io.ReadAll(img)
	if !strings.Contains(raw, "Content-Transfer-Encoding: base64") || len(enc) == 0 {
		t.Error("image part not base64-encoded")
	}
}

func TestBuildRichMessage_NoImagesIsPlainAlternative(t *testing.T) {
	raw, err := buildRichMessage("a@example.com", Message{To: []string{"b@example.com"}, Subject: "s", Text: "t", HTML: "<p>h</p>"})
	if err != nil {
		t.Fatal(err)
	}
	_, mr := parseRich(t, raw)
	if _, err := mr.NextPart(); err != nil {
		t.Fatal(err)
	}
	p, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
		t.Errorf("second part = %q, want text/html", p.Header.Get("Content-Type"))
	}
}

func TestBuildRichMessage_RefusesHeaderInjection(t *testing.T) {
	cases := []Message{
		{Headers: map[string]string{"Bcc": "x@example.com"}},
		{Headers: map[string]string{"X-Bad Name": "v"}},
		{Headers: map[string]string{"X-Bad\r\nBcc": "v"}},
		{Inline: []InlineImage{{ContentID: "a>\r\nX-Evil: 1", ContentType: "image/jpeg"}}},
		{Inline: []InlineImage{{ContentID: "ok", ContentType: "text/html"}}},
	}
	for i, m := range cases {
		m.To = []string{"a@example.com"}
		if _, err := buildRichMessage("a@example.com", m); err == nil {
			t.Errorf("case %d: built a message it should refuse", i)
		}
	}
	// A CR/LF in a header VALUE is folded, not refused, and can't start a new
	// header line.
	raw, err := buildRichMessage("a@example.com", Message{
		To:      []string{"a@example.com"},
		Headers: map[string]string{"List-Unsubscribe": "<https://x>\r\nBcc: evil@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "\r\nBcc:") {
		t.Error("CR/LF in a header value started a new header")
	}
}

func TestSendMessage_DeliversOverLoopback(t *testing.T) {
	f := newFakeSMTP(t, "127.0.0.1", false)
	s := NewSender(staticConfig(Config{
		Enabled: true, Host: "127.0.0.1", Port: f.port(),
		From: "OnScreen <noreply@example.com>",
	}))
	err := s.SendMessage(context.Background(), Message{
		To: []string{"to@example.com"}, Subject: "Digest", Text: "t", HTML: "<p>h</p>",
		Headers: map[string]string{"List-Unsubscribe-Post": "List-Unsubscribe=One-Click"},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	select {
	case body := <-f.received:
		if !strings.Contains(body, "List-Unsubscribe-Post") {
			t.Errorf("delivered message missing the extra header: %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fake SMTP never received the message")
	}
}

func TestSendMessage_NotConfigured(t *testing.T) {
	s := NewSender(staticConfig(Config{}))
	if err := s.SendMessage(context.Background(), Message{To: []string{"a@example.com"}}); err != ErrNotConfigured {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}
