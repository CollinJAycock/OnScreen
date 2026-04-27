//go:build integration

// Round-trips the DVR queries (schedules + recordings). Recording is
// the most consequential write path in DVR — a bug here either fails
// to start the capture or loses a finished recording's link to its
// media_items row.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// seedTunerChannel creates a minimal tuner + channel pair and returns
// the channel ID — recordings carry channel_id as a FK.
func seedTunerChannel(t *testing.T, q *gen.Queries, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tuner, err := q.CreateTunerDevice(ctx, gen.CreateTunerDeviceParams{
		Type: "m3u", Name: name, Config: []byte(`{}`), TuneCount: 2,
	})
	if err != nil {
		t.Fatalf("CreateTunerDevice: %v", err)
	}
	ch, err := q.UpsertChannel(ctx, gen.UpsertChannelParams{
		TunerID: tuner.ID,
		Number:  "1",
		Name:    "Test Channel",
	})
	if err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	return ch.ID
}

// seedEPGProgram inserts a single EPG row on the given channel and
// returns its ID. recordings.program_id has a FK on epg_programs(id);
// without a real row the upsert fails on insert.
//
// The generated UpsertEPGProgram returns only error (not the row ID), so
// we hit the table directly with INSERT...RETURNING id.
func seedEPGProgram(t *testing.T, pool *pgxpool.Pool, channelID uuid.UUID, sourceID string, starts, ends time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO epg_programs (channel_id, source_program_id, title, starts_at, ends_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, channelID, sourceID, "Test Program", starts, ends).Scan(&id)
	if err != nil {
		t.Fatalf("seedEPGProgram: %v", err)
	}
	return id
}

// TestDVR_Integration_ScheduleCRUDRoundTrip — every field set on
// Create comes back unchanged on Get, and the disabled flag flips
// via SetScheduleEnabled.
func TestDVR_Integration_ScheduleCRUDRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-sch-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-sch-tuner-"+uuid.New().String()[:6])

	titleMatch := "Saturday Night Live"
	retention := int32(30)
	created, err := q.CreateSchedule(ctx, gen.CreateScheduleParams{
		UserID:         user,
		Type:           "series",
		ChannelID:      pgtype.UUID{Bytes: chID, Valid: true},
		TitleMatch:     &titleMatch,
		NewOnly:        true,
		PaddingPreSec:  60,
		PaddingPostSec: 120,
		Priority:       5,
		RetentionDays:  &retention,
	})
	if err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}

	got, err := q.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if got.Type != "series" {
		t.Errorf("type = %q, want series", got.Type)
	}
	if got.TitleMatch == nil || *got.TitleMatch != titleMatch {
		t.Errorf("title_match round-trip mismatch")
	}
	if !got.NewOnly {
		t.Error("new_only flag lost on round-trip")
	}
	if got.PaddingPreSec != 60 || got.PaddingPostSec != 120 {
		t.Errorf("padding lost: pre=%d post=%d, want 60/120", got.PaddingPreSec, got.PaddingPostSec)
	}
	if !got.Enabled {
		t.Error("schedule defaulted to disabled — should default to enabled per migration")
	}

	// Disable it.
	if err := q.SetScheduleEnabled(ctx, gen.SetScheduleEnabledParams{
		ID: created.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}
	got2, _ := q.GetSchedule(ctx, created.ID)
	if got2.Enabled {
		t.Error("Enabled flag did not flip to false")
	}
}

