package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/i18n"
)

// The "Report a problem" notices come from the notify.* catalog; the
// notification service writes each recipient's copy in their language. These
// render the notices the handler builds in every language.

func checkIssueText(t *testing.T, c issueNotifyCall, want map[i18n.Lang][2]string) {
	t.Helper()
	for l, w := range want {
		title, body := c.text(l)
		if title != w[0] || body != w[1] {
			t.Errorf("%s in %s:\n got  %q / %q\n want %q / %q", c.typ, l, title, body, w[0], w[1])
		}
	}
}

func TestIssues_ReportedNoticeTranslated(t *testing.T) {
	fx := newIssuesFixture()
	if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"wrong_match"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	checkIssueText(t, fx.notifier.calls[0], map[i18n.Lang][2]string{
		i18n.English:           {"Problem reported", "alice reported a problem with Heat: wrong match"},
		i18n.Spanish:           {"Problema informado", "alice informó de un problema con Heat: película o serie equivocada"},
		i18n.SimplifiedChinese: {"收到问题报告", "alice 报告了“Heat”的问题：电影/剧集不对"},
	})

	// Every kind has its label in every language (no key shown as text).
	for kind := range issueKindLabels {
		fx := newIssuesFixture()
		if rec := fx.create(t, fx.user, fx.movie.ID, `{"kind":"`+kind+`"}`); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", kind, rec.Code)
		}
		for _, l := range i18n.Supported {
			if _, body := fx.notifier.calls[0].text(l); body == "" || strings.Contains(body, "notify.") {
				t.Errorf("%s in %s: body %q", kind, l, body)
			}
		}
	}

	// No username: the sentence that names nobody.
	fx = newIssuesFixture()
	anon := &auth.Claims{UserID: uuid.New()}
	if rec := fx.create(t, anon, fx.movie.ID, `{"kind":"video"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	checkIssueText(t, fx.notifier.calls[0], map[i18n.Lang][2]string{
		i18n.English:           {"Problem reported", "A user reported a problem with Heat: video"},
		i18n.Spanish:           {"Problema informado", "Un usuario informó de un problema con Heat: vídeo"},
		i18n.SimplifiedChinese: {"收到问题报告", "一位用户报告了“Heat”的问题：视频"},
	})
}

func TestIssues_ResolvedNoticeTranslated(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		itemGone bool
		want     map[i18n.Lang][2]string
	}{
		{
			name: "resolved with a note",
			body: `{"status":"resolved","note":"Replaced the file."}`,
			want: map[i18n.Lang][2]string{
				i18n.English:           {"Problem report resolved", "An admin resolved your report about Heat. Replaced the file."},
				i18n.Spanish:           {"Informe de problema resuelto", "Un administrador resolvió tu informe sobre Heat. Replaced the file."},
				i18n.SimplifiedChinese: {"问题报告已解决", "管理员已解决你关于“Heat”的报告。Replaced the file."},
			},
		},
		{
			name: "dismissed",
			body: `{"status":"dismissed"}`,
			want: map[i18n.Lang][2]string{
				i18n.English:           {"Problem report closed", "An admin closed your report about Heat."},
				i18n.Spanish:           {"Informe de problema cerrado", "Un administrador cerró tu informe sobre Heat."},
				i18n.SimplifiedChinese: {"问题报告已关闭", "管理员已关闭你关于“Heat”的报告。"},
			},
		},
		{
			name:     "item gone",
			body:     `{"status":"resolved"}`,
			itemGone: true,
			want: map[i18n.Lang][2]string{
				i18n.English:           {"Problem report resolved", "An admin resolved your report about the item."},
				i18n.Spanish:           {"Informe de problema resuelto", "Un administrador resolvió tu informe sobre el elemento."},
				i18n.SimplifiedChinese: {"问题报告已解决", "管理员已解决你关于该项目的报告。"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newIssuesFixture()
			_ = fx.create(t, fx.user, fx.movie.ID, `{"kind":"video"}`)
			var issueID uuid.UUID
			for id := range fx.db.issues {
				issueID = id
			}
			fx.notifier.calls = nil
			if tc.itemGone {
				delete(fx.db.items, fx.movie.ID)
			}
			rec := httptest.NewRecorder()
			fx.h.AdminUpdate(rec, issueReq(t, http.MethodPatch, "/api/v1/admin/issues/"+issueID.String(), tc.body, fx.admin,
				map[string]string{"id": issueID.String()}))
			if rec.Code != http.StatusOK || len(fx.notifier.calls) != 1 {
				t.Fatalf("update: %d %s, notices %+v", rec.Code, rec.Body, fx.notifier.calls)
			}
			checkIssueText(t, fx.notifier.calls[0], tc.want)
		})
	}
}
