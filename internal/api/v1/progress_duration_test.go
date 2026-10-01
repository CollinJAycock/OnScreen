package v1

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

func durationMSPtr(v int64) *int64 { return &v }

// A progress report without a duration records the length the server knows
// for what is being played. Only when it knows none is the event NULL, which
// the watch_progress rollup (migration 00035) takes as "keep the stored one".
func TestProgress_DurationFill(t *testing.T) {
	itemID := uuid.New()
	primaryID, extendedID := uuid.New(), uuid.New()
	versions := []media.File{
		{ID: primaryID, MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(5_400_000)},
		{ID: extendedID, MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(6_600_000)},
	}
	noDuration := []media.File{
		{ID: primaryID, MediaItemID: itemID, Status: "active"},
		{ID: extendedID, MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(0)},
	}
	// Only the primary version is measured; the extended cut is not.
	extendedUnmeasured := []media.File{
		{ID: primaryID, MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(5_400_000)},
		{ID: extendedID, MediaItemID: itemID, Status: "active"},
	}
	// The item's only version, unmeasured.
	soleUnmeasured := []media.File{
		{ID: extendedID, MediaItemID: itemID, Status: "active"},
	}
	movie := &media.Item{ID: itemID, Type: "movie", DurationMS: durationMSPtr(5_460_000)}
	movieNoDuration := &media.Item{ID: itemID, Type: "movie"}
	now := time.Now()

	cases := []struct {
		name     string
		body     string
		files    []media.File
		filesErr error
		item     *media.Item
		sessions []transcode.Session
		stored   *int64
		want     *int64
	}{
		{
			name:  "positive reported duration is recorded unchanged",
			body:  `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`,
			files: versions, item: movie, stored: durationMSPtr(3_000_000),
			want: durationMSPtr(120_000),
		},
		{
			name:  "omitted duration is filled from the primary file",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: versions, item: movie,
			want: durationMSPtr(5_400_000),
		},
		{
			name:  "zero duration is filled from the primary file",
			body:  `{"view_offset_ms":30000,"duration_ms":0,"state":"paused"}`,
			files: versions, item: movie,
			want: durationMSPtr(5_400_000),
		},
		{
			name:  "negative duration is filled from the primary file",
			body:  `{"view_offset_ms":30000,"duration_ms":-1,"state":"stopped"}`,
			files: versions, item: movie,
			want: durationMSPtr(5_400_000),
		},
		{
			name:  "file_id picks the version being played",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, extendedID),
			files: versions, item: movie,
			want: durationMSPtr(6_600_000),
		},
		{
			name:  "file_id outside the item falls back to the primary file",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, uuid.New()),
			files: versions, item: movie,
			want: durationMSPtr(5_400_000),
		},
		{
			// An id that isn't one of the item's files identifies nothing;
			// the live session still does.
			name:  "file_id outside the item falls back to the live session's version",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, uuid.New()),
			files: versions, item: movie,
			sessions: []transcode.Session{{ID: "live", FileID: extendedID, CreatedAt: now}},
			want:     durationMSPtr(6_600_000),
		},
		{
			name:  "malformed file_id falls back to the live session's version",
			body:  `{"view_offset_ms":30000,"state":"playing","file_id":"not-a-uuid"}`,
			files: versions, item: movie,
			sessions: []transcode.Session{{ID: "live", FileID: extendedID, CreatedAt: now}},
			want:     durationMSPtr(6_600_000),
		},
		{
			name:  "newest live transcode session picks the version being played",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: versions, item: movie,
			sessions: []transcode.Session{
				{ID: "old", FileID: primaryID, CreatedAt: now.Add(-time.Minute)},
				{ID: "new", FileID: extendedID, CreatedAt: now},
			},
			want: durationMSPtr(6_600_000),
		},
		{
			// The primary file is another cut, and the item's runtime is
			// metadata (usually the theatrical cut's): either length would
			// mark the extended cut watched (and scrobble it) early.
			name:  "file_id version without a duration beside other versions is NULL",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, extendedID),
			files: extendedUnmeasured, item: movie, stored: durationMSPtr(6_600_000),
			want: nil,
		},
		{
			// With no other version the item's runtime describes this file.
			name:  "file_id sole version without a duration takes the item's",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, extendedID),
			files: soleUnmeasured, item: movie,
			want: durationMSPtr(5_460_000),
		},
		{
			name:  "file_id sole version without a duration is NULL when the item has none",
			body:  fmt.Sprintf(`{"view_offset_ms":30000,"state":"playing","file_id":%q}`, extendedID),
			files: soleUnmeasured, item: movieNoDuration, stored: durationMSPtr(6_600_000),
			want: nil,
		},
		{
			name:  "live session version without a duration beside other versions is NULL",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: extendedUnmeasured, item: movie,
			sessions: []transcode.Session{
				{ID: "old", FileID: primaryID, CreatedAt: now.Add(-time.Minute)},
				{ID: "new", FileID: extendedID, CreatedAt: now},
			},
			want: nil,
		},
		{
			name:  "live session sole version without a duration takes the item's",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: soleUnmeasured, item: movie,
			sessions: []transcode.Session{{ID: "live", FileID: extendedID, CreatedAt: now}},
			want:     durationMSPtr(5_460_000),
		},
		{
			name:  "live session sole version without a duration is NULL when the item has none",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: soleUnmeasured, item: movieNoDuration,
			sessions: []transcode.Session{{ID: "live", FileID: extendedID, CreatedAt: now}},
			want:     nil,
		},
		{
			name:  "item duration when no file carries one",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: noDuration, item: movie, stored: durationMSPtr(3_000_000),
			want: durationMSPtr(5_460_000),
		},
		{
			name:     "item duration when the file lookup fails",
			body:     `{"view_offset_ms":30000,"state":"playing"}`,
			filesErr: errors.New("db down"), item: movie,
			want: durationMSPtr(5_460_000),
		},
		{
			// The rollup keeps the stored duration; copying it into the event
			// would let the beat complete the item against a client-reported
			// length (2.9 of 3 million ms is past 90%).
			name:  "stored duration is not copied into the event",
			body:  `{"view_offset_ms":2900000,"state":"playing"}`,
			files: noDuration, item: movieNoDuration, stored: durationMSPtr(3_000_000),
			want: nil,
		},
		{
			name:  "NULL when no duration is known anywhere",
			body:  `{"view_offset_ms":30000,"state":"playing"}`,
			files: noDuration, item: movieNoDuration,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := &mockItemMedia{item: tc.item, files: tc.files, filesErr: tc.filesErr}
			ws := &captureWatch{state: watchevent.WatchState{DurationMS: tc.stored}}
			sessions := &listingSessions{sessions: tc.sessions}
			h := NewItemHandler(ms, ws, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

			runProgress(t, h, itemID, uuid.New(), tc.body)

			if len(ws.events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(ws.events))
			}
			got := ws.events[0].DurationMS
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("duration: got %d, want NULL", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("duration: got NULL, want %d", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Fatalf("duration: got %d, want %d", *got, *tc.want)
			}
		})
	}
}

// A reported duration costs no lookups: the fill only runs for reports that
// lack one. (A fill here would list sessions to find the version played.)
func TestProgress_DurationFill_ReportedSkipsLookups(t *testing.T) {
	itemID := uuid.New()
	ms := &mockItemMedia{files: []media.File{
		{ID: uuid.New(), MediaItemID: itemID, Status: "active", DurationMS: durationMSPtr(5_400_000)},
	}}
	sessions := &listingSessions{}
	h := NewItemHandler(ms, &captureWatch{}, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	// A decision too, so the decision inference doesn't list sessions either.
	runProgress(t, h, itemID, uuid.New(),
		`{"view_offset_ms":30000,"duration_ms":120000,"state":"playing","decision":"transcode"}`)
	if sessions.lists != 0 {
		t.Fatalf("session lists: got %d, want 0", sessions.lists)
	}

	runProgress(t, h, itemID, uuid.New(), `{"view_offset_ms":30000,"state":"playing","decision":"transcode"}`)
	if sessions.lists != 1 {
		t.Fatalf("session lists after a duration-less report: got %d, want 1", sessions.lists)
	}
}