// TestDVR_Integration_ListEnabledSchedulesSkipsDisabled — the matcher
// loop iterates this query every minute. Disabled schedules MUST stay
// out of the result set.
func TestDVR_Integration_ListEnabledSchedulesSkipsDisabled(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-le-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-le-tuner-"+uuid.New().String()[:6])

	on, err := q.CreateSchedule(ctx, gen.CreateScheduleParams{
		UserID: user, Type: "channel_block", ChannelID: pgtype.UUID{Bytes: chID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSchedule (on): %v", err)
	}
	off, err := q.CreateSchedule(ctx, gen.CreateScheduleParams{
		UserID: user, Type: "channel_block", ChannelID: pgtype.UUID{Bytes: chID, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSchedule (off): %v", err)
	}
	if err := q.SetScheduleEnabled(ctx, gen.SetScheduleEnabledParams{ID: off.ID, Enabled: false}); err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}

	rows, err := q.ListEnabledSchedules(ctx)
	if err != nil {
		t.Fatalf("ListEnabledSchedules: %v", err)
	}
	var sawOn, sawOff bool
	for _, r := range rows {
		if r.ID == on.ID {
			sawOn = true
		}
		if r.ID == off.ID {
			sawOff = true
		}
	}
	if !sawOn {
		t.Error("enabled schedule missing from list")
	}
	if sawOff {
		t.Error("disabled schedule leaked into matcher list")
	}
}

// TestDVR_Integration_UpsertRecordingIsIdempotent — re-running the
// matcher must not create duplicate recording rows for the same
// (user, program). Re-upserting a 'scheduled' row updates metadata;
// re-upserting after the row hits 'recording' or 'completed' is a
// no-op (the matcher must not re-arm captures it already started).
func TestDVR_Integration_UpsertRecordingIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-up-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-up-tuner-"+uuid.New().String()[:6])

	starts := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	ends := pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true}
	progID := seedEPGProgram(t, pool, chID, "EP-up-"+uuid.New().String()[:6], starts.Time, ends.Time)
	programID := pgtype.UUID{Bytes: progID, Valid: true}
	r1, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID, ProgramID: programID,
		Title: "v1", StartsAt: starts, EndsAt: ends,
	})
	if err != nil {
		t.Fatalf("UpsertRecording (first): %v", err)
	}

	// Same (user, program) — should return the same ID with updated
	// title rather than insert a duplicate row.
	r2, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID, ProgramID: programID,
		Title: "v2", StartsAt: starts, EndsAt: ends,
	})
	if err != nil {
		t.Fatalf("UpsertRecording (second): %v", err)
	}
	if r1.ID != r2.ID {
		t.Errorf("re-upsert created a new row: %s vs %s", r1.ID, r2.ID)
	}
	if r2.Title != "v2" {
		t.Errorf("title not updated on re-upsert: got %q, want v2", r2.Title)
	}
}

