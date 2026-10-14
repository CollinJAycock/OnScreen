package digest

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/userstats"
)

// Entry is one title in a digest.
type Entry struct {
	ID        uuid.UUID
	LibraryID uuid.UUID
	// ItemType decides the link (movie, show, album, book, audiobook,
	// episode).
	ItemType string
	Title    string
	Year     *int32
	// Subtitle is the album's artist, "S2 · E5" for an episode in progress,
	// or "3 new episodes" for a show.
	Subtitle   string
	PosterPath string // library-relative artwork path; "" = none
	// ContentID is set when the poster was attached inline.
	ContentID string
}

// Section is one kind of new content ("Movies", "Shows", ...).
type Section struct {
	Kind    string // movie | show | album | book
	Heading string
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

// sectionOrder / sectionHeadings fix the order and headings of the sections.
var (
	sectionOrder    = []string{"movie", "show", "album", "book"}
	sectionHeadings = map[string]string{
		"movie": "New movies",
		"show":  "New episodes",
		"album": "New music",
		"book":  "New books",
	}
)

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
	prefix := ""
	if c.Test {
		prefix = "[Test] "
	}
	if c.Period.Frequency == FrequencyMonthly {
		return prefix + "New on OnScreen in " + c.Period.Label
	}
	return prefix + "New on OnScreen: " + c.Period.Label
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

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}

// fmtHours renders hours with one decimal, dropping ".0".
func fmtHours(h float64) string {
	s := strconv.FormatFloat(h, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// statsLines are the user's numbers as short lines ("12.5 hours watched").
func statsLines(t *userstats.Totals) []string {
	if t == nil {
		return nil
	}
	var out []string
	if t.HoursWatched > 0 {
		out = append(out, fmtHours(t.HoursWatched)+" hours watched")
	}
	if t.HoursListened > 0 {
		out = append(out, fmtHours(t.HoursListened)+" hours listened")
	}
	if t.Plays > 0 {
		out = append(out, plural(t.Plays, "play", "plays"))
	}
	if t.ItemsFinished > 0 {
		out = append(out, plural(t.ItemsFinished, "title finished", "titles finished"))
	}
	if t.BooksRead > 0 {
		out = append(out, plural(t.BooksRead, "book read", "books read"))
	}
	return out
}

// Text renders the plain-text body.
func (c Content) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\n", c.Username)
	if c.Empty() {
		b.WriteString("Nothing new was added to your libraries in " + c.Period.Label + ".\n")
	} else {
		b.WriteString("Here's what was added to your libraries in " + c.Period.Label + ".\n")
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
			if l := c.link(itemPath(e.ItemType, e.ID)); l != "" {
				b.WriteString("\n  " + l)
			}
			b.WriteString("\n")
		}
		if s.More > 0 {
			fmt.Fprintf(&b, "...and %d more\n", s.More)
		}
	}
	if len(c.ContinueWatching) > 0 {
		b.WriteString("\nCONTINUE WATCHING\n")
		for _, e := range c.ContinueWatching {
			b.WriteString("- " + e.Title)
			if e.Subtitle != "" {
				b.WriteString(" - " + e.Subtitle)
			}
			b.WriteString("\n")
		}
	}
	if lines := statsLines(c.Stats); len(lines) > 0 {
		b.WriteString("\nYOUR " + strings.ToUpper(periodNoun(c.Period)) + " IN NUMBERS\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
	}
	b.WriteString("\n-- \n")
	b.WriteString("You're getting this because you turned on the " + c.Period.Frequency + " digest on your OnScreen account.\n")
	if c.UnsubscribeURL != "" {
		b.WriteString("Unsubscribe: " + c.UnsubscribeURL + "\n")
	}
	return b.String()
}

func periodNoun(p Period) string {
	if p.Frequency == FrequencyMonthly {
		return "month"
	}
	return "week"
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
	heading := "New in " + c.Period.Label
	fmt.Fprintf(&b, `<tr><td style="font-size:18px;font-weight:600;color:%s;padding-bottom:6px">%s</td></tr>`, cText, e(heading))
	intro := "Hi " + c.Username + ", here's what was added to your libraries."
	if c.Empty() {
		intro = "Hi " + c.Username + ", nothing new was added to your libraries in " + c.Period.Label + "."
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
			fmt.Fprintf(&b, `<tr><td style="font-size:13px;color:%s;padding-top:6px">…and %d more</td></tr>`, cMuted, s.More)
		}
	}

	if len(c.ContinueWatching) > 0 {
		fmt.Fprintf(&b, `<tr><td style="font-size:15px;font-weight:600;color:%s;padding:24px 0 10px">Continue watching</td></tr>`, cText)
		b.WriteString(`<tr><td>`)
		c.posterGrid(&b, c.ContinueWatching)
		b.WriteString(`</td></tr>`)
	}

	if lines := statsLines(c.Stats); len(lines) > 0 {
		fmt.Fprintf(&b, `<tr><td style="font-size:15px;font-weight:600;color:%s;padding:24px 0 8px">Your %s in numbers</td></tr>`, cText, periodNoun(c.Period))
		fmt.Fprintf(&b, `<tr><td style="font-size:14px;color:%s;line-height:1.8">`, cMuted)
		for i, l := range lines {
			if i > 0 {
				b.WriteString(` &middot; `)
			}
			b.WriteString(e(l))
		}
		b.WriteString(`</td></tr>`)
	}

	if l := c.link("/"); l != "" {
		fmt.Fprintf(&b, `<tr><td style="padding:28px 0 0"><a href="%s" style="display:inline-block;padding:12px 28px;background:%s;color:#fff;text-decoration:none;border-radius:8px;font-weight:600;font-size:15px">Open OnScreen</a></td></tr>`, e(l), cAccent)
	}

	footer := "You're getting this because you turned on the " + c.Period.Frequency + " digest on your OnScreen account."
	fmt.Fprintf(&b, `<tr><td style="padding-top:28px;font-size:12px;color:%s;line-height:1.6;border-top:1px solid %s">%s`, cFaint, cBorder, e(footer))
	if c.UnsubscribeURL != "" {
		fmt.Fprintf(&b, ` <a href="%s" style="color:%s">Unsubscribe</a>`, e(c.UnsubscribeURL), cMuted)
	}
	if l := c.link("/account"); l != "" {
		fmt.Fprintf(&b, ` &middot; <a href="%s" style="color:%s">Email settings</a>`, e(l), cMuted)
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
