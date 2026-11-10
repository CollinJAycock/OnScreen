package requests

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/i18n"
	"github.com/onscreen/onscreen/internal/notification"
)

// The request notices come from the notify.* catalog and are written in each
// recipient's language (the notification service picks it). These tests
// render what the service would store, in Spanish and Chinese, and pin that
// English is what the code wrote before translation.

// lastText is the newest requester notice of typ.
func (n *fakeNotifier) lastText(t *testing.T, typ string) notification.Text {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.sent) - 1; i >= 0; i-- {
		if n.sent[i].typ == typ {
			return n.texts[i]
		}
	}
	t.Fatalf("no %s notice sent; got %+v", typ, n.sent)
	return nil
}

// lastAdminText is the newest admin notice.
func (n *fakeNotifier) lastAdminText(t *testing.T) notification.Text {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.adminTexts) == 0 {
		t.Fatal("no admin notice sent")
	}
	return n.adminTexts[len(n.adminTexts)-1]
}

type rendered map[i18n.Lang][2]string // language → title, body

func checkText(t *testing.T, what string, text notification.Text, want rendered) {
	t.Helper()
	for l, w := range want {
		title, body := text(l)
		if title != w[0] || body != w[1] {
			t.Errorf("%s in %s:\n got  %q / %q\n want %q / %q", what, l, title, body, w[0], w[1])
		}
	}
}

func TestNotices_LifecycleTranslated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	alice := h.user(gen.GetUserRequestPermissionsRow{Username: "alice"})

	queued, err := h.svc.Create(ctx, CreateInput{UserID: alice, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	checkText(t, "request_created", h.notify.lastText(t, notification.TypeRequestCreated), rendered{
		i18n.English:           {"Request submitted", `"Heat" is awaiting admin approval.`},
		i18n.Spanish:           {"Solicitud enviada", "«Heat» está pendiente de la aprobación de un administrador."},
		i18n.SimplifiedChinese: {"请求已提交", "“Heat”正在等待管理员批准。"},
	})
	checkText(t, "request_pending", h.notify.lastAdminText(t), rendered{
		i18n.English:           {"New request", `alice requested "Heat (1995)"`},
		i18n.Spanish:           {"Nueva solicitud", "alice solicitó «Heat (1995)»"},
		i18n.SimplifiedChinese: {"新请求", "alice 请求了“Heat (1995)”"},
	})

	if _, err := h.svc.Approve(ctx, ApproveInput{RequestID: queued.ID, AdminID: uuid.New()}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	checkText(t, "request_approved", h.notify.lastText(t, notification.TypeRequestApproved), rendered{
		i18n.English:           {"Request approved", `"Heat" is being downloaded.`},
		i18n.Spanish:           {"Solicitud aprobada", "«Heat» se está descargando."},
		i18n.SimplifiedChinese: {"请求已批准", "“Heat”正在下载。"},
	})

	h.svc.MarkFulfilled(ctx, TypeMovie, 949, uuid.New())
	checkText(t, "request_available", h.notify.lastText(t, notification.TypeRequestAvailable), rendered{
		i18n.English:           {"Now available: Heat", `"Heat" is ready to watch.`},
		i18n.Spanish:           {"Ya disponible: Heat", "«Heat» ya se puede ver."},
		i18n.SimplifiedChinese: {"现已可观看：Heat", "“Heat”已可观看。"},
	})

	show, err := h.svc.Create(ctx, CreateInput{UserID: alice, Type: TypeShow, TMDBID: 95396})
	if err != nil {
		t.Fatalf("Create show: %v", err)
	}
	// The admin's reason is their own words, never translated.
	if _, err := h.svc.Decline(ctx, show.ID, uuid.New(), "no space"); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	checkText(t, "request_declined", h.notify.lastText(t, notification.TypeRequestDeclined), rendered{
		i18n.English:           {"Request declined: Severance", "Your request was declined: no space"},
		i18n.Spanish:           {"Solicitud rechazada: Severance", "Tu solicitud fue rechazada: no space"},
		i18n.SimplifiedChinese: {"请求已拒绝：Severance", "你的请求已被拒绝：no space"},
	})
}

func TestNotices_AutoApprovedTranslated(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true, Username: "bob"})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	checkText(t, "auto-approved", h.notify.lastText(t, notification.TypeRequestApproved), rendered{
		i18n.English:           {"Request approved", `"Heat" was approved automatically and is being downloaded.`},
		i18n.Spanish:           {"Solicitud aprobada", "«Heat» se aprobó automáticamente y se está descargando."},
		i18n.SimplifiedChinese: {"请求已批准", "“Heat”已自动批准，正在下载。"},
	})
}

func TestNotices_OverQuotaTranslated(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{TV: 1, WindowDays: 7}
	uid := h.user(gen.GetUserRequestPermissionsRow{Username: "bob"})
	h.db.seed(uid, TypeShow, StatusAvailable, h.now.Add(-day))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 95396})
	if err != nil || !got.OverQuota {
		t.Fatalf("Create = %+v, %v; want over quota", got, err)
	}
	checkText(t, "over-quota request_created", h.notify.lastText(t, notification.TypeRequestCreated), rendered{
		i18n.English:           {"Request submitted", `"Severance" is over your TV request limit and is awaiting admin approval.`},
		i18n.Spanish:           {"Solicitud enviada", "«Severance» supera tu límite de solicitudes de series y está pendiente de la aprobación de un administrador."},
		i18n.SimplifiedChinese: {"请求已提交", "“Severance”超出了你的剧集请求限额，正在等待管理员批准。"},
	})
	checkText(t, "over-quota request_pending", h.notify.lastAdminText(t), rendered{
		i18n.English:           {"New request", `bob requested "Severance (2022)" (over their TV quota)`},
		i18n.Spanish:           {"Nueva solicitud", "bob solicitó «Severance (2022)» (supera su cuota de series)"},
		i18n.SimplifiedChinese: {"新请求", "bob 请求了“Severance (2022)”（超出其剧集限额）"},
	})
}

