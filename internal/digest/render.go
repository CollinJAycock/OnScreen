package digest

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/i18n"
	"github.com/onscreen/onscreen/internal/userstats"
)

// A digest is written in its recipient's language (users.ui_language, else
// English) from the email.digest.* catalog keys
// (internal/i18n/locales/*/email.json). Titles, usernames and links are
// values in that text, never translated; the HTML escapes the finished text,
// values included, as it escaped the English before.

// Entry is one title in a digest.
type Entry struct {
	ID        uuid.UUID
	LibraryID uuid.UUID
	// ItemType decides the link (movie, show, album, book, audiobook,
	// episode).
	ItemType string
	Title    string
	Year     *int32
	// Subtitle is the album's artist, "E5 · Title" for an episode in
	// progress, or "3 new episodes" for a show (already in the digest's
	// language).
	Subtitle   string
	PosterPath string // library-relative artwork path; "" = none
	// ContentID is set when the poster was attached inline.
	ContentID string
}

// Section is one kind of new content ("New movies", "New episodes", ...).
type Section struct {
	Kind    string // movie | show | album | book
	Heading string // in the digest's language (sectionHeading)
	Entries []Entry
	// More is how many further entries the kind had beyond those listed.
	More int64
}

// Content is everything a digest email shows.
type Content struct {
	Username string
	Period   Period
	Sections []Section
	// ContinueWatching is empty when the server leaves it out or the user has
	// nothing in progress.
	ContinueWatching []Entry
	// Stats is nil when the server leaves the user's numbers out.
	Stats *userstats.Totals
	// BaseURL is the server's public URL (no trailing slash); "" omits links.
	BaseURL        string
	UnsubscribeURL string
	// Test marks the admin's "Send me a test digest now".
	Test bool
	// Lang is the language the email is written in ("" = English).
	Lang i18n.Lang
}

func (c Content) lang() i18n.Lang {
	if c.Lang == "" {
		return i18n.English
	}
	return c.Lang
}

// Empty reports whether nothing new was added: such a digest isn't sent.
func (c Content) Empty() bool {
	for _, s := range c.Sections {
		if len(s.Entries) > 0 {
			return false
		}
	}
	return true
}

// sectionOrder / sectionHeadingKeys fix the order and headings of the
// sections.
var (
	sectionOrder       = []string{"movie", "show", "album", "book"}
	sectionHeadingKeys = map[string]string{
		"movie": "email.digest.section.movie",
		"show":  "email.digest.section.show",
		"album": "email.digest.section.album",
		"book":  "email.digest.section.book",
	}
)

// sectionHeading is the heading of kind's section in l.
func sectionHeading(l i18n.Lang, kind string) string {
	return i18n.T(l, sectionHeadingKeys[kind], nil)
}

// itemPath mirrors the web app's itemHref: where a title of this type opens.
func itemPath(itemType string, id uuid.UUID) string {
	switch itemType {
	case "album":
		return "/albums/" + id.String()
	case "book":
		return "/books/" + id.String()
	case "audiobook":
		return "/audiobooks/" + id.String()
	case "artist":
		return "/artists/" + id.String()
	default:
		return "/watch/" + id.String()
	}
}

// cleanBaseURL keeps only an absolute http(s) URL, without a trailing slash.
func cleanBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}

// Subject returns the email's subject line.
func (c Content) Subject() string {
	l := c.lang()
	key := c.byFrequency("email.digest.subject.weekly", "email.digest.subject.monthly")
	s := i18n.T(l, key, i18n.Params{"period": c.period()})
	if c.Test {
		s = i18n.T(l, "email.digest.subject.test", i18n.Params{"subject": s})
	}
	return s
}

// period is the period's name in the digest's language.
func (c Content) period() string { return c.Period.LabelIn(c.lang()) }

