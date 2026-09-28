package email

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
)

// NotificationEmail builds the text and HTML bodies for an outbound
// notification-agent message (Settings → Notifications → Email): a heading,
// a plain-text body (line breaks preserved) and an optional "Open in OnScreen"
// link. Every field is escaped in the HTML part; linkURL is only rendered when
// it is an absolute http(s) URL.
func NotificationEmail(heading, body, linkURL string) (textBody, htmlBody string) {
	if !strings.HasPrefix(linkURL, "https://") && !strings.HasPrefix(linkURL, "http://") {
		linkURL = ""
	}
	var tb strings.Builder
	tb.WriteString(heading)
	tb.WriteString("\n\n")
	tb.WriteString(body)
	if linkURL != "" {
		tb.WriteString("\n\nOpen in OnScreen: ")
		tb.WriteString(linkURL)
	}
	tb.WriteString("\n\n-- \nSent by OnScreen (Settings > Notifications).\n")

	htmlBody = render(emailTemplate{
		Heading:    heading,
		Body:       body,
		ButtonText: "Open in OnScreen",
		ButtonURL:  linkURL,
		Footer:     "Sent by OnScreen. An admin can change which events are emailed under Settings → Notifications.",
		// Keep the message's line breaks (the new-content list is one title
		// per line).
		PreserveBreaks: true,
	})
	return tb.String(), htmlBody
}

// headerText folds CR/LF (header injection) and RFC 2047-encodes a header
// value that isn't plain ASCII.
func headerText(s string) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
	return mime.QEncoding.Encode("utf-8", s)
}

// buildAlternativeMessage builds a multipart/alternative RFC 5322 message
// with quoted-printable text and HTML parts.
func buildAlternativeMessage(from string, to []string, subject, textBody, htmlBody string) (string, error) {
	var rb [12]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return "", fmt.Errorf("email: boundary: %w", err)
	}
	boundary := "onscreen-" + hex.EncodeToString(rb[:])

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + headerText(subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")
	for _, part := range []struct{ ctype, body string }{
		{"text/plain; charset=\"UTF-8\"", textBody},
		{"text/html; charset=\"UTF-8\"", htmlBody},
	} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + part.ctype + "\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&b)
		if _, err := qp.Write([]byte(strings.ReplaceAll(part.body, "\r\n", "\n"))); err != nil {
			return "", fmt.Errorf("email: encode part: %w", err)
		}
		if err := qp.Close(); err != nil {
			return "", fmt.Errorf("email: encode part: %w", err)
		}
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), nil
}
