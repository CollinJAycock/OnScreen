package digest

import (
	"fmt"
	"strconv"
	"time"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/i18n"
)

// Frequencies a user can choose.
const (
	FrequencyOff     = "off"
	FrequencyWeekly  = "weekly"
	FrequencyMonthly = "monthly"
)

// ValidFrequency reports whether f is one of the three frequencies.
func ValidFrequency(f string) bool {
	return f == FrequencyOff || f == FrequencyWeekly || f == FrequencyMonthly
}

// sendGrace is how long after its scheduled moment a period is still sent.
// The task runs hourly, so this covers a server that was down at send time;
// beyond it the period is skipped rather than mailed days late, and a user
// who opts in mid-week waits for the next scheduled send instead of getting
// last week's digest at once.
const sendGrace = 48 * time.Hour

// Period is one digest's window.
type Period struct {
	// Key identifies the period for idempotency (user_digest_prefs.last_period):
	// "2026-W42" for a week, "2026-09" for a month.
	Key string
	// Since / Until bound what counts as new: the seven whole days before the
	// send day, or the previous calendar month.
	Since, Until time.Time
	// Scheduled is when this period's digest is due.
	Scheduled time.Time
	// Label names the period in English ("the week of Oct 6", "September
	// 2026"), for logs. The email writes LabelIn its recipient's language.
	Label     string
	Frequency string
}

// The month names in the email.digest catalog, January first: standing
// alone ("September 2026") and in a date ("Oct 5").
var (
	monthKeys = [12]string{
		"email.digest.month.january", "email.digest.month.february", "email.digest.month.march",
		"email.digest.month.april", "email.digest.month.may", "email.digest.month.june",
		"email.digest.month.july", "email.digest.month.august", "email.digest.month.september",
		"email.digest.month.october", "email.digest.month.november", "email.digest.month.december",
	}
	dateMonthKeys = [12]string{
		"email.digest.dateMonth.january", "email.digest.dateMonth.february", "email.digest.dateMonth.march",
		"email.digest.dateMonth.april", "email.digest.dateMonth.may", "email.digest.dateMonth.june",
		"email.digest.dateMonth.july", "email.digest.dateMonth.august", "email.digest.dateMonth.september",
		"email.digest.dateMonth.october", "email.digest.dateMonth.november", "email.digest.dateMonth.december",
	}
)

// LabelIn names the period in l, from the email.digest.period.* patterns:
// "the week of Oct 5" / "la semana del 5 de octubre" / "10月5日起的一周", and
// "September 2026" / "septiembre de 2026" / "2026年9月". The English reads
// exactly as Label. A Period without its window (built by hand) keeps Label.
func (p Period) LabelIn(l i18n.Lang) string {
	if p.Since.IsZero() {
		return p.Label
	}
	// The day and year go in as strings: a number would get digit grouping
	// ("2,026").
	m := p.Since.Month() - 1
	switch p.Frequency {
	case FrequencyWeekly:
		date := i18n.T(l, "email.digest.period.date", i18n.Params{
			"month": i18n.T(l, dateMonthKeys[m], nil),
			"day":   strconv.Itoa(p.Since.Day()),
		})
		return i18n.T(l, "email.digest.period.week", i18n.Params{"date": date})
	case FrequencyMonthly:
		return i18n.T(l, "email.digest.period.month", i18n.Params{
			"month": i18n.T(l, monthKeys[m], nil),
			"year":  strconv.Itoa(p.Since.Year()),
		})
	}
	return p.Label
}

// CurrentPeriod returns the most recent period of freq whose scheduled send
// moment is at or before now (in loc), and whether it is still due (within
// sendGrace of that moment). freq must be weekly or monthly.
//
// Weekly digests are scheduled on cfg.Weekday at cfg.Hour and cover the seven
// days before that day; monthly ones on the 1st at cfg.Hour and cover the
// previous calendar month.
func CurrentPeriod(freq string, cfg settings.DigestConfig, now time.Time, loc *time.Location) (Period, bool) {
	if loc == nil {
		loc = time.Local
	}
	now = now.In(loc)
	var p Period
	switch freq {
	case FrequencyWeekly:
		s := time.Date(now.Year(), now.Month(), now.Day(), cfg.Hour, 0, 0, 0, loc)
		back := (int(now.Weekday()) - cfg.Weekday + 7) % 7
		s = s.AddDate(0, 0, -back)
		if s.After(now) {
			s = s.AddDate(0, 0, -7)
		}
		day := time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, loc)
		year, week := s.ISOWeek()
		p = Period{
			Key:       fmt.Sprintf("%d-W%02d", year, week),
			Since:     day.AddDate(0, 0, -7),
			Until:     day,
			Scheduled: s,
			Label:     "the week of " + day.AddDate(0, 0, -7).Format("Jan 2"),
			Frequency: freq,
		}
	case FrequencyMonthly:
		s := time.Date(now.Year(), now.Month(), 1, cfg.Hour, 0, 0, 0, loc)
		if s.After(now) {
			s = s.AddDate(0, -1, 0)
		}
		until := time.Date(s.Year(), s.Month(), 1, 0, 0, 0, 0, loc)
		since := until.AddDate(0, -1, 0)
		p = Period{
			Key:       since.Format("2006-01"),
			Since:     since,
			Until:     until,
			Scheduled: s,
			Label:     since.Format("January 2006"),
			Frequency: freq,
		}
	default:
		return Period{}, false
	}
	return p, now.Sub(p.Scheduled) < sendGrace
}
