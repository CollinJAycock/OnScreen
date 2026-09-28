package requests

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

const day = 24 * time.Hour

// ── notification types, end to end ──────────────────────────────────────────

// Every requester-facing transition sends a declared notification type — the
// old CHECK constraint silently dropped all of these — and a queued request
// alerts the admins.
func TestNotifications_RequestLifecycleTypes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	alice := h.user(gen.GetUserRequestPermissionsRow{Username: "alice"})

	// Queued → request_created to the requester, request_pending to admins.
	queued, err := h.svc.Create(ctx, CreateInput{UserID: alice, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := h.notify.sent; len(got) != 1 || got[0].typ != notification.TypeRequestCreated || h.notify.to[0] != alice {
		t.Fatalf("requester notices = %+v to %v, want one request_created to alice", got, h.notify.to)
	}
	wantAdmin := notice{notification.TypeRequestPending, "New request", `alice requested "Heat (1995)"`}
	if len(h.notify.adminSent) != 1 || h.notify.adminSent[0] != wantAdmin {
		t.Fatalf("admin notices = %+v, want [%+v]", h.notify.adminSent, wantAdmin)
	}

	// Admin approval → request_approved.
	if _, err := h.svc.Approve(ctx, ApproveInput{RequestID: queued.ID, AdminID: uuid.New()}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if last := h.notify.sent[len(h.notify.sent)-1]; last.typ != notification.TypeRequestApproved {
		t.Errorf("after approve: last notice %+v, want request_approved", last)
	}

	// Fulfilment → request_available, carrying the item so the UI can link it.
	item := uuid.New()
	h.svc.MarkFulfilled(ctx, TypeMovie, 949, item)
	last := len(h.notify.sent) - 1
	if h.notify.sent[last].typ != notification.TypeRequestAvailable || h.notify.items[last] == nil || *h.notify.items[last] != item {
		t.Errorf("after fulfil: last notice %+v item %v, want request_available for %s", h.notify.sent[last], h.notify.items[last], item)
	}

	// Decline → request_declined.
	show, err := h.svc.Create(ctx, CreateInput{UserID: alice, Type: TypeShow, TMDBID: 95396})
	if err != nil {
		t.Fatalf("Create show: %v", err)
	}
	if _, err := h.svc.Decline(ctx, show.ID, uuid.New(), "no space"); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if last := h.notify.sent[len(h.notify.sent)-1]; last.typ != notification.TypeRequestDeclined || !strings.Contains(last.body, "no space") {
		t.Errorf("after decline: last notice %+v, want request_declined with the reason", last)
	}

	// Every type used is declared and satisfies the DB format.
	for _, n := range append(append([]notice{}, h.notify.sent...), h.notify.adminSent...) {
		if !notification.IsKnownType(n.typ) || !notification.TypePattern.MatchString(n.typ) {
			t.Errorf("notice type %q is not a declared notification type", n.typ)
		}
	}
}

// An auto-approved request tells the requester (request_approved) and does
// NOT alert the admins — there's nothing for them to do.
func TestNotifications_AutoApprovedSkipsAdminAlert(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true, Username: "bob"})
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil || !got.AutoApproved {
		t.Fatalf("Create = %+v, %v; want auto-approved", got, err)
	}
	if len(h.notify.sent) != 1 || h.notify.sent[0].typ != notification.TypeRequestApproved {
		t.Errorf("requester notices = %+v, want one request_approved", h.notify.sent)
	}
	if len(h.notify.adminSent) != 0 {
		t.Errorf("admin notices = %+v, want none for an auto-approved request", h.notify.adminSent)
	}
}

// A failed auto-approval leaves the request pending, so the admins are told.
func TestNotifications_FailedAutoApproveAlertsAdmins(t *testing.T) {
	h := newHarness(t)
	h.arr.fail = true
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true, Username: "bob"})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(h.notify.adminSent) != 1 || h.notify.adminSent[0].typ != notification.TypeRequestPending {
		t.Errorf("admin notices = %+v, want one request_pending", h.notify.adminSent)
	}
}

