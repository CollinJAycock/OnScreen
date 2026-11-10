package notification

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/i18n"
)

// TestNotifyAdmins_EachInTheirLanguage: one notice to every admin is written
// in each admin's ui_language (English when unset), and the outbound agents
// get it once, in English.
func TestNotifyAdmins_EachInTheirLanguage(t *testing.T) {
	en, es, zh := uuid.New(), uuid.New(), uuid.New()
	db := &mockDB{
		adminIDs: []uuid.UUID{en, es, zh},
		langs:    map[uuid.UUID]string{es: "es", zh: "zh-Hans"},
	}
	svc := NewService(db, NewBroker(), slog.Default())
	rec := &recordingAgents{}
	svc.SetAgentDispatcher(rec)

	svc.NotifyAdmins(context.Background(), TypeRequestPending, Keys(
		"notify.requestPending.title", nil,
		"notify.requestPending.body", i18n.Params{"user": "alice", "title": "Heat (1995)"}), nil)

	want := map[uuid.UUID][2]string{
		en: {"New request", `alice requested "Heat (1995)"`},
		es: {"Nueva solicitud", "alice solicitó «Heat (1995)»"},
		zh: {"新请求", "alice 请求了“Heat (1995)”"},
	}
	if len(db.created) != 3 {
		t.Fatalf("created %d notices, want 3", len(db.created))
	}
	for _, c := range db.created {
		w := want[c.UserID]
		if c.Title != w[0] || c.Body != w[1] {
			t.Errorf("user %s: got %q / %q, want %q / %q", c.UserID, c.Title, c.Body, w[0], w[1])
		}
	}
	if len(rec.events) != 1 || rec.events[0].Title != "New request" || rec.events[0].Body != `alice requested "Heat (1995)"` {
		t.Errorf("agent event = %+v, want the English notice once", rec.events)
	}
}

// TestNotify_UnknownLanguageIsEnglish: a ui_language the server doesn't ship
// (or none) reads in English.
func TestNotify_UnknownLanguageIsEnglish(t *testing.T) {
	uid := uuid.New()
	db := &mockDB{langs: map[uuid.UUID]string{uid: "fr"}}
	svc := NewService(db, NewBroker(), slog.Default())
	svc.Notify(context.Background(), uid, TypeRequestApproved, Keys("notify.requestApproved.title", nil, "", nil), nil)
	if len(db.created) != 1 || db.created[0].Title != "Request approved" || db.created[0].Body != "" {
		t.Errorf("created = %+v, want the English title and no body", db.created)
	}
}

func TestNotifyScanComplete_Translated(t *testing.T) {
	cases := []struct {
		lang        string
		count       int
		title, body string
	}{
		{"", 1, "Library scan complete", "Movies: 1 new item added"},
		// English has always written the count without grouping.
		{"", 1234, "Library scan complete", "Movies: 1234 new items added"},
		{"es", 1, "Escaneo de la biblioteca completado", "Movies: se añadió 1 elemento nuevo"},
		{"es", 5, "Escaneo de la biblioteca completado", "Movies: se añadieron 5 elementos nuevos"},
		{"zh-Hans", 1, "媒体库扫描完成", "Movies：新增 1 个项目"},
		{"zh-Hans", 5, "媒体库扫描完成", "Movies：新增 5 个项目"},
	}
	for _, tc := range cases {
		uid := uuid.New()
		db := &mockDB{libUserIDs: []uuid.UUID{uid}, langs: map[uuid.UUID]string{}}
		if tc.lang != "" {
			db.langs[uid] = tc.lang
		}
		svc := NewService(db, NewBroker(), slog.Default())
		svc.NotifyScanComplete(context.Background(), uuid.New(), "Movies", tc.count)
		if len(db.created) != 1 {
			t.Fatalf("%q/%d: created %d, want 1", tc.lang, tc.count, len(db.created))
		}
		if c := db.created[0]; c.Title != tc.title || c.Body != tc.body {
			t.Errorf("%q/%d: got %q / %q, want %q / %q", tc.lang, tc.count, c.Title, c.Body, tc.title, tc.body)
		}
	}
}

func TestNotifyNewContent_Translated(t *testing.T) {
	es, zh := uuid.New(), uuid.New()
	db := &mockDB{userIDs: []uuid.UUID{es, zh}, langs: map[uuid.UUID]string{es: "es", zh: "zh-Hans"}}
	svc := NewService(db, NewBroker(), slog.Default())
	svc.NotifyNewContent(context.Background(), "Inception", uuid.New())
	want := map[uuid.UUID]string{es: "Novedad: Inception", zh: "新内容：Inception"}
	for _, c := range db.created {
		if c.Title != want[c.UserID] {
			t.Errorf("title %q, want %q", c.Title, want[c.UserID])
		}
	}
}
