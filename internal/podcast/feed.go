package podcast

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Feed is a parsed podcast RSS feed: the channel and its audio episodes.
type Feed struct {
	Title       string
	Author      string
	Description string
	ImageURL    string
	SiteURL     string
	Language    string
	Episodes    []Episode
	// Skipped counts items left out: no enclosure, or a video one.
	Skipped int
}

// Episode is one audio item of a feed.
type Episode struct {
	GUID            string
	Title           string
	Description     string
	PublishedAt     *time.Time
	DurationMS      *int64
	EnclosureURL    string
	EnclosureType   string
	EnclosureLength *int64
	Season          *int32
	Episode         *int32
	ImageURL        string
}

// ErrNotAFeed is returned when the document isn't an RSS podcast feed.
var ErrNotAFeed = errors.New("not an RSS podcast feed")

// Namespaces of the extension elements a podcast feed uses. iTunes' is
// matched without regard to case: older feeds spell it
// "http://www.itunes.com/DTDs/Podcast-1.0.dtd".
const (
	nsITunes  = "http://www.itunes.com/dtds/podcast-1.0.dtd"
	nsContent = "http://purl.org/rss/1.0/modules/content/"
)

// Size caps on stored text. Show notes can be very long; the item summary
// they become doesn't need to be.
const (
	maxTitleLen       = 500
	maxDescriptionLen = 20000
)

// rssDoc, rssChannel and rssItem keep every child element as a raw element
// and pick fields by namespace + name afterwards. encoding/xml matches an
// untagged-namespace field to an element of any namespace, so a plain
// `xml:"title"` field would also take <itunes:title> and keep whichever came
// last.
type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
	Elems []rawElem `xml:",any"`
}

type rssItem struct {
	Elems []rawElem `xml:",any"`
}

type rawElem struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Elems   []rawElem  `xml:",any"`
}

func (e rawElem) is(space, local string) bool {
	if e.XMLName.Local != local {
		return false
	}
	switch space {
	case "":
		return e.XMLName.Space == ""
	case nsITunes:
		return strings.EqualFold(e.XMLName.Space, nsITunes)
	}
	return e.XMLName.Space == space
}

