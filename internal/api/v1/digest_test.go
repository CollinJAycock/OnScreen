package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/digest"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

type fakeDigestDB struct {
	prefs        map[uuid.UUID]gen.UserDigestPref
	emails       map[uuid.UUID]string
	unsubscribed []uuid.UUID
}

func (f *fakeDigestDB) GetDigestPrefs(_ context.Context, id uuid.UUID) (gen.UserDigestPref, error) {
	p, ok := f.prefs[id]
	if !ok {
		return gen.UserDigestPref{}, pgx.ErrNoRows
	}
	return p, nil
}
func (f *fakeDigestDB) SetDigestFrequency(_ context.Context, a gen.SetDigestFrequencyParams) (gen.UserDigestPref, error) {
	if f.prefs == nil {
		f.prefs = map[uuid.UUID]gen.UserDigestPref{}
	}
	p := gen.UserDigestPref{UserID: a.UserID, Frequency: a.Frequency}
	f.prefs[a.UserID] = p
	return p, nil
}
func (f *fakeDigestDB) UnsubscribeDigest(_ context.Context, id uuid.UUID) (int64, error) {
	f.unsubscribed = append(f.unsubscribed, id)
	return 1, nil
}
func (f *fakeDigestDB) GetDigestRecipient(_ context.Context, id uuid.UUID) (gen.GetDigestRecipientRow, error) {
	return gen.GetDigestRecipientRow{ID: id, Email: f.emails[id]}, nil
}

type fakeDigestSettings struct{ cfg settings.DigestConfig }

func (f *fakeDigestSettings) Digest(context.Context) settings.DigestConfig { return f.cfg }
func (f *fakeDigestSettings) SetDigest(_ context.Context, c settings.DigestConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	f.cfg = c
	return nil
}

type fakeDigestTest struct{ err error }

func (f fakeDigestTest) SendTest(context.Context, uuid.UUID) (string, error) {
	return "me@example.com", f.err
}

type smtpOn bool

func (s smtpOn) Enabled(context.Context) bool { return bool(s) }

func newDigestHandler(db *fakeDigestDB, st *fakeDigestSettings, smtp bool) *DigestHandler {
	return NewDigestHandler(db, st, fakeDigestTest{}, smtpOn(smtp), digest.NewSigner("secret"),
		func(context.Context) string { return "" }, slog.Default())
}

func TestDigest_Unsubscribe_GetConfirmsPostUnsubscribes(t *testing.T) {
	db := &fakeDigestDB{}
	h := newDigestHandler(db, &fakeDigestSettings{}, true)
	user := uuid.New()
	tok := digest.NewSigner("secret").Token(user)

	rec := httptest.NewRecorder()
	h.Unsubscribe(rec, httptest.NewRequest(http.MethodGet, "/api/v1/digest/unsubscribe?token="+tok, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `method="post"`) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if len(db.unsubscribed) != 0 {
		t.Fatal("GET must not unsubscribe (mail scanners fetch links)")
	}

	// RFC 8058 one-click: a form POST with no cookie or session.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/digest/unsubscribe?token="+tok, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h.Unsubscribe(rec, req)
	if rec.Code != http.StatusOK || len(db.unsubscribed) != 1 || db.unsubscribed[0] != user {
		t.Fatalf("POST: %d unsubscribed %v", rec.Code, db.unsubscribed)
	}

	// A token from another key, or a mangled one, does nothing.
	for _, bad := range []string{digest.NewSigner("other").Token(user), tok[:20], ""} {
		rec = httptest.NewRecorder()
		h.Unsubscribe(rec, httptest.NewRequest(http.MethodPost, "/api/v1/digest/unsubscribe?token="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("bad token %q: status %d", bad, rec.Code)
		}
	}
	if len(db.unsubscribed) != 1 {
		t.Errorf("bad tokens unsubscribed someone: %v", db.unsubscribed)
	}
}

func TestDigest_MyPrefs(t *testing.T) {
	user := uuid.New()
	db := &fakeDigestDB{emails: map[uuid.UUID]string{user: "a@example.com"}}
	st := &fakeDigestSettings{cfg: settings.DigestConfig{Enabled: true, Hour: 8, Weekday: 1}}
	h := newDigestHandler(db, st, true)
	ctx := middleware.WithClaims(context.Background(), &auth.Claims{UserID: user})

	rec := httptest.NewRecorder()
	h.GetMine(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	var got struct {
		Data myDigestDTO `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Data.Frequency != "off" || !got.Data.HasEmail || !got.Data.Available {
		t.Errorf("GET mine = %+v (%s)", got.Data, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.SetMine(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"frequency":"monthly"}`)).WithContext(ctx))
	if rec.Code != http.StatusOK || db.prefs[user].Frequency != "monthly" {
		t.Errorf("PUT monthly: %d %+v", rec.Code, db.prefs[user])
	}
	rec = httptest.NewRecorder()
	h.SetMine(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"frequency":"daily"}`)).WithContext(ctx))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT daily: %d", rec.Code)
	}

	// Unavailable while SMTP is off.
	h = newDigestHandler(db, st, false)
	rec = httptest.NewRecorder()
	h.GetMine(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Data.Available {
		t.Error("digest reported available without SMTP")
	}
}

func TestDigest_AdminSettings(t *testing.T) {
	st := &fakeDigestSettings{cfg: settings.DefaultDigestConfig()}
	h := newDigestHandler(&fakeDigestDB{}, st, false)
	rec := httptest.NewRecorder()
	h.GetSettings(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `"smtp_configured":false`) || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("GET settings = %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.UpdateSettings(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"enabled":true,"weekday":5,"hour":18,"include_stats":true}`)))
	if rec.Code != http.StatusOK || !st.cfg.Enabled || st.cfg.Weekday != 5 || st.cfg.Hour != 18 || st.cfg.IncludeContinueWatching {
		t.Errorf("PUT: %d %+v", rec.Code, st.cfg)
	}
	rec = httptest.NewRecorder()
	h.UpdateSettings(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"enabled":true,"weekday":9,"hour":8}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad weekday: %d", rec.Code)
	}
}

func TestDigest_SendTestErrors(t *testing.T) {
	ctx := middleware.WithClaims(context.Background(), &auth.Claims{UserID: uuid.New(), IsAdmin: true})
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, http.StatusOK},
		{digest.ErrNoEmail, http.StatusBadRequest},
		{digest.ErrSMTPOff, http.StatusBadRequest},
		{errors.New("relay refused"), http.StatusBadRequest},
	} {
		h := NewDigestHandler(&fakeDigestDB{}, &fakeDigestSettings{}, fakeDigestTest{err: tc.err}, smtpOn(true),
			digest.NewSigner("s"), nil, slog.Default())
		rec := httptest.NewRecorder()
		h.SendTest(rec, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx))
		if rec.Code != tc.want {
			t.Errorf("err %v: status %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}
