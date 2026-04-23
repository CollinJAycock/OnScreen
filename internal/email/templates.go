package email

import (
	"fmt"
	"html"
	"strings"
)

// PasswordResetEmail builds the HTML for a password reset email.
func PasswordResetEmail(username, resetURL string) (subject, body string) {
	subject = "OnScreen — Password Reset"
	body = render(emailTemplate{
		Heading:    "Reset Your Password",
		Body:       fmt.Sprintf("Hi %s, we received a request to reset your password. Click the button below to choose a new one. This link expires in 1 hour.", username),
		ButtonText: "Reset Password",
		ButtonURL:  resetURL,
		Footer:     "If you didn't request this, you can safely ignore this email.",
	})
	return
}

// InviteEmail builds the HTML for a user invitation email.
func InviteEmail(inviterName, inviteURL string) (subject, body string) {
	subject = "You're invited to OnScreen"
	body = render(emailTemplate{
		Heading:    "You've Been Invited",
		Body:       fmt.Sprintf("%s has invited you to join their OnScreen media server. Click below to set up your account.", inviterName),
		ButtonText: "Accept Invite",
		ButtonURL:  inviteURL,
		Footer:     "If you weren't expecting this, you can safely ignore this email.",
	})
	return
}

// WelcomeEmail builds the HTML for a welcome email after registration.
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
func TestEmail() (subject, body string) {
	subject = "OnScreen — SMTP Test"
	body = render(emailTemplate{
		Heading:    "SMTP Configuration Works",
		Body:       "If you're reading this, your OnScreen email configuration is working correctly.",
		ButtonText: "",
		ButtonURL:  "",
		Footer:     "This is a test email sent from OnScreen.",
	})
	return
}

type emailTemplate struct {
	Heading    string
	Body       string
	ButtonText string
	ButtonURL  string
	Footer     string
}

func render(t emailTemplate) string {
	// Escape every interpolated field. Call sites currently pass
	// server-controlled strings and regex-restricted usernames, but routing
	// the raw value through html.EscapeString means a future caller that
	// forwards a looser field (display name, managed-profile label, etc.)
	// cannot land stored XSS in recipients' mail clients.
	heading := html.EscapeString(t.Heading)
	body := html.EscapeString(t.Body)
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