// TestDVR_Integration_RecordingStatusTransitions — covers the
// state-machine path the worker takes:
// scheduled → recording → completed (with item_id).
func TestDVR_Integration_RecordingStatusTransitions(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-trans-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-trans-tuner-"+uuid.New().String()[:6])
	starts := time.Now()
	ends := starts.Add(time.Hour)
	progID := seedEPGProgram(t, pool, chID, "EP-trans-"+uuid.New().String()[:6], starts, ends)
	r, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID,
		ProgramID: pgtype.UUID{Bytes: progID, Valid: true},
		Title:     "Show",
		StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
		EndsAt:    pgtype.Timestamptz{Time: ends, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "scheduled" {
		t.Fatalf("initial status = %q, want scheduled", r.Status)
	}

	filePath := "/dvr/show.ts"
	if err := q.SetRecordingStartedFile(ctx, gen.SetRecordingStartedFileParams{
		ID: r.ID, FilePath: &filePath,
	}); err != nil {
		t.Fatalf("SetRecordingStartedFile: %v", err)
	}
	r2, _ := q.GetRecording(ctx, r.ID)
	if r2.Status != "recording" {
		t.Errorf("after start: status = %q, want recording", r2.Status)
	}
	if r2.FilePath == nil || *r2.FilePath != filePath {
		t.Errorf("file_path not set: %v", r2.FilePath)
	}

	// ListActiveRecordings must include this row now.
	active, _ := q.ListActiveRecordings(ctx)
	var sawActive bool
	for _, a := range active {
		if a.ID == r.ID {
			sawActive = true
		}
	}
	if !sawActive {
		t.Error("recording missing from ListActiveRecordings while in 'recording' state")
	}

	// Complete with media_items link. recordings.item_id has a FK on
	// media_items so we need a real row, not a synthetic uuid.
	lib := seedLibrary(ctx, t, q, "dvr-trans-lib-"+uuid.New().String()[:8])
	mediaID := seedMediaItem(ctx, t, q, lib, "Recorded Show")
	itemID := pgtype.UUID{Bytes: mediaID, Valid: true}
	if err := q.SetRecordingCompleted(ctx, gen.SetRecordingCompletedParams{
		ID: r.ID, ItemID: itemID,
	}); err != nil {
		t.Fatalf("SetRecordingCompleted: %v", err)
	}
	r3, _ := q.GetRecording(ctx, r.ID)
	if r3.Status != "completed" {
		t.Errorf("after complete: status = %q, want completed", r3.Status)
	}
	if !r3.ItemID.Valid {
		t.Error("item_id not linked on completion — completed recording can't be played")
	}

	// Should no longer be in active list.
	active2, _ := q.ListActiveRecordings(ctx)
	for _, a := range active2 {
		if a.ID == r.ID {
			t.Error("completed recording still in ListActiveRecordings")
		}
	}
}

// TestDVR_Integration_ListDueRecordingsTimeBound — only rows whose
// starts_at is at or before the cutoff appear.
func TestDVR_Integration_ListDueRecordingsTimeBound(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-due-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-due-tuner-"+uuid.New().String()[:6])

	// Soon — should appear at the 1h cutoff.
	soonStart := time.Now().Add(10 * time.Minute)
	soonEnd := time.Now().Add(time.Hour)
	soonProg := seedEPGProgram(t, pool, chID, "EP-soon-"+uuid.New().String()[:6], soonStart, soonEnd)
	soon, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID,
		ProgramID: pgtype.UUID{Bytes: soonProg, Valid: true},
		Title:     "Soon",
		StartsAt:  pgtype.Timestamptz{Time: soonStart, Valid: true},
		EndsAt:    pgtype.Timestamptz{Time: soonEnd, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertRecording (soon): %v", err)
	}
	// Later — should NOT appear.
	laterStart := time.Now().Add(3 * time.Hour)
	laterEnd := time.Now().Add(4 * time.Hour)
	laterProg := seedEPGProgram(t, pool, chID, "EP-later-"+uuid.New().String()[:6], laterStart, laterEnd)
	later, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		UserID: user, ChannelID: chID,
		ProgramID: pgtype.UUID{Bytes: laterProg, Valid: true},
		Title:     "Later",
		StartsAt:  pgtype.Timestamptz{Time: laterStart, Valid: true},
		EndsAt:    pgtype.Timestamptz{Time: laterEnd, Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertRecording (later): %v", err)
	}

	due, err := q.ListDueRecordings(ctx, pgtype.Timestamptz{
		Time: time.Now().Add(time.Hour), Valid: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawSoon, sawLater bool
	for _, d := range due {
		if d.ID == soon.ID {
			sawSoon = true
		}
		if d.ID == later.ID {
			sawLater = true
		}
	}
	if !sawSoon {
		t.Error("recording within cutoff missing from due list")
	}
	if sawLater {
		t.Error("recording past cutoff leaked into due list")
	}
}

// TestDVR_Integration_ListRecordingsForUserStatusFilter — the optional
// status filter actually filters when supplied, returns all when nil.
func TestDVR_Integration_ListRecordingsForUserStatusFilter(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "dvr-stat-"+uuid.New().String()[:8])
	chID := seedTunerChannel(t, q, "dvr-stat-tuner-"+uuid.New().String()[:6])

	starts := time.Now()
	ends := starts.Add(time.Hour)
	for i := 0; i < 3; i++ {
		progID := seedEPGProgram(t, pool, chID,
			fmt.Sprintf("EP-stat-%d-%s", i, uuid.New().String()[:6]),
			starts, ends)
		if _, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
			UserID: user, ChannelID: chID,
			ProgramID: pgtype.UUID{Bytes: progID, Valid: true},
			Title:     "row",
			StartsAt:  pgtype.Timestamptz{Time: starts, Valid: true},
			EndsAt:    pgtype.Timestamptz{Time: ends, Valid: true},
		}); err != nil {
			t.Fatalf("UpsertRecording (row %d): %v", i, err)
		}
	}

	// Without filter — get all 3.
	all, _ := q.ListRecordingsForUser(ctx, gen.ListRecordingsForUserParams{
		UserID: user, Limit: 100, Offset: 0,
	})
	if len(all) != 3 {
		t.Errorf("nil filter: got %d, want 3", len(all))
	}

	// With non-matching filter — get 0.
	completed := "completed"
	completedRows, _ := q.ListRecordingsForUser(ctx, gen.ListRecordingsForUserParams{
		UserID: user, Limit: 100, Offset: 0, Status: &completed,
	})
	if len(completedRows) != 0 {
		t.Errorf("status=completed filter: got %d, want 0 (none are completed)", len(completedRows))
	}

	// With matching filter — get all 3.
	scheduled := "scheduled"
	scheduledRows, _ := q.ListRecordingsForUser(ctx, gen.ListRecordingsForUserParams{
		UserID: user, Limit: 100, Offset: 0, Status: &scheduled,
	})
	if len(scheduledRows) != 3 {
		t.Errorf("status=scheduled filter: got %d, want 3", len(scheduledRows))
	}
}
