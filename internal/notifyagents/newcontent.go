package notifyagents

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// New-content batching. A scan (full or watcher-driven) that finds new items
// reports its library; the first report opens a window, and when it closes
// ONE message lists what every reported library gained since its watermark —
// never one message per item, however large the import.
const (
	newContentMaxRows   = 500 // rows read per library per batch
	newContentMaxTitles = 10  // titles listed before "…and N more"
)

type newContentBatcher struct {
	s      *Service
	window time.Duration
	start  time.Time

	mu      sync.Mutex
	since   map[uuid.UUID]time.Time // per-library watermark (DB created_at)
	pending map[uuid.UUID]struct{}
	timer   *time.Timer
	stopped bool
}

func newNewContentBatcher(s *Service, window time.Duration) *newContentBatcher {
	return &newContentBatcher{
		s:       s,
		window:  window,
		start:   time.Now(),
		since:   map[uuid.UUID]time.Time{},
		pending: map[uuid.UUID]struct{}{},
	}
}

// LibraryHasNewContent records that a scan added items to a library. The
// titles are read (and announced, in one batched new_content message) when
// the batch window closes.
func (s *Service) LibraryHasNewContent(libraryID uuid.UUID) {
	s.newContent.note(libraryID)
}

func (b *newContentBatcher) note(libraryID uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.pending[libraryID] = struct{}{}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.window, b.flush)
	}
}

func (b *newContentBatcher) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	if b.timer != nil {
		b.timer.Stop()
	}
}

// flush reads every pending library's new titles and emits one message.
func (b *newContentBatcher) flush() {
	b.mu.Lock()
	libs := make([]uuid.UUID, 0, len(b.pending))
	for id := range b.pending {
		libs = append(libs, id)
	}
	b.pending = map[uuid.UUID]struct{}{}
	b.timer = nil
	stopped := b.stopped
	b.mu.Unlock()
	if stopped || len(libs) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(b.s.ctx, 30*time.Second)
	defer cancel()
	var rows []gen.ListNewContentForNotificationAgentsRow
	truncated := false
	for _, lib := range libs {
		b.mu.Lock()
		since, ok := b.since[lib]
		if !ok {
			since = b.start
		}
		b.mu.Unlock()
		got, err := b.s.db.ListNewContentForNotificationAgents(ctx, gen.ListNewContentForNotificationAgentsParams{
			LibraryID: lib,
			Since:     pgtype.Timestamptz{Time: since, Valid: true},
			MaxRows:   newContentMaxRows,
		})
		if err != nil {
			b.s.logger.Warn("notification agents: list new content", "library_id", lib, "err", err)
			continue
		}
		if len(got) == newContentMaxRows {
			truncated = true
		}
		// Rows are newest first; the watermark moves to the newest one seen
		// (the DB's clock, not ours).
		if len(got) > 0 && got[0].CreatedAt.Valid {
			b.mu.Lock()
			if got[0].CreatedAt.Time.After(b.since[lib]) {
				b.since[lib] = got[0].CreatedAt.Time
			}
			b.mu.Unlock()
		}
		rows = append(rows, got...)
	}
	m, ok := newContentMessage(rows, truncated)
	if !ok {
		return
	}
	if m.itemID != uuid.Nil {
		m.msg.URL = b.s.link(ctx, "/watch/"+m.itemID.String())
	} else {
		m.msg.URL = b.s.link(ctx, "/")
	}
	b.s.Emit(m.msg)
}

type newContentResult struct {
	msg    Message
	itemID uuid.UUID // the single title's page, when there is one
}

// newContentMessage aggregates new rows into one message: episodes grouped
// under their show ("Show — 3 new episodes"), everything else by title.
func newContentMessage(rows []gen.ListNewContentForNotificationAgentsRow, truncated bool) (newContentResult, bool) {
	type entry struct {
		id       uuid.UUID
		label    string
		year     *int32
		episodes int
		isShow   bool
	}
	var order []uuid.UUID
	entries := map[uuid.UUID]*entry{}
	add := func(id uuid.UUID) *entry {
		if e, ok := entries[id]; ok {
			return e
		}
		e := &entry{id: id}
		entries[id] = e
		order = append(order, id)
		return e
	}
	for _, r := range rows {
		switch {
		case r.Type == "episode":
			if !r.ShowID.Valid || r.ShowTitle == nil {
				continue // an orphan episode has no title worth announcing
			}
			e := add(uuid.UUID(r.ShowID.Bytes))
			e.label, e.isShow = *r.ShowTitle, true
			e.episodes++
		case r.Type == "show":
			e := add(r.ID)
			e.label, e.isShow, e.year = r.Title, true, r.Year
		default:
			e := add(r.ID)
			e.label, e.year = r.Title, r.Year
		}
	}
	if len(order) == 0 {
		return newContentResult{}, false
	}
	lines := make([]string, 0, newContentMaxTitles+1)
	for i, id := range order {
		if i == newContentMaxTitles {
			break
		}
		e := entries[id]
		line := "• " + e.label
		if e.year != nil && *e.year > 0 && !e.isShow {
			line += " (" + strconv.Itoa(int(*e.year)) + ")"
		}
		if e.episodes > 0 {
			line += fmt.Sprintf(" — %d new episode", e.episodes)
			if e.episodes != 1 {
				line += "s"
			}
		}
		lines = append(lines, line)
	}
	switch rest := len(order) - newContentMaxTitles; {
	case rest > 0 && truncated:
		lines = append(lines, fmt.Sprintf("…and %d+ more", rest))
	case rest > 0:
		lines = append(lines, fmt.Sprintf("…and %d more", rest))
	case truncated:
		lines = append(lines, "…and more")
	}
	var title string
	if n := len(order); n == 1 {
		title = "New on OnScreen: " + entries[order[0]].label
	} else {
		title = fmt.Sprintf("New on OnScreen: %d titles", n)
		if truncated {
			title += "+"
		}
	}
	res := newContentResult{msg: Message{
		Event:    EventNewContent,
		Title:    title,
		Body:     strings.Join(lines, "\n"),
		Severity: SeverityInfo,
	}}
	if len(order) == 1 {
		res.itemID = order[0]
	}
	return res, true
}
