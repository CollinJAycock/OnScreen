package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

// zonedWatchLimit is a watch-limit store with a household clock, as
// *watchlimit.Store has once System ▸ Time zone is wired (Store.Now).
type zonedWatchLimit struct {
	now    time.Time
	policy watchlimit.Policy

	mu        sync.Mutex
	usageDays []time.Time
	tickDays  []time.Time
}

func (z *zonedWatchLimit) Now(context.Context) time.Time { return z.now }
func (z *zonedWatchLimit) GetPolicy(context.Context, uuid.UUID) (watchlimit.Policy, error) {
	return z.policy, nil
}
func (z *zonedWatchLimit) TodayUsageSeconds(_ context.Context, _ uuid.UUID, day time.Time) (int, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.usageDays = append(z.usageDays, day)
	return 0, nil
}
func (z *zonedWatchLimit) AddTick(_ context.Context, _ uuid.UUID, day, _ time.Time) (int, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.tickDays = append(z.tickDays, day)
	return 0, nil
}

// The parental gates evaluated time.Now() — the process zone, UTC in the
// Docker images — so a household eight hours from UTC had its allowed hours
// and its daily reset eight hours off. The gate and the accrual take "now"
// from the store's household clock, through the memo the handlers hold.
func TestWatchLimitGate_EvaluatesInTheHouseholdZone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	// 19:00 UTC on Jan 1 — inside 08:00–20:00 by the process clock in UTC —
	// is 04:00 on Jan 2 in Tokyo, outside it.
	store := &zonedWatchLimit{
		now:    time.Date(2026, 1, 1, 19, 0, 0, 0, time.UTC).In(tokyo),
		policy: watchlimit.Policy{AllowedStartMinute: zoneIntPtr(8 * 60), AllowedEndMinute: zoneIntPtr(20 * 60)},
	}
	wl := newWatchLimitMemo(store)

	rec := httptest.NewRecorder()
	if !watchLimitBlocks(rec, httptest.NewRequest(http.MethodGet, "/", nil), wl, nil, uuid.New()) {
		t.Fatal("04:00 in the household zone was let through an 08:00–20:00 window")
	}
	if !strings.Contains(rec.Body.String(), watchlimit.ReasonOutsideHours) {
		t.Errorf("body = %s, want %s", rec.Body.String(), watchlimit.ReasonOutsideHours)
	}

	// The daily budget is the household's calendar day.
	store.policy = watchlimit.Policy{DailyLimitMinutes: zoneIntPtr(60)}
	wl = newWatchLimitMemo(store)
	if watchLimitBlocks(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), wl, nil, uuid.New()) {
		t.Fatal("unused daily budget blocked")
	}
	newUsageAccruer(wl).Tick(context.Background(), nil, uuid.New())
	for _, days := range [][]time.Time{store.usageDays, store.tickDays} {
		if len(days) == 0 || days[len(days)-1].Day() != 2 || days[len(days)-1].Location() != tokyo {
			t.Errorf("usage day = %v, want Jan 2 in Tokyo", days)
		}
	}
}

func zoneIntPtr(i int) *int { return &i }

// System ▸ Time zone round-trips through the settings API, refuses names the
// server can't load, and is kept by a PUT from a client that doesn't send it.
func TestSystemSettings_TimeZone(t *testing.T) {
	svc := &mockSettingsService{}
	h := sysHandler(svc, settings.SystemConfig{})
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.UpdateSystem(rec, httptest.NewRequest(http.MethodPut, "/settings/system", strings.NewReader(body)))
		return rec
	}

	if dto := getSystemDTO(t, h); dto.TimeZone == nil || *dto.TimeZone != "" {
		t.Errorf("unset time_zone = %v, want \"\"", dto.TimeZone)
	}

	if rec := put(`{"server_name":"S","time_zone":" Europe/Berlin "}`); rec.Code != http.StatusNoContent {
		t.Fatalf("valid zone: %d %s", rec.Code, rec.Body.String())
	}
	if svc.system.TimeZone == nil || *svc.system.TimeZone != "Europe/Berlin" {
		t.Fatalf("stored time zone = %v, want Europe/Berlin", svc.system.TimeZone)
	}
	if dto := getSystemDTO(t, h); dto.TimeZone == nil || *dto.TimeZone != "Europe/Berlin" {
		t.Errorf("GET time_zone = %v, want Europe/Berlin", dto.TimeZone)
	}

	rec := put(`{"server_name":"S","time_zone":"Mars/Olympus_Mons"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown zone: %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Mars/Olympus_Mons") {
		t.Errorf("400 body doesn't name the zone: %s", rec.Body.String())
	}
	if *svc.system.TimeZone != "Europe/Berlin" {
		t.Error("a refused PUT changed the stored zone")
	}

	// A client that predates the field saves without it: the zone stays.
	if rec := put(`{"server_name":"Older client"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT without time_zone: %d", rec.Code)
	}
	if svc.system.TimeZone == nil || *svc.system.TimeZone != "Europe/Berlin" {
		t.Errorf("PUT without time_zone cleared it: %v", svc.system.TimeZone)
	}

	// "" clears it back to the process zone.
	if rec := put(`{"server_name":"S","time_zone":""}`); rec.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", rec.Code)
	}
	if svc.system.TimeZone != nil {
		t.Errorf("time zone after clearing = %v, want unset", *svc.system.TimeZone)
	}
}