// Without a policy row the admin alert still goes out, just without a name.
func TestNotifications_AdminAlertWithoutUsername(t *testing.T) {
	h := newHarness(t)
	h.db.permsErr = errors.New("db down")
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uuid.New(), Type: TypeShow, TMDBID: 95396}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := notice{notification.TypeRequestPending, "New request", `A user requested "Severance (2022)"`}
	if len(h.notify.adminSent) != 1 || h.notify.adminSent[0] != want {
		t.Errorf("admin notices = %+v, want [%+v]", h.notify.adminSent, want)
	}
}

// ── can_request ─────────────────────────────────────────────────────────────

func TestCreate_RequestsDisabled(t *testing.T) {
	h := newHarness(t)
	blocked := h.userRow(gen.GetUserRequestPermissionsRow{CanRequest: false, AutoApproveMovies: true})
	_, err := h.svc.Create(context.Background(), CreateInput{UserID: blocked, Type: TypeMovie, TMDBID: 949})
	if !errors.Is(err, ErrRequestsDisabled) {
		t.Fatalf("err = %v, want ErrRequestsDisabled", err)
	}
	if len(h.db.reqs) != 0 {
		t.Errorf("a blocked user's request was stored: %+v", h.db.reqs)
	}
	if len(h.notify.sent)+len(h.notify.adminSent) != 0 {
		t.Errorf("notices sent for a refused request: %+v %+v", h.notify.sent, h.notify.adminSent)
	}

	// An admin is never blocked, whatever the column says.
	admin := h.userRow(gen.GetUserRequestPermissionsRow{IsAdmin: true, CanRequest: false})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: admin, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Errorf("admin with can_request=false: %v, want allowed", err)
	}
}

// ── quotas ──────────────────────────────────────────────────────────────────

func TestCreate_QuotaWindowBoundary(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 2, WindowDays: 7}
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true})

	// Exactly at the window start and older: aged out. One minute inside: counts.
	h.db.seed(uid, TypeMovie, StatusAvailable, h.now.Add(-7*day))
	h.db.seed(uid, TypeMovie, StatusAvailable, h.now.Add(-8*day))
	h.db.seed(uid, TypeMovie, StatusAvailable, h.now.Add(-7*day+time.Minute))

	// used 1 of 2 → within quota, auto-approved.
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OverQuota || !got.AutoApproved {
		t.Fatalf("second request in window: over=%v auto=%v, want false/true", got.OverQuota, got.AutoApproved)
	}
	if want := h.now.Add(-7 * day); len(h.db.sinceSeen) == 0 || !h.db.sinceSeen[0].Equal(want) {
		t.Errorf("window start = %v, want %v", h.db.sinceSeen, want)
	}

	// used 2 of 2 → over quota: created, pending, not sent to the arr.
	adds := len(h.arr.adds)
	got, err = h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 950})
	if err != nil {
		t.Fatalf("Create over quota must still succeed: %v", err)
	}
	if !got.OverQuota || got.AutoApproved || got.Status != StatusPending {
		t.Errorf("over-quota request: over=%v auto=%v status=%q, want true/false/pending", got.OverQuota, got.AutoApproved, got.Status)
	}
	if _, ok := h.db.reqs[got.ID]; !ok {
		t.Error("over-quota request was not stored")
	}
	if len(h.arr.adds) != adds {
		t.Errorf("over-quota request was sent to the arr: %v", h.arr.adds[adds:])
	}
	last := h.notify.sent[len(h.notify.sent)-1]
	if last.typ != notification.TypeRequestCreated || !strings.Contains(last.body, "over your movie request limit") {
		t.Errorf("requester notice = %+v, want request_created mentioning the limit", last)
	}
	adminLast := h.notify.adminSent[len(h.notify.adminSent)-1]
	if !strings.Contains(adminLast.body, "over their movie quota") {
		t.Errorf("admin notice = %+v, want it to flag the quota", adminLast)
	}

	// A week later the window has moved past all of it.
	h.now = h.now.Add(8 * day)
	got, err = h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 951})
	if err != nil || got.OverQuota || !got.AutoApproved {
		t.Errorf("after the window moved: %+v, %v; want auto-approved within quota", got, err)
	}
}

