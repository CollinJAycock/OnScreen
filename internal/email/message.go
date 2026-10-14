package email

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime/quotedprintable"
	"regexp"
	"sort"
	"strings"
)

// InlineImage is an image carried inside the message and referenced from the
// HTML as src="cid:<ContentID>". Inline images need no network access to
// display: nothing in the HTML points back at the server, so the message
// carries no credential and leaks nothing when forwarded.
type InlineImage struct {
	ContentID   string // without angle brackets; [A-Za-z0-9._@-] only
	ContentType string // "image/jpeg", "image/png"
	Data        []byte
}

// Message is a multipart message with a plain-text and an HTML body, optional
// inline images, and optional extra headers (List-Unsubscribe and the like).
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
	Inline  []InlineImage
	// Headers are extra header fields. Names must be RFC 5322 field names;
	// values have CR/LF folded away. Use them for List-Unsubscribe /
	// List-Unsubscribe-Post, not for From / To / Subject.
	Headers map[string]string
}

// SendMessage sends m. Same transport, TLS posture and ErrNotConfigured
// semantics as Send.
func (s *Sender) SendMessage(ctx context.Context, m Message) error {
	cfg := s.config(ctx)
	if !cfg.Enabled || !cfg.complete() {
		return ErrNotConfigured
	}
	if err := validateRecipients(m.To); err != nil {
		return err
	}
	msg, err := buildRichMessage(cfg.From, m)
	if err != nil {
		return err
	}
	return deliver(ctx, cfg, m.To, msg)
}

var (
	headerNameRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	contentIDRE  = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)
	// reservedHeaders are set by buildRichMessage itself.
	reservedHeaders = map[string]bool{
		"from": true, "to": true, "subject": true, "mime-version": true,
		"content-type": true, "content-transfer-encoding": true, "bcc": true, "cc": true,
	}
)

func newBoundary() (string, error) {
	var rb [12]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return "", fmt.Errorf("email: boundary: %w", err)
	}
	return "onscreen-" + hex.EncodeToString(rb[:]), nil
}

// writeQP writes body as a quoted-printable MIME part (headers included).
func writeQP(b *strings.Builder, ctype, body string) error {
	b.WriteString("Content-Type: " + ctype + "\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n"))); err != nil {
		return fmt.Errorf("email: encode part: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("email: encode part: %w", err)
	}
	b.WriteString("\r\n")
	return nil
}

// writeBase64 writes data base64-encoded in 76-column lines.
func writeBase64(b *strings.Builder, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	if enc != "" {
		b.WriteString(enc + "\r\n")
	}
}

// buildRichMessage builds:
//
//	multipart/alternative
//	├── text/plain
//	└── multipart/related            (only when there are inline images)
//	    ├── text/html
//	    └── image/* (Content-ID)…
func buildRichMessage(from string, m Message) (string, error) {
	outer, err := newBoundary()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(m.To, ", ") + "\r\n")
	b.WriteString("Subject: " + headerText(m.Subject) + "\r\n")
	names := make([]string, 0, len(m.Headers))
	for name := range m.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !headerNameRE.MatchString(name) || reservedHeaders[strings.ToLower(name)] {
			return "", fmt.Errorf("email: header %q not allowed", name)
		}
		v := strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Headers[name])), " ")
		b.WriteString(name + ": " + v + "\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + outer + "\"\r\n\r\n")

	b.WriteString("--" + outer + "\r\n")
	if err := writeQP(&b, "text/plain; charset=\"UTF-8\"", m.Text); err != nil {
		return "", err
	}

	b.WriteString("--" + outer + "\r\n")
	if len(m.Inline) == 0 {
		if err := writeQP(&b, "text/html; charset=\"UTF-8\"", m.HTML); err != nil {
			return "", err
		}
	} else {
		related, err := newBoundary()
		if err != nil {
			return "", err
		}
		b.WriteString("Content-Type: multipart/related; type=\"text/html\"; boundary=\"" + related + "\"\r\n\r\n")
		b.WriteString("--" + related + "\r\n")
		if err := writeQP(&b, "text/html; charset=\"UTF-8\"", m.HTML); err != nil {
			return "", err
		}
		for _, img := range m.Inline {
			if !contentIDRE.MatchString(img.ContentID) {
				return "", fmt.Errorf("email: invalid content id %q", img.ContentID)
			}
			ctype := img.ContentType
			if ctype != "image/jpeg" && ctype != "image/png" && ctype != "image/webp" && ctype != "image/gif" {
				return "", fmt.Errorf("email: inline content type %q not allowed", ctype)
			}
			b.WriteString("--" + related + "\r\n")
			b.WriteString("Content-Type: " + ctype + "\r\n")
			b.WriteString("Content-Transfer-Encoding: base64\r\n")
			b.WriteString("Content-ID: <" + img.ContentID + ">\r\n")
			b.WriteString("Content-Disposition: inline\r\n\r\n")
			writeBase64(&b, img.Data)
		}
		b.WriteString("--" + related + "--\r\n")
	}
	b.WriteString("--" + outer + "--\r\n")
	return b.String(), nil
}
