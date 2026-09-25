//go:build integration

package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestDVR_Integration_CountLiveSchedulesForUser pins what the per-user
// schedule cap counts: rules that can still produce a recording. Spent
// one-offs (programme aired, or trimmed from the EPG so program_id is NULL)
// and disabled rules are left in place but must not count — otherwise every
// guide "Record" click permanently eats one of the user's 100 slots.
func TestDVR_Integration_CountLiveSchedulesForUser(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-cnt-"+uuid.New().String()[:8])
	other := seedUser(ctx, t, q, "dvr-cnt-o-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-cnt-tuner-"+uuid.New().String()[:6])
	ch := pgtype.UUID{Bytes: chID, Valid: true}
	now := time.Now()
	future := seedEPGProgram(t, pool, chID, "cnt-future-"+uuid.New().String()[:6], now.Add(time.Hour), now.Add(2*time.Hour))
	airing := seedEPGProgram(t, pool, chID, "cnt-airing-"+uuid.New().String()[:6], now.Add(-10*time.Minute), now.Add(20*time.Minute))
	aired := seedEPGProgram(t, pool, chID, "cnt-aired-"+uuid.New().String()[:6], now.Add(-3*time.Hour), now.Add(-2*time.Hour))
	title := "News"
	blockStart, blockEnd := "20:00", "21:00"

	create := func(p gen.CreateScheduleParams, enabled bool) {
		t.Helper()
		row, err := q.CreateSchedule(ctx, p)
		if err != nil {
			t.Fatalf("CreateSchedule(%s): %v", p.Type, err)
		}
		if !enabled {
			if err := q.SetScheduleEnabled(ctx, gen.SetScheduleEnabledParams{ID: row.ID, Enabled: false}); err != nil {
				t.Fatalf("SetScheduleEnabled: %v", err)
			}
		}
	}
	once := func(u uuid.UUID, program *uuid.UUID) gen.CreateScheduleParams {
		p := gen.CreateScheduleParams{UserID: u, Type: "once"}
		if program != nil {
			p.ProgramID = pgtype.UUID{Bytes: *program, Valid: true}
		}
		return p
	}

	// Counted.
	create(gen.CreateScheduleParams{UserID: user, Type: "series", ChannelID: ch, TitleMatch: &title}, true)
	create(gen.CreateScheduleParams{UserID: user, Type: "channel_block", ChannelID: ch, TimeStart: &blockStart, TimeEnd: &blockEnd}, true)
	create(once(user, &future), true)
	create(once(user, &airing), true)
	// Not counted.
	create(gen.CreateScheduleParams{UserID: user, Type: "series", ChannelID: ch, TitleMatch: &title}, false)
	create(once(user, &future), false)
	create(once(user, &aired), true)
	create(once(user, nil), true) // programme trimmed from the EPG
	create(once(other, &future), true)

	got, err := q.CountLiveSchedulesForUser(ctx, user)
	if err != nil {
		t.Fatalf("CountLiveSchedulesForUser: %v", err)
	}
	if got != 4 {
		t.Errorf("live schedules = %d, want 4 (enabled series + channel_block + two unfinished one-offs)", got)
	}

	// The matcher's list applies the same rule: spent one-offs and disabled
	// rules are skipped (they'd otherwise accumulate under its LIMIT and push
	// live rules out), while live ones — including another user's — remain.
	enabled, err := q.ListEnabledSchedules(ctx)
	if err != nil {
		t.Fatalf("ListEnabledSchedules: %v", err)
	}
	mine := 0
	for _, s := range enabled {
		if s.UserID == user || s.UserID == other {
			mine++
		}
	}
	if mine != 5 {
		t.Errorf("matcher schedules for these users = %d, want 5 (4 live for user + 1 for other)", mine)
	}

	// Trimming the EPG (FK ON DELETE SET NULL) retires a one-off without
	// touching the schedule row.
	if _, err := pool.Exec(ctx, `DELETE FROM epg_programs WHERE id = $1`, future); err != nil {
		t.Fatalf("delete programme: %v", err)
	}
	if got, _ := q.CountLiveSchedulesForUser(ctx, user); got != 3 {
		t.Errorf("after the programme was trimmed: live schedules = %d, want 3", got)
	}
	rows, err := q.ListSchedulesForUser(ctx, user)
	if err != nil {
		t.Fatalf("ListSchedulesForUser: %v", err)
	}
	if len(rows) != 8 {
		t.Errorf("schedule rows = %d, want 8 — counting must never delete rows", len(rows))
	}
}