func (e rawElem) attr(name string) string {
	for _, a := range e.Attrs {
		if a.Name.Local == name {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func (e rawElem) text() string { return strings.TrimSpace(e.Text) }

// find returns the text of the first element named (space, local).
func find(elems []rawElem, space, local string) string {
	for _, e := range elems {
		if e.is(space, local) {
			if t := e.text(); t != "" {
				return t
			}
		}
	}
	return ""
}

func findElem(elems []rawElem, space, local string) (rawElem, bool) {
	for _, e := range elems {
		if e.is(space, local) {
			return e, true
		}
	}
	return rawElem{}, false
}

// Parse reads an RSS 2.0 podcast feed. base resolves relative URLs (the
// feed's own URL). Items without an audio enclosure are counted in Skipped.
func Parse(r io.Reader, base *url.URL) (*Feed, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	// Lenient about the HTML entities (&nbsp;, &eacute;) feeds use without
	// declaring. Not AutoClose: RSS's <link> has content, unlike HTML's.
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	// Anything that doesn't decode as <rss> — an HTML page, an Atom feed,
	// broken XML — isn't a feed this package reads.
	var doc rssDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAFeed, err)
	}
	ch := doc.Channel
	f := &Feed{
		Title:       clip(plainText(find(ch.Elems, "", "title")), maxTitleLen),
		Author:      clip(plainText(firstNonEmpty(find(ch.Elems, nsITunes, "author"), find(ch.Elems, "", "managingEditor"))), maxTitleLen),
		Description: clip(plainText(firstNonEmpty(find(ch.Elems, "", "description"), find(ch.Elems, nsITunes, "summary"))), maxDescriptionLen),
		SiteURL:     resolveURL(base, find(ch.Elems, "", "link")),
		Language:    clip(find(ch.Elems, "", "language"), 35),
	}
	if img, ok := findElem(ch.Elems, nsITunes, "image"); ok {
		f.ImageURL = resolveURL(base, img.attr("href"))
	}
	if f.ImageURL == "" {
		if img, ok := findElem(ch.Elems, "", "image"); ok {
			f.ImageURL = resolveURL(base, find(img.Elems, "", "url"))
		}
	}
	if f.Title == "" {
		return nil, fmt.Errorf("%w: the channel has no title", ErrNotAFeed)
	}

	seen := make(map[string]bool, len(ch.Items))
	for _, it := range ch.Items {
		ep, ok := parseItem(it, base)
		if !ok {
			f.Skipped++
			continue
		}
		// A feed that repeats a guid keeps the first (newest, in feed order).
		if seen[ep.GUID] {
			continue
		}
		seen[ep.GUID] = true
		f.Episodes = append(f.Episodes, ep)
	}
	return f, nil
}

func parseItem(it rssItem, base *url.URL) (Episode, bool) {
	enc, ok := findElem(it.Elems, "", "enclosure")
	if !ok {
		return Episode{}, false
	}
	encURL := resolveURL(base, enc.attr("url"))
	if encURL == "" {
		return Episode{}, false
	}
	encType := strings.ToLower(enc.attr("type"))
	if !isAudioEnclosure(encURL, encType) {
		return Episode{}, false
	}
	ep := Episode{
		EnclosureURL:  encURL,
		EnclosureType: encType,
		GUID:          clip(find(it.Elems, "", "guid"), 1000),
		Title:         clip(plainText(firstNonEmpty(find(it.Elems, "", "title"), find(it.Elems, nsITunes, "title"))), maxTitleLen),
	}
	if ep.GUID == "" {
		ep.GUID = encURL
	}
	if ep.Title == "" {
		ep.Title = "Untitled episode"
	}
	if n, err := strconv.ParseInt(enc.attr("length"), 10, 64); err == nil && n > 0 {
		ep.EnclosureLength = &n
	}
	ep.Description = clip(plainText(firstNonEmpty(
		find(it.Elems, nsContent, "encoded"),
		find(it.Elems, "", "description"),
		find(it.Elems, nsITunes, "summary"),
	)), maxDescriptionLen)
	if t, ok := parseDate(find(it.Elems, "", "pubDate")); ok {
		ep.PublishedAt = &t
	}
	if d, ok := parseDuration(find(it.Elems, nsITunes, "duration")); ok {
		ep.DurationMS = &d
	}
	ep.Season = parsePositive(find(it.Elems, nsITunes, "season"))
	ep.Episode = parsePositive(find(it.Elems, nsITunes, "episode"))
	if img, ok := findElem(it.Elems, nsITunes, "image"); ok {
		ep.ImageURL = resolveURL(base, img.attr("href"))
	}
	return ep, true
}

// audioMIMEExtensions maps enclosure MIME types to the file extension an
// episode is saved with, for URLs without a usable extension.
var audioMIMEExtensions = map[string]string{
	"audio/mpeg":   ".mp3",
	"audio/mp3":    ".mp3",
	"audio/x-mp3":  ".mp3",
	"audio/mpeg3":  ".mp3",
	"audio/mp4":    ".m4a",
	"audio/x-m4a":  ".m4a",
	"audio/m4a":    ".m4a",
	"audio/x-m4b":  ".m4b",
	"audio/aac":    ".aac",
	"audio/x-aac":  ".aac",
	"audio/aacp":   ".aac",
	"audio/ogg":    ".ogg",
	"audio/vorbis": ".ogg",
	"audio/opus":   ".opus",
	"audio/flac":   ".flac",
	"audio/x-flac": ".flac",
}

// audioExtensions are the episode file types the podcast scanner reads (its
// audio set, plus .oga, saved as .ogg).
var audioExtensions = map[string]bool{
	".mp3": true, ".m4a": true, ".m4b": true, ".aac": true,
	".ogg": true, ".oga": true, ".opus": true, ".flac": true,
}

// isAudioEnclosure accepts an audio MIME type, or — for the feeds that send
// application/octet-stream or nothing — an audio file extension.
func isAudioEnclosure(rawURL, mimeType string) bool {
	mimeType, _, _ = strings.Cut(mimeType, ";")
	mimeType = strings.TrimSpace(mimeType)
	if strings.HasPrefix(mimeType, "audio/") {
		return true
	}
	if strings.HasPrefix(mimeType, "video/") {
		return false
	}
	return audioExtensions[urlExt(rawURL)]
}

// EpisodeExt picks the file extension an episode is saved with: the URL's,
// when it's an audio type the scanner reads; else the MIME type's; else .mp3.
func EpisodeExt(rawURL, mimeType string) string {
	if ext := urlExt(rawURL); audioExtensions[ext] {
		if ext == ".oga" {
			return ".ogg"
		}
		return ext
	}
	mimeType, _, _ = strings.Cut(strings.ToLower(mimeType), ";")
	if ext, ok := audioMIMEExtensions[strings.TrimSpace(mimeType)]; ok {
		return ext
	}
	return ".mp3"
}

func urlExt(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(path.Ext(u.Path))
}

// resolveURL returns ref as an absolute http(s) URL, resolved against base
// when relative; "" when it isn't one.
func resolveURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

// dateLayouts are the pubDate forms seen in the wild: RFC 822/1123 with and
// without the weekday, seconds or a numeric zone, and the ISO dates some
// generators write instead.
var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST",
	"Mon, 02 Jan 2006 15:04 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"02 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 06 15:04:05 -0700",
	"Mon, 2 January 2006 15:04:05 -0700",
	"Monday, 2 Jan 2006 15:04:05 -0700",
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDate(s string) (time.Time, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return time.Time{}, false
	}
	// "UT" and "Z" are RFC 822 zones Go's MST layout doesn't know.
	if strings.HasSuffix(s, " UT") || strings.HasSuffix(s, " Z") {
		s = s[:strings.LastIndexByte(s, ' ')] + " +0000"
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// parseDuration reads itunes:duration: seconds ("3723", "3723.5") or
// clock form ("1:02:03", "62:03").
func parseDuration(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	var total float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return 0, false
		}
		if i < len(parts)-1 && v != float64(int64(v)) {
			return 0, false
		}
		total = total*60 + v
	}
	ms := int64(total * 1000)
	if ms <= 0 {
		return 0, false
	}
	return ms, true
}

func parsePositive(s string) *int32 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil || v <= 0 {
		return nil
	}
	n := int32(v)
	return &n
}

// blockTags start a new line in plainText.
var blockTags = map[string]bool{
	"p": true, "br": true, "div": true, "li": true, "ul": true, "ol": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "tr": true, "hr": true,
}

// plainText turns a feed's HTML (show notes, titles with entities) into plain
// text: tags dropped, entities decoded, block elements as line breaks (a
// blank line between paragraphs), scripts and styles left out.
func plainText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return strings.TrimSpace(collapseSpaces(s))
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return tidyLines(b.String())
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" {
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if blockTags[tag] {
				b.WriteByte('\n')
			}
		case html.CommentToken, html.DoctypeToken:
			// not text
		}
	}
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// tidyLines collapses spaces within lines and runs of blank lines.
func tidyLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, l := range lines {
		l = collapseSpaces(l)
		if l == "" {
			if len(out) > 0 && !blank {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, l)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// clip shortens s to at most n bytes without splitting a UTF-8 sequence.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
