package v1

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// Memories tuning.
const (
	// memoryYears is how far back memories look.
	memoryYears = 60
	// memoryGroupSize caps the photos in one memory.
	memoryGroupSize = 30
	// memoryMaxGroups caps the memories returned.
	memoryMaxGroups = 10
	// memoryCandidateLimit caps the photos read for one request.
	memoryCandidateLimit = 4000
	// memorySmallLibrary is the dated-photo count under which "today" widens
	// to ±1 day, so a small library still has something to show.
	memorySmallLibrary = 2000
	// memoryBurstGap: photos closer together than this are one burst, and
	// a memory shows one photo per burst before it shows a second.
	memoryBurstGap = 90 * time.Second
	// memoryWeekMin is the fewest photos a "this week" memory needs.
	memoryWeekMin = 3
)

// MemoryPhotoResponse is one photo in a memory.
type MemoryPhotoResponse struct {
	ID          uuid.UUID `json:"id"`
	LibraryID   uuid.UUID `json:"library_id"`
	Title       string    `json:"title"`
	PosterPath  *string   `json:"poster_path,omitempty"`
	TakenAt     time.Time `json:"taken_at"`
	Width       *int32    `json:"width,omitempty"`
	Height      *int32    `json:"height,omitempty"`
	Orientation *int32    `json:"orientation,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MemoryGroupResponse is one memory: "3 years ago today" or "This week 5
// years ago", with a cover and up to memoryGroupSize photos in time order.
type MemoryGroupResponse struct {
	Kind     string                `json:"kind"` // "on_this_day" | "this_week"
	Year     int                   `json:"year"`
	YearsAgo int                   `json:"years_ago"`
	Title    string                `json:"title"`
	Total    int                   `json:"total"`
	Cover    MemoryPhotoResponse   `json:"cover"`
	Items    []MemoryPhotoResponse `json:"items"`
}

// MemoriesResponse is the /photos/memories body.
type MemoriesResponse struct {
	Date   string                `json:"date"`
	Groups []MemoryGroupResponse `json:"groups"`
}

type memoryPhoto struct {
	MemoryPhotoResponse
	faces int32
}

// Memories handles GET /api/v1/photos/memories?date=YYYY-MM-DD.
//
// date defaults to today in the server's time zone; clients send their own
// local date so "today" follows the viewer. Photos are matched by their
// EXIF taken date across every photo library the caller may open, with the
// rating ceiling and label restriction applied. Each past year with photos
// on this month-day (±1 day for small libraries) becomes "N years ago
// today"; a year with none that day but some within three days becomes
// "This week N years ago". An empty groups list means nothing to show.
func (h *PhotoPeopleHandler) Memories(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	loc := time.Local
	day := h.now().In(loc)
	if s := r.URL.Query().Get("date"); s != "" {
		d, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			respond.BadRequest(w, r, "date must be YYYY-MM-DD")
			return
		}
		day = d
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	out := MemoriesResponse{Date: day.Format("2006-01-02"), Groups: []MemoryGroupResponse{}}

	libs, err := h.memoryLibraries(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "memories: libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	if len(libs) == 0 {
		respond.Success(w, r, out)
		return
	}
	dated, err := h.db.CountDatedPhotos(r.Context(), libs)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "memories: count", "err", err)
		respond.InternalError(w, r)
		return
	}
	if dated == 0 {
		respond.Success(w, r, out)
		return
	}
	todayPad := 0
	if dated < memorySmallLibrary {
		todayPad = 1
	}

	from, to := memoryWindows(day, memoryYears, 3)
	f := callerListFilter(r)
	rows, err := h.db.ListPhotoMemoryCandidates(r.Context(), gen.ListPhotoMemoryCandidatesParams{
		LibraryIds: libs, RangeFrom: from, RangeTo: to, Lim: memoryCandidateLimit,
		MaxRatingRank: f.rank, BlockedLabels: f.blocked, AllowedLabels: f.allowed,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "memories: candidates", "err", err)
		respond.InternalError(w, r)
		return
	}
	photos := make([]memoryPhoto, 0, len(rows))
	for _, row := range rows {
		if !row.TakenAt.Valid {
			continue
		}
		photos = append(photos, memoryPhoto{
			MemoryPhotoResponse: MemoryPhotoResponse{
				ID: row.ID, LibraryID: row.LibraryID, Title: row.Title, PosterPath: row.PosterPath,
				TakenAt: row.TakenAt.Time, Width: row.Width, Height: row.Height,
				Orientation: row.Orientation, UpdatedAt: row.UpdatedAt.Time,
			},
			faces: row.FaceCount,
		})
	}
	out.Groups = buildMemoryGroups(day, photos, todayPad)
	respond.Success(w, r, out)
}

// memoryLibraries is every photo library the caller may open.
func (h *PhotoPeopleHandler) memoryLibraries(r *http.Request) ([]uuid.UUID, error) {
	all, err := h.db.ListPhotoLibraryIDs(r.Context())
	if err != nil || h.access == nil {
		return all, err
	}
	claims := middleware.ClaimsFromContext(r.Context())
	allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		return nil, err
	}
	if allowed == nil { // admin: every library
		return all, nil
	}
	out := all[:0]
	for _, id := range all {
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// sameDayIn returns month/day in year y, moving Feb 29 to Feb 28 in a
// year without one.
func sameDayIn(day time.Time, y int) time.Time {
	m, d := day.Month(), day.Day()
	if m == time.February && d == 29 && !isLeap(y) {
		d = 28
	}
	return time.Date(y, m, d, 0, 0, 0, 0, day.Location())
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// memoryWindows returns, for each of the past `years` years, the window
// [day-pad, day+1+pad) in local time, as the query's parallel arrays.
func memoryWindows(day time.Time, years, pad int) ([]pgtype.Timestamptz, []pgtype.Timestamptz) {
	from := make([]pgtype.Timestamptz, 0, years)
	to := make([]pgtype.Timestamptz, 0, years)
	for n := 1; n <= years; n++ {
		d := sameDayIn(day, day.Year()-n)
		from = append(from, pgtype.Timestamptz{Time: d.AddDate(0, 0, -pad), Valid: true})
		to = append(to, pgtype.Timestamptz{Time: d.AddDate(0, 0, 1+pad), Valid: true})
	}
	return from, to
}

// buildMemoryGroups sorts candidates into per-year memories (see Memories).
func buildMemoryGroups(day time.Time, photos []memoryPhoto, todayPad int) []MemoryGroupResponse {
	byYear := map[int][]memoryPhoto{}
	for _, p := range photos {
		y := p.TakenAt.In(day.Location()).Year()
		byYear[y] = append(byYear[y], p)
	}
	years := make([]int, 0, len(byYear))
	for y := range byYear {
		if y < day.Year() {
			years = append(years, y)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(years))) // most recent first

	var groups []MemoryGroupResponse
	for _, y := range years {
		if len(groups) >= memoryMaxGroups {
			break
		}
		d := sameDayIn(day, y)
		todayFrom, todayTo := d.AddDate(0, 0, -todayPad), d.AddDate(0, 0, 1+todayPad)
		var today, week []memoryPhoto
		for _, p := range byYear[y] {
			t := p.TakenAt.In(day.Location())
			if !t.Before(todayFrom) && t.Before(todayTo) {
				today = append(today, p)
			} else {
				week = append(week, p)
			}
		}
		ago := day.Year() - y
		switch {
		case len(today) > 0:
			groups = append(groups, memoryGroup("on_this_day", y, ago, today))
		case len(week) >= memoryWeekMin:
			groups = append(groups, memoryGroup("this_week", y, ago, week))
		}
	}
	if groups == nil {
		groups = []MemoryGroupResponse{}
	}
	return groups
}

func memoryGroup(kind string, year, ago int, photos []memoryPhoto) MemoryGroupResponse {
	sort.SliceStable(photos, func(i, j int) bool { return photos[i].TakenAt.Before(photos[j].TakenAt) })
	picked := spreadPick(photos, memoryGroupSize)
	items := make([]MemoryPhotoResponse, len(picked))
	for i, p := range picked {
		items[i] = p.MemoryPhotoResponse
	}
	return MemoryGroupResponse{
		Kind: kind, Year: year, YearsAgo: ago, Title: memoryTitle(kind, ago),
		Total: len(photos), Cover: memoryCover(picked), Items: items,
	}
}

func memoryTitle(kind string, ago int) string {
	unit := "years"
	if ago == 1 {
		unit = "year"
	}
	if kind == "this_week" {
		return fmt.Sprintf("This week %d %s ago", ago, unit)
	}
	return fmt.Sprintf("%d %s ago today", ago, unit)
}

// spreadPick chooses up to n photos (sorted by time) that cover the day
// rather than one burst: one photo per burst first, evenly spaced, then
// further burst photos only if there's room. The result stays in time order.
func spreadPick(photos []memoryPhoto, n int) []memoryPhoto {
	if len(photos) <= n {
		return photos
	}
	var heads, rest []memoryPhoto
	var last time.Time
	for i, p := range photos {
		if i == 0 || p.TakenAt.Sub(last) >= memoryBurstGap {
			heads = append(heads, p)
		} else {
			rest = append(rest, p)
		}
		last = p.TakenAt
	}
	if len(heads) >= n {
		return evenly(heads, n)
	}
	out := append(heads, evenly(rest, n-len(heads))...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TakenAt.Before(out[j].TakenAt) })
	return out
}

// evenly picks k items spread across xs, keeping the first and last.
func evenly(xs []memoryPhoto, k int) []memoryPhoto {
	if k <= 0 {
		return nil
	}
	if k >= len(xs) {
		return xs
	}
	if k == 1 {
		return []memoryPhoto{xs[len(xs)/2]}
	}
	out := make([]memoryPhoto, 0, k)
	for i := 0; i < k; i++ {
		out = append(out, xs[i*(len(xs)-1)/(k-1)])
	}
	return out
}

// memoryCover is the photo with the most faces (people make a memory),
// else the middle one.
func memoryCover(picked []memoryPhoto) MemoryPhotoResponse {
	best := len(picked) / 2
	for i, p := range picked {
		if p.faces > picked[best].faces {
			best = i
		}
	}
	return picked[best].MemoryPhotoResponse
}
