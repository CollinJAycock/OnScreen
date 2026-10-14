package digest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/userstats"
)

// ── Token ────────────────────────────────────────────────────────────────────

func TestSigner_RoundTripAndTamper(t *testing.T) {
	s := NewSigner("server-secret")
	id := uuid.New()
	tok := s.Token(id)
	got, ok := s.Verify(tok)
	if !ok || got != id {
		t.Fatalf("Verify(own token) = %v, %v", got, ok)
	}
	// Another server's secret can't verify it.
	if _, ok := NewSigner("other-secret").Verify(tok); ok {
		t.Error("a token verified under a different secret")
	}
	// Swapping in another user's id breaks the signature.
	raw := []byte(tok)
	other := s.Token(uuid.New())
	forged := other[:22] + string(raw[22:])
	if _, ok := s.Verify(forged); ok {
		t.Error("a token with a swapped user id verified")
	}
	for _, bad := range []string{"", "abc", tok + "x", strings.ToUpper(tok)} {
		if _, ok := s.Verify(bad); ok {
			t.Errorf("Verify(%q) passed", bad)
		}
	}
}

// ── Periods ──────────────────────────────────────────────────────────────────

func TestCurrentPeriod_Weekly(t *testing.T) {
	cfg := settings.DigestConfig{Weekday: 1, Hour: 8} // Monday 8am
	loc := time.UTC
	// Tuesday 2026-10-13 10:00 → scheduled Monday 2026-10-12 08:00, due.
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, loc)
	p, due := CurrentPeriod(FrequencyWeekly, cfg, now, loc)
	if !p.Scheduled.Equal(time.Date(2026, 10, 12, 8, 0, 0, 0, loc)) {
		t.Fatalf("scheduled = %v", p.Scheduled)
	}
	if !p.Since.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, loc)) || !p.Until.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, loc)) {
		t.Errorf("window = %v .. %v", p.Since, p.Until)
	}
	if p.Key != "2026-W42" || !due {
		t.Errorf("key %q due %v", p.Key, due)
	}
	// Monday 07:59 → still last week's moment, now outside the grace.
	p, due = CurrentPeriod(FrequencyWeekly, cfg, time.Date(2026, 10, 12, 7, 59, 0, 0, loc), loc)
	if !p.Scheduled.Equal(time.Date(2026, 10, 5, 8, 0, 0, 0, loc)) || due {
		t.Errorf("before the hour: scheduled %v due %v", p.Scheduled, due)
	}
	// Exactly at the moment.
	p, due = CurrentPeriod(FrequencyWeekly, cfg, time.Date(2026, 10, 12, 8, 0, 0, 0, loc), loc)
	if !p.Scheduled.Equal(time.Date(2026, 10, 12, 8, 0, 0, 0, loc)) || !due {
		t.Errorf("at the hour: scheduled %v due %v", p.Scheduled, due)
	}
}

func TestCurrentPeriod_Monthly(t *testing.T) {
	cfg := settings.DigestConfig{Weekday: 1, Hour: 9}
	loc := time.UTC
	p, due := CurrentPeriod(FrequencyMonthly, cfg, time.Date(2026, 10, 1, 9, 30, 0, 0, loc), loc)
	if p.Key != "2026-09" || !due || p.Label != "September 2026" {
		t.Errorf("key %q due %v label %q", p.Key, due, p.Label)
	}
	if !p.Since.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, loc)) || !p.Until.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, loc)) {
		t.Errorf("window = %v .. %v", p.Since, p.Until)
	}
	// Mid-month: the 1st is long past, not due (a user opting in on the 14th
	// waits for the next month).
	if _, due := CurrentPeriod(FrequencyMonthly, cfg, time.Date(2026, 10, 14, 9, 0, 0, 0, loc), loc); due {
		t.Error("mid-month must not be due")
	}
	// January rolls back to December.
	p, _ = CurrentPeriod(FrequencyMonthly, cfg, time.Date(2027, 1, 1, 10, 0, 0, 0, loc), loc)
	if p.Key != "2026-12" {
		t.Errorf("January key = %q", p.Key)
	}
	if _, due := CurrentPeriod(FrequencyOff, cfg, time.Now(), loc); due {
		t.Error("off is never due")
	}
}

// ── Rendering ────────────────────────────────────────────────────────────────