func TestCreate_QuotaIgnoresDeclinedAndCancelled(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true, RequestQuotaMovies: ptr(int32(1))})
	// A declined request and a user-cancelled one (stored as declined) in the window.
	h.db.seed(uid, TypeMovie, StatusDeclined, h.now.Add(-time.Hour))
	h.db.seed(uid, TypeMovie, StatusDeclined, h.now.Add(-2*time.Hour))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OverQuota || !got.AutoApproved {
		t.Errorf("declined requests counted against the quota: over=%v auto=%v", got.OverQuota, got.AutoApproved)
	}
}

func TestCreate_QuotaPerType(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 1, TV: 1}
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveTv: true})
	h.db.seed(uid, TypeMovie, StatusPending, h.now.Add(-time.Hour))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 95396})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OverQuota || !got.AutoApproved {
		t.Errorf("a movie request counted against the TV quota: over=%v auto=%v", got.OverQuota, got.AutoApproved)
	}
}

func TestCreate_QuotaAdminExempt(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 1}
	admin := h.user(gen.GetUserRequestPermissionsRow{IsAdmin: true, RequestQuotaMovies: ptr(int32(1))})
	h.db.seed(admin, TypeMovie, StatusAvailable, h.now.Add(-time.Hour))
	h.db.seed(admin, TypeMovie, StatusAvailable, h.now.Add(-2*time.Hour))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: admin, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OverQuota || !got.AutoApproved {
		t.Errorf("admin: over=%v auto=%v, want exempt and auto-approved", got.OverQuota, got.AutoApproved)
	}
}

// The user's own quota wins over the server default — including 0, which
// means unlimited even when the default is 1.
func TestCreate_QuotaOverrideBeatsDefault(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 1}

	unlimited := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true, RequestQuotaMovies: ptr(int32(0))})
	h.db.seed(unlimited, TypeMovie, StatusAvailable, h.now.Add(-time.Hour))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: unlimited, Type: TypeMovie, TMDBID: 949})
	if err != nil || got.OverQuota {
		t.Errorf("override 0: over=%v err=%v, want unlimited", got.OverQuota, err)
	}

	defaulted := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true})
	h.db.seed(defaulted, TypeMovie, StatusAvailable, h.now.Add(-time.Hour))
	got, err = h.svc.Create(context.Background(), CreateInput{UserID: defaulted, Type: TypeMovie, TMDBID: 949})
	if err != nil || !got.OverQuota {
		t.Errorf("no override, default 1 already used: over=%v err=%v, want over quota", got.OverQuota, err)
	}
}

// A user without auto-approval who goes over still gets the flag, so the UI
// can explain why the request is waiting.
func TestCreate_OverQuotaFlagWithoutAutoApprove(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{RequestQuotaMovies: ptr(int32(1))})
	h.db.seed(uid, TypeMovie, StatusPending, h.now.Add(-time.Hour))
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil || !got.OverQuota || got.Status != StatusPending {
		t.Errorf("got over=%v status=%q err=%v, want over-quota pending", got.OverQuota, got.Status, err)
	}
}

// A failed count can't prove the user is within quota: no auto-approval, but
// no over-quota claim either.
func TestCreate_QuotaCountErrorQueuesWithoutFlag(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 5}
	h.db.countErr = errors.New("db down")
	uid := h.user(gen.GetUserRequestPermissionsRow{AutoApproveMovies: true})
	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OverQuota || got.AutoApproved || got.Status != StatusPending {
		t.Errorf("got over=%v auto=%v status=%q, want false/false/pending", got.OverQuota, got.AutoApproved, got.Status)
	}
}

