package v1

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// A beat with neither a decision nor a duration needs the user's sessions for
// both, and used to list them once for each (on top of the store's own scan to
// write the position). It lists once.
func TestProgress_ListsSessionsOncePerBeat(t *testing.T) {
	itemID, fileID := uuid.New(), uuid.New()
	ms := &mockItemMedia{files: []media.File{
		{ID: uuid.New(), MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(5_400_000)},
		{ID: fileID, MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(6_600_000)},
	}}
	ws := &captureWatch{}
	sessions := &listingSessions{sessions: []transcode.Session{
		{ID: "s", Decision: "remux", FileID: fileID, MediaItemID: itemID, CreatedAt: time.Now()},
	}}
	h := NewItemHandler(ms, ws, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	runProgress(t, h, itemID, uuid.New(), `{"view_offset_ms":30000,"state":"playing"}`)
	if sessions.lists != 1 {
		t.Errorf("session lists for one beat: got %d, want 1", sessions.lists)
	}
	ev := ws.events[len(ws.events)-1]
	if ev.Decision == nil || *ev.Decision != "remux" {
		t.Errorf("decision = %v, want the session's remux", ev.Decision)
	}
	if ev.DurationMS == nil || *ev.DurationMS != 6_600_000 {
		t.Errorf("duration = %v, want the playing version's 6600000", ev.DurationMS)
	}
}

// beaconStore builds an ItemHandler over a real session store with the
// viewer's one session for the item among many other users' sessions.
func beaconStore(t *testing.T, others int) (*ItemHandler, *transcode.SessionStore, *valkeyTrips, *valkey.Client, uuid.UUID, uuid.UUID) {
	t.Helper()
	v := testvalkey.New(t)
	rt := &valkeyTrips{}
	v.Raw().AddHook(rt)
	store := transcode.NewSessionStore(v)
	ctx := context.Background()
	viewer, itemID := uuid.New(), uuid.New()
	for i := 0; i < others; i++ {
		_ = store.Create(ctx, transcode.Session{ID: uuid.NewString(), UserID: uuid.New(), MediaItemID: itemID, Decision: "transcode", CreatedAt: time.Now()})
	}
	if err := store.Create(ctx, transcode.Session{ID: "viewer-session", UserID: viewer, MediaItemID: itemID, Decision: "transcode", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	ms := &mockItemMedia{files: []media.File{{ID: uuid.New(), MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(5_400_000)}}}
	h := NewItemHandler(ms, &captureWatch{}, store, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())
	return h, store, rt, v, viewer, itemID
}

// Each beat read every session on the server up to three times. It now reads
// the viewer's own index once, whatever else is playing: two round trips to
// list, plus the position write (WATCH / GET / MULTI-EXEC).
func TestProgress_BeaconCostIndependentOfOtherViewers(t *testing.T) {
	for _, others := range []int{0, 50} {
		h, store, rt, _, viewer, itemID := beaconStore(t, others)
		runProgress(t, h, itemID, viewer, `{"view_offset_ms":1000,"state":"playing"}`) // the one-time backfill

		n := rt.during(func() {
			runProgress(t, h, itemID, viewer, `{"view_offset_ms":42000,"state":"playing"}`)
		})
		if n != 5 {
			t.Errorf("%d other sessions: a beat took %d Valkey round trips, want 5", others, n)
		}
		got, err := store.Get(context.Background(), "viewer-session")
		if err != nil || got.PositionMS != 42000 {
			t.Errorf("%d other sessions: position = %v (%v), want 42000", others, got, err)
		}
	}
}

// The stop beat deletes the viewer's sessions from the same list, in one
// pipeline, and only theirs.
func TestProgress_StopBeatDeletesFromTheOneList(t *testing.T) {
	h, store, rt, v, viewer, itemID := beaconStore(t, 5)
	runProgress(t, h, itemID, viewer, `{"view_offset_ms":1000,"state":"playing"}`)

	n := rt.during(func() {
		runProgress(t, h, itemID, viewer, `{"view_offset_ms":50000,"state":"stopped","decision":"transcode","duration_ms":100000}`)
	})
	if n != 3 {
		t.Errorf("stop beat: %d Valkey round trips, want 3 (index, sessions, one delete pipeline)", n)
	}
	if _, err := store.Get(context.Background(), "viewer-session"); err == nil {
		t.Error("viewer's session survived the stop beat")
	}
	all, _ := store.List(context.Background())
	if len(all) != 5 {
		t.Errorf("%d sessions left, want the 5 other viewers' untouched", len(all))
	}
	if v.Raw().SIsMember(context.Background(), "transcode:user_sessions:"+viewer.String(), "viewer-session").Val() {
		t.Error("stopped session left in the viewer's index")
	}
}