func TestRender_EscapesTitlesAndLinks(t *testing.T) {
	y := int32(2024)
	c := Content{
		Username: `<b>kid</b>`,
		Period:   Period{Label: "the week of Oct 5", Frequency: FrequencyWeekly},
		Sections: []Section{{Kind: "movie", Heading: "New movies", Entries: []Entry{
			{ID: uuid.New(), ItemType: "movie", Title: `<script>alert(1)</script>"&`, Year: &y, ContentID: "poster-1@onscreen"},
		}, More: 3}},
		BaseURL:        "https://media.example.com",
		UnsubscribeURL: "https://media.example.com/api/v1/digest/unsubscribe?token=a&b",
	}
	h := c.HTML()
	if strings.Contains(h, "<script>") || strings.Contains(h, "<b>kid") {
		t.Error("HTML contains an unescaped title or username")
	}
	if !strings.Contains(h, "&lt;script&gt;alert(1)&lt;/script&gt;&#34;&amp; (2024)") {
		t.Errorf("escaped title missing: %s", h)
	}
	if !strings.Contains(h, `src="cid:poster-1@onscreen"`) {
		t.Error("inline poster not referenced by cid")
	}
	if !strings.Contains(h, "token=a&amp;b") {
		t.Error("unsubscribe URL not attribute-escaped")
	}
	if strings.Contains(h, "/artwork/") || strings.Contains(h, "?token=ey") {
		t.Error("HTML points at server artwork")
	}
	if !strings.Contains(h, "…and 3 more") {
		t.Error("'and more' line missing")
	}
	txt := c.Text()
	if !strings.Contains(txt, "Unsubscribe: https://media.example.com/api/v1/digest/unsubscribe") {
		t.Errorf("text body missing unsubscribe: %s", txt)
	}
}

func TestCleanBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://media.example.com/": "https://media.example.com",
		"http://10.0.0.5:7070":       "http://10.0.0.5:7070",
		"javascript:alert(1)":        "",
		"media.example.com":          "",
		"":                           "",
	}
	for in, want := range cases {
		if got := cleanBaseURL(in); got != want {
			t.Errorf("cleanBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanRel(t *testing.T) {
	ok := []string{"Movies/Heat (1995)/poster.jpg", "/Shows/X/poster.png"}
	bad := []string{"", "../etc/passwd.jpg", "Movies/../../x.jpg", "Movies/Heat/Heat.mkv", "https://image.tmdb.org/p.jpg"}
	for _, p := range ok {
		if _, good := cleanRel(p); !good {
			t.Errorf("cleanRel(%q) refused", p)
		}
	}
	for _, p := range bad {
		if _, good := cleanRel(p); good {
			t.Errorf("cleanRel(%q) accepted", p)
		}
	}
}

// ── Sending ──────────────────────────────────────────────────────────────────

type fakeStore struct {
	recipients []gen.ListDigestRecipientsRow
	me         gen.GetDigestRecipientRow
	items      []gen.ListDigestNewItemsRow
	cw         []gen.ListContinueWatchingRow

	claimed  map[uuid.UUID]string // user → claimed period
	released []uuid.UUID
	gotItems gen.ListDigestNewItemsParams
}

func (f *fakeStore) ListDigestRecipients(context.Context) ([]gen.ListDigestRecipientsRow, error) {
	return f.recipients, nil
}
func (f *fakeStore) GetDigestRecipient(context.Context, uuid.UUID) (gen.GetDigestRecipientRow, error) {
	return f.me, nil
}
func (f *fakeStore) ClaimDigestPeriod(_ context.Context, a gen.ClaimDigestPeriodParams) (int64, error) {
	if f.claimed == nil {
		f.claimed = map[uuid.UUID]string{}
	}
	if f.claimed[a.UserID] == a.Period {
		return 0, nil
	}
	f.claimed[a.UserID] = a.Period
	return 1, nil
}
func (f *fakeStore) ReleaseDigestPeriod(_ context.Context, a gen.ReleaseDigestPeriodParams) error {
	f.released = append(f.released, a.UserID)
	delete(f.claimed, a.UserID)
	return nil
}
func (f *fakeStore) ListDigestNewItems(_ context.Context, a gen.ListDigestNewItemsParams) ([]gen.ListDigestNewItemsRow, error) {
	f.gotItems = a
	return f.items, nil
}
func (f *fakeStore) ListContinueWatching(context.Context, gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error) {
	return f.cw, nil
}

type fakeAccess struct{ libs map[uuid.UUID]struct{} }

func (a fakeAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	return a.libs, nil
}

type fakeMailer struct {
	enabled bool
	err     error
	sent    []email.Message
}

func (m *fakeMailer) Enabled(context.Context) bool { return m.enabled }
func (m *fakeMailer) SendMessage(_ context.Context, msg email.Message) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

type fakeStats struct{ totals userstats.Totals }

func (s fakeStats) TotalsFor(context.Context, userstats.Viewer, userstats.Range, string) (userstats.Totals, error) {
	return s.totals, nil
}

type fakePosters struct{ calls int }

func (p *fakePosters) Poster(context.Context, uuid.UUID, string) ([]byte, error) {
	p.calls++
	return []byte{0xff, 0xd8, 0xff}, nil
}

func newTestService(store *fakeStore, mailer *fakeMailer, cfg settings.DigestConfig, now time.Time, libs map[uuid.UUID]struct{}) *Service {
	s := New(Deps{
		Store:    store,
		Access:   fakeAccess{libs: libs},
		Stats:    fakeStats{totals: userstats.Totals{Plays: 4, HoursWatched: 3.5}},
		Posters:  &fakePosters{},
		Mailer:   mailer,
		Signer:   NewSigner("k"),
		Config:   func(context.Context) settings.DigestConfig { return cfg },
		BaseURL:  func(context.Context) string { return "https://media.example.com/" },
		Location: time.UTC,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s.now = func() time.Time { return now }
	s.pace = 0
	return s
}

func strp(s string) *string { return &s }

func TestSendDue(t *testing.T) {
	lib := uuid.New()
	cfg := settings.DigestConfig{Enabled: true, Weekday: 1, Hour: 8, IncludeContinueWatching: true, IncludeStats: true}
	now := time.Date(2026, 10, 12, 8, 19, 0, 0, time.UTC) // Monday 08:19
	due := gen.ListDigestRecipientsRow{ID: uuid.New(), Username: "ana", Email: "ana@example.com", Frequency: FrequencyWeekly,
		MaxContentRating: strp("PG"), BlockedLabels: []string{"horror"}}
	already := gen.ListDigestRecipientsRow{ID: uuid.New(), Username: "bo", Email: "bo@example.com", Frequency: FrequencyWeekly, LastPeriod: strp("2026-W42")}
	monthly := gen.ListDigestRecipientsRow{ID: uuid.New(), Username: "cy", Email: "cy@example.com", Frequency: FrequencyMonthly}
	store := &fakeStore{
		recipients: []gen.ListDigestRecipientsRow{due, already, monthly},
		items: []gen.ListDigestNewItemsRow{
			{Kind: "movie", ItemType: "movie", RefID: uuid.New(), LibraryID: lib, Title: "Heat", PosterPath: strp("Movies/Heat/poster.jpg"), ItemCount: 1, KindTotal: 1},
			{Kind: "show", ItemType: "show", RefID: uuid.New(), LibraryID: lib, Title: "Severance", ItemCount: 3, KindTotal: 1},
		},
		cw: []gen.ListContinueWatchingRow{
			{ID: uuid.New(), LibraryID: lib, Type: "movie", Title: "Visible"},
			{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Other library"},
		},
	}
	mailer := &fakeMailer{enabled: true}
	svc := newTestService(store, mailer, cfg, now, map[uuid.UUID]struct{}{lib: {}})

	summary, err := svc.SendDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d, want 1 (only the due weekly user): %s", len(mailer.sent), summary)
	}
	msg := mailer.sent[0]
	if msg.To[0] != "ana@example.com" {
		t.Errorf("to = %v", msg.To)
	}
	if !strings.HasPrefix(msg.Headers["List-Unsubscribe"], "<https://media.example.com/api/v1/digest/unsubscribe?token=") ||
		msg.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Errorf("headers = %v", msg.Headers)
	}
	if len(msg.Inline) != 1 || !strings.Contains(msg.HTML, "cid:"+msg.Inline[0].ContentID) {
		t.Errorf("inline posters = %d", len(msg.Inline))
	}
	if !strings.Contains(msg.HTML, "3 new episodes") || !strings.Contains(msg.HTML, "Visible") || strings.Contains(msg.HTML, "Other library") {
		t.Error("content: show grouping or continue-watching library filter wrong")
	}
	if !strings.Contains(msg.HTML, "3.5 hours watched") {
		t.Error("stats section missing")
	}
	// The recipient's gates reached the query.
	if store.gotItems.MaxRatingRank == nil || *store.gotItems.MaxRatingRank != 1 ||
		len(store.gotItems.BlockedLabels) != 1 || len(store.gotItems.LibraryIds) != 1 {
		t.Errorf("new-items gates = %+v", store.gotItems)
	}
	if store.claimed[due.ID] != "2026-W42" {
		t.Errorf("period not recorded: %v", store.claimed)
	}

	// A second run in the same hour sends nothing more (claimed).
	store.recipients[0].LastPeriod = strp("2026-W42")
	if _, err := svc.SendDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 {
		t.Errorf("second run re-sent: %d", len(mailer.sent))
	}
}

func TestSendDue_EmptyAndFailures(t *testing.T) {
	cfg := settings.DigestConfig{Enabled: true, Weekday: 1, Hour: 8}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	u := gen.ListDigestRecipientsRow{ID: uuid.New(), Username: "ana", Email: "ana@example.com", Frequency: FrequencyWeekly}

	// Nothing new: no email, period kept (not retried every hour).
	store := &fakeStore{recipients: []gen.ListDigestRecipientsRow{u}}
	mailer := &fakeMailer{enabled: true}
	if _, err := newTestService(store, mailer, cfg, now, nil).SendDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 0 || store.claimed[u.ID] != "2026-W42" || len(store.released) != 0 {
		t.Errorf("empty digest: sent %d claimed %v released %v", len(mailer.sent), store.claimed, store.released)
	}

	// A failed send hands the period back for the next run.
	store = &fakeStore{recipients: []gen.ListDigestRecipientsRow{u},
		items: []gen.ListDigestNewItemsRow{{Kind: "movie", ItemType: "movie", RefID: uuid.New(), Title: "Heat", KindTotal: 1}}}
	mailer = &fakeMailer{enabled: true, err: errors.New("relay down")}
	if _, err := newTestService(store, mailer, cfg, now, nil).SendDue(context.Background()); err == nil {
		t.Error("an all-failed run should report an error")
	}
	if len(store.released) != 1 {
		t.Errorf("failed send not released: %v", store.released)
	}

	// Off server-wide, or no SMTP: nothing is even listed.
	for _, tc := range []struct {
		cfg  settings.DigestConfig
		smtp bool
	}{{settings.DigestConfig{Enabled: false}, true}, {cfg, false}} {
		store := &fakeStore{recipients: []gen.ListDigestRecipientsRow{u}}
		mailer := &fakeMailer{enabled: tc.smtp}
		if _, err := newTestService(store, mailer, tc.cfg, now, nil).SendDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(mailer.sent) != 0 || len(store.claimed) != 0 {
			t.Errorf("cfg %+v smtp %v: sent or claimed", tc.cfg, tc.smtp)
		}
	}
}

func TestSendTest(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	store := &fakeStore{me: gen.GetDigestRecipientRow{ID: uuid.New(), Username: "admin", Email: "admin@example.com", IsAdmin: true, Frequency: FrequencyOff}}
	mailer := &fakeMailer{enabled: true}
	svc := newTestService(store, mailer, settings.DefaultDigestConfig(), now, nil)
	to, err := svc.SendTest(context.Background(), store.me.ID)
	if err != nil || to != "admin@example.com" {
		t.Fatalf("SendTest = %q, %v", to, err)
	}
	// Sent even though nothing is new and the digest is off; period untouched.
	if len(mailer.sent) != 1 || !strings.HasPrefix(mailer.sent[0].Subject, "[Test]") || len(store.claimed) != 0 {
		t.Errorf("test send: %d sent, claimed %v", len(mailer.sent), store.claimed)
	}
	if !strings.Contains(mailer.sent[0].Text, "Nothing new") {
		t.Error("empty test digest should say nothing new")
	}

	store.me.Email = ""
	if _, err := svc.SendTest(context.Background(), store.me.ID); !errors.Is(err, ErrNoEmail) {
		t.Errorf("no email err = %v", err)
	}
	mailer.enabled = false
	if _, err := svc.SendTest(context.Background(), store.me.ID); !errors.Is(err, ErrSMTPOff) {
		t.Errorf("smtp off err = %v", err)
	}
}

type fakeResizer struct{ got string }

func (r *fakeResizer) Resize(_ context.Context, w io.Writer, src string, _, _ int) error {
	r.got = src
	_, err := io.Copy(w, bytes.NewReader([]byte("jpeg")))
	return err
}

func TestArtworkPosters(t *testing.T) {
	lib := uuid.New()
	rz := &fakeResizer{}
	p := ArtworkPosters{
		ScanPaths: func(context.Context, uuid.UUID) []string { return []string{"/nope", "/media/movies"} },
		Exists:    func(_ context.Context, abs string) bool { return strings.Contains(abs, "media") },
		Resizer:   rz,
	}
	data, err := p.Poster(context.Background(), lib, "Heat (1995)/poster.jpg")
	if err != nil || string(data) != "jpeg" {
		t.Fatalf("Poster = %q, %v", data, err)
	}
	if !strings.Contains(strings.ReplaceAll(rz.got, `\`, "/"), "/media/movies/Heat (1995)/poster.jpg") {
		t.Errorf("resized %q", rz.got)
	}
	if _, err := p.Poster(context.Background(), lib, "../../etc/shadow.jpg"); err == nil {
		t.Error("escaping path accepted")
	}
}