// byFrequency picks the weekly or the monthly form of a message.
func (c Content) byFrequency(weeklyKey, monthlyKey string) string {
	if c.Period.Frequency == FrequencyMonthly {
		return monthlyKey
	}
	return weeklyKey
}

func (c Content) link(path string) string {
	if c.BaseURL == "" {
		return ""
	}
	return c.BaseURL + path
}

func yearSuffix(y *int32) string {
	if y == nil || *y == 0 {
		return ""
	}
	return " (" + strconv.Itoa(int(*y)) + ")"
}

// fmtHours renders hours with one decimal, dropping ".0", with the
// language's decimal mark ("12.5" / "12,5").
func fmtHours(l i18n.Lang, h float64) string {
	s := strings.TrimSuffix(strconv.FormatFloat(h, 'f', 1, 64), ".0")
	if l == i18n.Spanish {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// hoursLine is "1 hour watched" / "12.5 hours watched": hours are rounded to
// a tenth, and only a value shown as exactly 1 takes the singular (1.5 is
// plural in English and Spanish alike).
func hoursLine(l i18n.Lang, key string, h float64) string {
	s := fmtHours(l, h)
	n := 2
	if s == "1" {
		n = 1
	}
	return i18n.TN(l, key, n, i18n.Params{"hours": s})
}

// statsLines are the user's numbers as short lines ("12.5 hours watched").
func statsLines(l i18n.Lang, t *userstats.Totals) []string {
	if t == nil {
		return nil
	}
	var out []string
	if t.HoursWatched > 0 {
		out = append(out, hoursLine(l, "email.digest.hoursWatched", t.HoursWatched))
	}
	if t.HoursListened > 0 {
		out = append(out, hoursLine(l, "email.digest.hoursListened", t.HoursListened))
	}
	if t.Plays > 0 {
		out = append(out, i18n.TN(l, "email.digest.plays", int(t.Plays), nil))
	}
	if t.ItemsFinished > 0 {
		out = append(out, i18n.TN(l, "email.digest.titlesFinished", int(t.ItemsFinished), nil))
	}
	if t.BooksRead > 0 {
		out = append(out, i18n.TN(l, "email.digest.booksRead", int(t.BooksRead), nil))
	}
	return out
}

// Text renders the plain-text body.
func (c Content) Text() string {
	l := c.lang()
	period := i18n.Params{"period": c.period()}
	var b strings.Builder
	b.WriteString(i18n.T(l, "email.digest.greeting", i18n.Params{"username": c.Username}) + "\n\n")
	if c.Empty() {
		b.WriteString(i18n.T(l, "email.digest.textEmpty", period) + "\n")
	} else {
		b.WriteString(i18n.T(l, "email.digest.textIntro", period) + "\n")
	}
	for _, s := range c.Sections {
		if len(s.Entries) == 0 {
			continue
		}
		b.WriteString("\n" + strings.ToUpper(s.Heading) + "\n")
		for _, e := range s.Entries {
			b.WriteString("- " + e.Title + yearSuffix(e.Year))
			if e.Subtitle != "" {
				b.WriteString(" - " + e.Subtitle)
			}
			if href := c.link(itemPath(e.ItemType, e.ID)); href != "" {
				b.WriteString("\n  " + href)
			}
			b.WriteString("\n")
		}
		if s.More > 0 {
			b.WriteString(i18n.TN(l, "email.digest.moreText", int(s.More), nil) + "\n")
		}
	}
	if len(c.ContinueWatching) > 0 {
		b.WriteString("\n" + strings.ToUpper(i18n.T(l, "email.digest.continueWatching", nil)) + "\n")
		for _, e := range c.ContinueWatching {
			b.WriteString("- " + e.Title)
			if e.Subtitle != "" {
				b.WriteString(" - " + e.Subtitle)
			}
			b.WriteString("\n")
		}
	}
	if lines := statsLines(l, c.Stats); len(lines) > 0 {
		b.WriteString("\n" + strings.ToUpper(c.statsHeading()) + "\n")
		for _, line := range lines {
			b.WriteString("- " + line + "\n")
		}
	}
	b.WriteString("\n-- \n")
	b.WriteString(c.footer() + "\n")
	if c.UnsubscribeURL != "" {
		b.WriteString(i18n.T(l, "email.digest.unsubscribeText", i18n.Params{"url": c.UnsubscribeURL}) + "\n")
	}
	return b.String()
}

// statsHeading heads the user's numbers ("Your week in numbers").
func (c Content) statsHeading() string {
	return i18n.T(c.lang(), c.byFrequency("email.digest.stats.weekly", "email.digest.stats.monthly"), nil)
}

// footer says why the email came.
func (c Content) footer() string {
	return i18n.T(c.lang(), c.byFrequency("email.digest.footer.weekly", "email.digest.footer.monthly"), nil)
}

// Colours match the other OnScreen emails (internal/email templates).
const (
	cBg      = "#07070d"
	cCard    = "#0e0e18"
	cText    = "#eeeef8"
	cMuted   = "#8888a0"
	cFaint   = "#44445a"
	cAccent  = "#7c6af7"
	cBorder  = "rgba(255,255,255,0.06)"
	cPoster  = "#1a1a28"
	posterW  = 96
	posterH  = 144
	perRow   = 4
	cardWide = 520
)

// HTML renders the HTML body. Every title and other stored string is
// escaped; links are built from the validated base URL and item UUIDs only.
func (c Content) HTML() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>`)
	b.WriteString(`<body style="margin:0;padding:0;background:` + cBg + `;font-family:system-ui,-apple-system,sans-serif">`)
	b.WriteString(`<table width="100%" cellpadding="0" cellspacing="0" style="background:` + cBg + `;padding:32px 12px"><tr><td align="center">`)
	fmt.Fprintf(&b, `<table width="%d" cellpadding="0" cellspacing="0" style="max-width:%dpx;width:100%%;background:%s;border:1px solid %s;border-radius:16px;padding:32px">`, cardWide, cardWide, cCard, cBorder)

	b.WriteString(`<tr><td style="text-align:center;padding-bottom:20px"><span style="font-size:22px;font-weight:700;color:` + cText + `;letter-spacing:-0.02em">OnScreen</span></td></tr>`)
	l := c.lang()
	heading := i18n.T(l, "email.digest.heading", i18n.Params{"period": c.period()})
	fmt.Fprintf(&b, `<tr><td style="font-size:18px;font-weight:600;color:%s;padding-bottom:6px">%s</td></tr>`, cText, e(heading))
	intro := i18n.T(l, "email.digest.intro", i18n.Params{"username": c.Username})
	if c.Empty() {
		intro = i18n.T(l, "email.digest.introEmpty", i18n.Params{"username": c.Username, "period": c.period()})
	}
	fmt.Fprintf(&b, `<tr><td style="font-size:14px;color:%s;line-height:1.6;padding-bottom:8px">%s</td></tr>`, cMuted, e(intro))

	for _, s := range c.Sections {
		if len(s.Entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, `<tr><td style="font-size:15px;font-weight:600;color:%s;padding:20px 0 10px">%s</td></tr>`, cText, e(s.Heading))
		b.WriteString(`<tr><td>`)
		c.posterGrid(&b, s.Entries)
		b.WriteString(`</td></tr>`)
		if s.More > 0 {
			fmt.Fprintf(&b, `<tr><td style="font-size:13px;color:%s;padding-top:6px">%s</td></tr>`, cMuted, e(i18n.TN(l, "email.digest.more", int(s.More), nil)))
		}
	}

	if len(c.ContinueWatching) > 0 {
		fmt.Fprintf(&b, `<tr><td style="font-size:15px;font-weight:600;color:%s;padding:24px 0 10px">%s</td></tr>`, cText, e(i18n.T(l, "email.digest.continueWatching", nil)))
		b.WriteString(`<tr><td>`)
		c.posterGrid(&b, c.ContinueWatching)
		b.WriteString(`</td></tr>`)
	}

	if lines := statsLines(l, c.Stats); len(lines) > 0 {
		fmt.Fprintf(&b, `<tr><td style="font-size:15px;font-weight:600;color:%s;padding:24px 0 8px">%s</td></tr>`, cText, e(c.statsHeading()))
		fmt.Fprintf(&b, `<tr><td style="font-size:14px;color:%s;line-height:1.8">`, cMuted)
		for i, line := range lines {
			if i > 0 {
				b.WriteString(` &middot; `)
			}
			b.WriteString(e(line))
		}
		b.WriteString(`</td></tr>`)
	}

	if href := c.link("/"); href != "" {
		fmt.Fprintf(&b, `<tr><td style="padding:28px 0 0"><a href="%s" style="display:inline-block;padding:12px 28px;background:%s;color:#fff;text-decoration:none;border-radius:8px;font-weight:600;font-size:15px">%s</a></td></tr>`, e(href), cAccent, e(i18n.T(l, "email.digest.open", nil)))
	}

	fmt.Fprintf(&b, `<tr><td style="padding-top:28px;font-size:12px;color:%s;line-height:1.6;border-top:1px solid %s">%s`, cFaint, cBorder, e(c.footer()))
	if c.UnsubscribeURL != "" {
		fmt.Fprintf(&b, ` <a href="%s" style="color:%s">%s</a>`, e(c.UnsubscribeURL), cMuted, e(i18n.T(l, "email.digest.unsubscribe", nil)))
	}
	if href := c.link("/account"); href != "" {
		fmt.Fprintf(&b, ` &middot; <a href="%s" style="color:%s">%s</a>`, e(href), cMuted, e(i18n.T(l, "email.digest.emailSettings", nil)))
	}
	b.WriteString(`</td></tr>`)

	b.WriteString(`</table></td></tr></table></body></html>`)
	return b.String()
}

// posterGrid writes entries as rows of poster tiles with their titles
// underneath. A tile without an attached poster gets a plain block of the
// same size, so the grid stays aligned.
func (c Content) posterGrid(b *strings.Builder, entries []Entry) {
	e := html.EscapeString
	b.WriteString(`<table cellpadding="0" cellspacing="0" width="100%">`)
	for i := 0; i < len(entries); i += perRow {
		b.WriteString(`<tr>`)
		for j := i; j < i+perRow; j++ {
			fmt.Fprintf(b, `<td width="25%%" valign="top" style="padding:0 6px 14px 0">`)
			if j < len(entries) {
				en := entries[j]
				href := c.link(itemPath(en.ItemType, en.ID))
				if href != "" {
					fmt.Fprintf(b, `<a href="%s" style="text-decoration:none">`, e(href))
				}
				if en.ContentID != "" {
					fmt.Fprintf(b, `<img src="cid:%s" width="%d" height="%d" alt="%s" style="display:block;width:%dpx;max-width:100%%;height:auto;border-radius:6px;border:0">`,
						e(en.ContentID), posterW, posterH, e(en.Title), posterW)
				} else {
					fmt.Fprintf(b, `<div style="width:%dpx;max-width:100%%;height:%dpx;border-radius:6px;background:%s"></div>`, posterW, posterH, cPoster)
				}
				fmt.Fprintf(b, `<div style="font-size:12px;font-weight:600;color:%s;line-height:1.35;padding-top:6px">%s</div>`, cText, e(en.Title+yearSuffix(en.Year)))
				if en.Subtitle != "" {
					fmt.Fprintf(b, `<div style="font-size:11px;color:%s;line-height:1.35">%s</div>`, cMuted, e(en.Subtitle))
				}
				if href != "" {
					b.WriteString(`</a>`)
				}
			}
			b.WriteString(`</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</table>`)
}