// Unlimited quotas never hit the count query.
func TestCreate_UnlimitedSkipsCount(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(h.db.sinceSeen) != 0 {
		t.Errorf("count query ran %d times for an unlimited user", len(h.db.sinceSeen))
	}
}

// The 25-pending flood cap still applies on top of quotas.
func TestCreate_PendingFloodCapKept(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	h.db.countPending = maxPendingRequestsPerUser
	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949}); !errors.Is(err, ErrTooManyPending) {
		t.Errorf("err = %v, want ErrTooManyPending", err)
	}
}

// ── Quota() / CountPending ──────────────────────────────────────────────────

func TestQuota_Status(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 3, TV: 0, WindowDays: 30}
	uid := h.user(gen.GetUserRequestPermissionsRow{RequestQuotaTv: ptr(int32(1))})
	h.db.seed(uid, TypeMovie, StatusPending, h.now.Add(-time.Hour))
	h.db.seed(uid, TypeMovie, StatusDeclined, h.now.Add(-time.Hour))
	h.db.seed(uid, TypeShow, StatusAvailable, h.now.Add(-29*day))
	h.db.seed(uid, TypeShow, StatusAvailable, h.now.Add(-31*day)) // outside

	st, err := h.svc.Quota(context.Background(), uid)
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if !st.CanRequest || st.WindowDays != 30 {
		t.Errorf("can=%v window=%d, want true/30", st.CanRequest, st.WindowDays)
	}
	if st.Movies.Limit != 3 || st.Movies.Used != 1 || st.Movies.Remaining == nil || *st.Movies.Remaining != 2 {
		t.Errorf("movies = %+v (remaining %v), want limit 3 used 1 remaining 2", st.Movies, deref32(st.Movies.Remaining))
	}
	if st.TV.Limit != 1 || st.TV.Used != 1 || st.TV.Remaining == nil || *st.TV.Remaining != 0 {
		t.Errorf("tv = %+v (remaining %v), want limit 1 used 1 remaining 0", st.TV, deref32(st.TV.Remaining))
	}
}

func TestQuota_UnlimitedAdminAndBlocked(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{Movies: 2, TV: 2}

	admin := h.userRow(gen.GetUserRequestPermissionsRow{IsAdmin: true, CanRequest: false})
	st, err := h.svc.Quota(context.Background(), admin)
	if err != nil {
		t.Fatalf("Quota(admin): %v", err)
	}
	if !st.CanRequest || st.Movies.Limit != 0 || st.Movies.Remaining != nil || st.TV.Remaining != nil {
		t.Errorf("admin = %+v, want allowed and unlimited", st)
	}
	if st.WindowDays != DefaultQuotaWindowDays {
		t.Errorf("window = %d, want the %d-day default when unset", st.WindowDays, DefaultQuotaWindowDays)
	}

	blocked := h.userRow(gen.GetUserRequestPermissionsRow{CanRequest: false, RequestQuotaMovies: ptr(int32(0))})
	st, err = h.svc.Quota(context.Background(), blocked)
	if err != nil {
		t.Fatalf("Quota(blocked): %v", err)
	}
	if st.CanRequest {
		t.Error("blocked user reported can_request=true")
	}
	if st.Movies.Limit != 0 || st.Movies.Remaining != nil {
		t.Errorf("override 0 = %+v, want unlimited", st.Movies)
	}
	if st.TV.Limit != 2 || st.TV.Remaining == nil || *st.TV.Remaining != 2 {
		t.Errorf("tv = %+v, want the server default of 2", st.TV)
	}

	if _, err := h.svc.Quota(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: err = %v, want ErrNotFound", err)
	}
}

func TestCountPending(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	h.db.seed(uid, TypeMovie, StatusPending, h.now)
	h.db.seed(uid, TypeShow, StatusPending, h.now)
	h.db.seed(uid, TypeMovie, StatusDeclined, h.now)
	n, err := h.svc.CountPending(context.Background())
	if err != nil || n != 2 {
		t.Errorf("CountPending = %d, %v; want 2", n, err)
	}
}

func deref32(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
