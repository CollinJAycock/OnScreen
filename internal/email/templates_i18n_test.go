package email

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/i18n"
)

// The account emails come from the email.* catalog. English must stay
// byte-identical to the text the templates held before translation: these
// legacy* builders are that code, kept here as the reference.

func legacyPasswordResetEmail(username, resetURL string) (string, string) {
	return "OnScreen — Password Reset", render(emailTemplate{
		Heading:    "Reset Your Password",
		Body:       fmt.Sprintf("Hi %s, we received a request to reset your password. Click the button below to choose a new one. This link expires in 1 hour.", username),
		ButtonText: "Reset Password",
		ButtonURL:  resetURL,
		Footer:     "If you didn't request this, you can safely ignore this email.",
	})
}

func legacyInviteEmail(inviterName, inviteURL string) (string, string) {
	return "You're invited to OnScreen", render(emailTemplate{
		Heading:    "You've Been Invited",
		Body:       fmt.Sprintf("%s has invited you to join their OnScreen media server. Click below to set up your account.", inviterName),
		ButtonText: "Accept Invite",
		ButtonURL:  inviteURL,
		Footer:     "If you weren't expecting this, you can safely ignore this email.",
	})
}

func legacyTestEmail() (string, string) {
	return "OnScreen — SMTP Test", render(emailTemplate{
		Heading: "SMTP Configuration Works",
		Body:    "If you're reading this, your OnScreen email configuration is working correctly.",
		Footer:  "This is a test email sent from OnScreen.",
	})
}

func TestEmails_EnglishUnchanged(t *testing.T) {
	// A name with HTML in it checks that the escaping is unchanged too.
	for _, name := range []string{"alice", `<b>mallory</b> & "co"`} {
		gotS, gotB := PasswordResetEmail(i18n.English, name, "https://example.com/reset?token=abc")
		wantS, wantB := legacyPasswordResetEmail(name, "https://example.com/reset?token=abc")
		if gotS != wantS || gotB != wantB {
			t.Errorf("password reset (%q) changed:\nsubject %q\nwant    %q\nbody %s\nwant %s", name, gotS, wantS, gotB, wantB)
		}
		gotS, gotB = InviteEmail(i18n.English, name, "https://example.com/invite?token=xyz")
		wantS, wantB = legacyInviteEmail(name, "https://example.com/invite?token=xyz")
		if gotS != wantS || gotB != wantB {
			t.Errorf("invite (%q) changed:\nsubject %q\nwant    %q\nbody %s\nwant %s", name, gotS, wantS, gotB, wantB)
		}
	}
	gotS, gotB := TestEmail(i18n.English)
	wantS, wantB := legacyTestEmail()
	if gotS != wantS || gotB != wantB {
		t.Errorf("SMTP test email changed:\nsubject %q\nwant    %q\nbody %s\nwant %s", gotS, wantS, gotB, wantB)
	}
}

func TestEmails_Translated(t *testing.T) {
	const name = `<b>ana</b>`
	const escaped = `&lt;b&gt;ana&lt;/b&gt;`
	resetURL := "https://example.com/reset?token=abc"
	inviteURL := "https://example.com/invite?token=xyz"

	cases := []struct {
		name    string
		build   func(l i18n.Lang) (string, string)
		subject map[i18n.Lang]string
		// body holds text each translation must contain.
		body map[i18n.Lang][]string
		// english is text the English email has and a translation must not.
		english []string
		// url is the link the button must keep, unchanged.
		url string
	}{
		{
			name:  "password reset",
			build: func(l i18n.Lang) (string, string) { return PasswordResetEmail(l, name, resetURL) },
			subject: map[i18n.Lang]string{
				i18n.Spanish:           "OnScreen — Restablecer contraseña",
				i18n.SimplifiedChinese: "OnScreen — 重置密码",
			},
			body: map[i18n.Lang][]string{
				i18n.Spanish:           {"Restablece tu contraseña", "Hola, " + escaped + ": recibimos", "caduca en 1 hora", ">Restablecer contraseña</a>"},
				i18n.SimplifiedChinese: {"重置你的密码", escaped + "，你好！", "此链接将在 1 小时后失效", ">重置密码</a>"},
			},
			english: []string{"Reset Your Password", "we received a request", "Reset Password", "safely ignore"},
			url:     resetURL,
		},
		{
			name:  "invite",
			build: func(l i18n.Lang) (string, string) { return InviteEmail(l, name, inviteURL) },
			subject: map[i18n.Lang]string{
				i18n.Spanish:           "Te invitaron a OnScreen",
				i18n.SimplifiedChinese: "你受邀加入 OnScreen",
			},
			body: map[i18n.Lang][]string{
				i18n.Spanish:           {"Tienes una invitación", escaped + " te invitó a unirte", ">Aceptar invitación</a>"},
				i18n.SimplifiedChinese: {"你收到了一份邀请", escaped + " 邀请你加入", ">接受邀请</a>"},
			},
			english: []string{"Been Invited", "has invited you", "Accept Invite", "safely ignore"},
			url:     inviteURL,
		},
		{
			name:  "SMTP test",
			build: TestEmail,
			subject: map[i18n.Lang]string{
				i18n.Spanish:           "OnScreen — Prueba de SMTP",
				i18n.SimplifiedChinese: "OnScreen — SMTP 测试",
			},
			body: map[i18n.Lang][]string{
				i18n.Spanish:           {"La configuración de SMTP funciona", "Si estás leyendo esto", "correo de prueba"},
				i18n.SimplifiedChinese: {"SMTP 配置正常", "如果你看到了这封邮件", "测试邮件"},
			},
			english: []string{"Configuration Works", "reading this", "test email"},
		},
	}
	for _, tc := range cases {
		for _, l := range []i18n.Lang{i18n.Spanish, i18n.SimplifiedChinese} {
			subject, body := tc.build(l)
			if subject != tc.subject[l] {
				t.Errorf("%s %s: subject %q, want %q", tc.name, l, subject, tc.subject[l])
			}
			for _, want := range tc.body[l] {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s: body lacks %q:\n%s", tc.name, l, want, body)
				}
			}
			for _, en := range tc.english {
				if strings.Contains(body, en) {
					t.Errorf("%s %s: English %q left in the body", tc.name, l, en)
				}
			}
			if strings.Contains(body, "<b>ana") {
				t.Errorf("%s %s: the name reached the HTML unescaped", tc.name, l)
			}
			if tc.url != "" && !strings.Contains(body, `href="`+tc.url+`"`) {
				t.Errorf("%s %s: button link %q missing", tc.name, l, tc.url)
			}
			if !strings.Contains(body, ">OnScreen</span>") {
				t.Errorf("%s %s: the OnScreen header is missing", tc.name, l)
			}
		}
	}
}