func TestNotices_AnonymousRequesterTranslated(t *testing.T) {
	h := newHarness(t)
	h.db.permsErr = errors.New("db down")
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uuid.New(), Type: TypeShow, TMDBID: 95396}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	checkText(t, "anonymous request_pending", h.notify.lastAdminText(t), rendered{
		i18n.English:           {"New request", `A user requested "Severance (2022)"`},
		i18n.Spanish:           {"Nueva solicitud", "Un usuario solicitó «Severance (2022)»"},
		i18n.SimplifiedChinese: {"新请求", "一位用户请求了“Severance (2022)”"},
	})
}

// TestNotices_DownloadFailedEnglishUnchanged renders the failure notices for a
// title with quotes in it next to the code they replaced: English is
// byte-identical (notifyagents unquotes the admin copy's title), and the
// translations keep the requester, title, service and reason as written.
func TestNotices_DownloadFailedEnglishUnchanged(t *testing.T) {
	const title, reason = `Say "Hi" \ Bye`, "Download client reported failure"
	year := int32(2020)
	svc := gen.ArrService{Name: "Radarr 1080p"}

	for _, named := range []bool{true, false} {
		h := newHarness(t)
		requester, who := uuid.New(), "A user"
		if named {
			requester, who = h.user(gen.GetUserRequestPermissionsRow{Username: "alice"}), "alice"
		}
		r := gen.MediaRequest{UserID: requester, Title: title, Year: &year}
		for _, why := range []string{reason, ""} {
			h.svc.notifyDownloadFailed(context.Background(), svc, r, why)

			// What the code wrote before translation.
			legacyAdmin := fmt.Sprintf("%s's request for %q failed in %s", who, titleWithYear(title, 2020), svc.Name)
			if why != "" {
				legacyAdmin += ": " + why
			}
			checkText(t, "request_failed (user)", h.notify.lastText(t, notification.TypeRequestFailed), rendered{
				i18n.English: {"Download failed: " + title, fmt.Sprintf("%q couldn't be downloaded. An admin has been told and can retry it.", title)},
			})
			checkText(t, "request_failed (admin)", h.notify.lastAdminText(t), rendered{
				i18n.English: {"Request download failed", legacyAdmin},
			})
		}
	}

	h := newHarness(t)
	alice := h.user(gen.GetUserRequestPermissionsRow{Username: "alice"})
	h.svc.notifyDownloadFailed(context.Background(), svc, gen.MediaRequest{UserID: alice, Title: "Heat", Year: &year}, reason)
	checkText(t, "request_failed (user)", h.notify.lastText(t, notification.TypeRequestFailed), rendered{
		i18n.Spanish:           {"Falló la descarga: Heat", "No se pudo descargar «Heat». Ya se avisó a un administrador, que puede volver a intentarlo."},
		i18n.SimplifiedChinese: {"下载失败：Heat", "“Heat”无法下载。已通知管理员，管理员可以重试。"},
	})
	checkText(t, "request_failed (admin)", h.notify.lastAdminText(t), rendered{
		i18n.Spanish:           {"Falló la descarga de una solicitud", "La solicitud de alice para «Heat (2020)» falló en Radarr 1080p: " + reason},
		i18n.SimplifiedChinese: {"请求的下载失败", "alice 请求的“Heat (2020)”在 Radarr 1080p 中下载失败：" + reason},
	})
}

func TestNotices_SeasonAvailableTranslated(t *testing.T) {
	h := newFulfilHarness(t)
	h.inFlight(t, []int{1, 2}, true)
	h.sonarr.set(1, 10, 10)
	h.sonarr.set(2, 5, 10)
	h.db.ownSeason(1399, 1, 10)
	h.db.ownSeason(1399, 2, 5)
	h.svc.ReconcileFulfillments(context.Background())
	checkText(t, "request_season_available", h.notify.lastText(t, notification.TypeRequestSeasonAvailable), rendered{
		i18n.English:           {"Season 1 now available: Show", `Season 1 of "Show" is ready to watch.`},
		i18n.Spanish:           {"Temporada 1 ya disponible: Show", "La temporada 1 de «Show» ya se puede ver."},
		i18n.SimplifiedChinese: {"第 1 季现已可观看：Show", "“Show”第 1 季已可观看。"},
	})
}

// quoted must write exactly what %q wrote inside its quotes.
func TestQuoted_MatchesPercentQ(t *testing.T) {
	for _, s := range []string{"Heat", `Say "Hi"`, `a\b`, "Amélie", "千与千寻", "tab\there", ""} {
		if got, want := `"`+quoted(s)+`"`, fmt.Sprintf("%q", s); got != want {
			t.Errorf("quoted(%q) = %s, %%q = %s", s, got, want)
		}
	}
}
