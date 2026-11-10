package email

import (
	"fmt"
	"html"
	"strings"

	"github.com/onscreen/onscreen/internal/i18n"
)

// The account emails are written in one language, l, from the email.*
// catalog (internal/i18n/locales/*/email.json). The caller picks it: the
// recipient's ui_language for a password reset, else the language of the
// request that asked for it (docs/i18n.md). render escapes the finished text,
// placeholder values included, exactly as it escaped the English before.

// PasswordResetEmail builds the HTML for a password reset email.
func PasswordResetEmail(l i18n.Lang, username, resetURL string) (subject, body string) {
	subject = i18n.T(l, "email.passwordReset.subject", nil)
	body = render(emailTemplate{
		Heading:    i18n.T(l, "email.passwordReset.heading", nil),
		Body:       i18n.T(l, "email.passwordReset.body", i18n.Params{"username": username}),
		ButtonText: i18n.T(l, "email.passwordReset.button", nil),
		ButtonURL:  resetURL,
		Footer:     i18n.T(l, "email.passwordReset.footer", nil),
	})
	return
}

// InviteEmail builds the HTML for a user invitation email.
func InviteEmail(l i18n.Lang, inviterName, inviteURL string) (subject, body string) {
	subject = i18n.T(l, "email.invite.subject", nil)
	body = render(emailTemplate{
		Heading:    i18n.T(l, "email.invite.heading", nil),
		Body:       i18n.T(l, "email.invite.body", i18n.Params{"inviter": inviterName}),
		ButtonText: i18n.T(l, "email.invite.button", nil),
		ButtonURL:  inviteURL,
		Footer:     i18n.T(l, "email.invite.footer", nil),
	})
	return
}

// WelcomeEmail builds the HTML for a welcome email after registration.
// Nothing sends it yet, so it is still English only.
func WelcomeEmail(username, loginURL string) (subject, body string) {
	subject = "Welcome to OnScreen"
	body = render(emailTemplate{
		Heading:    "Welcome to OnScreen",
		Body:       fmt.Sprintf("Hi %s, your account has been created. You can sign in and start exploring your media library.", username),
		ButtonText: "Sign In",
		ButtonURL:  loginURL,
		Footer:     "Enjoy your media!",
	})
	return
}

// TestEmail builds a simple test email for verifying SMTP config.
func TestEmail(l i18n.Lang) (subject, body string) {
	subject = i18n.T(l, "email.test.subject", nil)
	body = render(emailTemplate{
		Heading:    i18n.T(l, "email.test.heading", nil),
		Body:       i18n.T(l, "email.test.body", nil),
		ButtonText: "",
		ButtonURL:  "",
		Footer:     i18n.T(l, "email.test.footer", nil),
	})
	return
}

type emailTemplate struct {
	Heading    string
	Body       string
	ButtonText string
	ButtonURL  string
	Footer     string
	// PreserveBreaks renders the body's line breaks as <br> (after escaping).
	PreserveBreaks bool
}

func render(t emailTemplate) string {
	// Escape every interpolated field. Call sites currently pass
	// server-controlled strings and regex-restricted usernames, but routing
	// the raw value through html.EscapeString means a future caller that
	// forwards a looser field (display name, managed-profile label, etc.)
	// cannot land stored XSS in recipients' mail clients.
	heading := html.EscapeString(t.Heading)
	body := html.EscapeString(t.Body)
	if t.PreserveBreaks {
		body = strings.ReplaceAll(body, "\n", "<br>")
	}
	footer := html.EscapeString(t.Footer)

	var btn string
	if t.ButtonText != "" && t.ButtonURL != "" {
		btn = fmt.Sprintf(`<tr><td style="padding:24px 0 0">
			<a href="%s" style="display:inline-block;padding:12px 32px;background:#7c6af7;color:#fff;text-decoration:none;border-radius:8px;font-weight:600;font-size:15px">%s</a>
		</td></tr>`, html.EscapeString(t.ButtonURL), html.EscapeString(t.ButtonText))
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>`)
	b.WriteString(`<body style="margin:0;padding:0;background:#07070d;font-family:system-ui,-apple-system,sans-serif">`)
	b.WriteString(`<table width="100%" cellpadding="0" cellspacing="0" style="background:#07070d;padding:40px 20px"><tr><td align="center">`)
	b.WriteString(`<table width="420" cellpadding="0" cellspacing="0" style="background:#0e0e18;border:1px solid rgba(255,255,255,0.06);border-radius:16px;padding:40px">`)

	// Header
	b.WriteString(`<tr><td style="text-align:center;padding-bottom:24px">`)
	b.WriteString(`<span style="font-size:22px;font-weight:700;color:#eeeef8;letter-spacing:-0.02em">OnScreen</span>`)
	b.WriteString(`</td></tr>`)

	// Heading
	b.WriteString(fmt.Sprintf(`<tr><td style="font-size:18px;font-weight:600;color:#eeeef8;padding-bottom:12px">%s</td></tr>`, heading))

	// Body
	b.WriteString(fmt.Sprintf(`<tr><td style="font-size:14px;color:#8888a0;line-height:1.6">%s</td></tr>`, body))

	// Button
	if btn != "" {
		b.WriteString(btn)
	}

	// Footer
	b.WriteString(fmt.Sprintf(`<tr><td style="padding-top:32px;font-size:12px;color:#44445a;border-top:1px solid rgba(255,255,255,0.06);margin-top:24px">%s</td></tr>`, footer))

	b.WriteString(`</table></td></tr></table></body></html>`)
	return b.String()
}
