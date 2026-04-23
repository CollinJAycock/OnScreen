package livetv

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

// XMLTVChannel is one <channel> element from the source. We keep the
// raw display-names + lcn so the auto-matcher can try several
// strategies before giving up on a channel.
type XMLTVChannel struct {
	ID           string
	DisplayNames []string // first is the canonical name
	LCN          string   // logical channel number, if present
	IconURL      string
}

// XMLTVProgram is one <programme> element. Times are normalized to UTC
// at parse time so the upsert layer doesn't have to think about TZs.
type XMLTVProgram struct {
	ChannelID       string
	Title           string
	Subtitle        string
	Description     string
	Category        []string
	Rating          string
	SeasonNum       *int32
	EpisodeNum      *int32
	OriginalAirDate *time.Time
	StartsAt        time.Time
	EndsAt          time.Time
}

// SourceProgramID synthesizes a stable key for the upsert. XMLTV doesn't
// carry an explicit program ID — convention is to use channel + start
// time, which is unique because no channel airs two programs at once.
// We hash-format it (HHMMSS) so the column stays human-grep-able.
func (p XMLTVProgram) SourceProgramID() string {
	return p.ChannelID + "@" + p.StartsAt.UTC().Format("20060102T150405Z")
}

// XMLTVDocument is the top-level XML structure we decode into. Only the
// fields we care about are mapped; the encoder ignores the rest.
type XMLTVDocument struct {
	XMLName    xml.Name        `xml:"tv"`
	Channels   []xmltvChannel  `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

type xmltvChannel struct {
	ID           string             `xml:"id,attr"`
	DisplayNames []string           `xml:"display-name"`
	Icons        []xmltvIcon        `xml:"icon"`
	LCN          string             `xml:"lcn"`
}

type xmltvIcon struct {
	Src string `xml:"src,attr"`
}

type xmltvProgramme struct {
	Start    string             `xml:"start,attr"`
	Stop     string             `xml:"stop,attr"`
	Channel  string             `xml:"channel,attr"`
	Titles   []string           `xml:"title"`
	Subs     []string           `xml:"sub-title"`
	Descs    []string           `xml:"desc"`
	Cats     []string           `xml:"category"`
	Date     string             `xml:"date"` // original air date as YYYYMMDD
	Episodes []xmltvEpisodeNum  `xml:"episode-num"`
	Ratings  []xmltvRating      `xml:"rating"`
}

type xmltvEpisodeNum struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}

type xmltvRating struct {
	System string `xml:"system,attr"`
	Value  string `xml:"value"`
}

// xmltvClient is the http.Client used to fetch XMLTV URLs. Declared as
// a package var (not inlined) so tests can swap in a permissive client
// that reaches httptest's loopback binds. Production value blocks
// private/loopback/link-local addresses to prevent admin-initiated SSRF.
var xmltvClient = safehttp.NewClient(safehttp.DialPolicy{}, 60*time.Second)

// FetchXMLTV pulls an XMLTV document from a URL or file path. The
// returned reader yields decompressed XML — gzip is auto-detected by
// peeking at the first two bytes (0x1F 0x8B) so callers don't have to
// know whether the source is `.xml`, `.xml.gz`, or HTTP-compressed.
//
// HTTP timeouts are generous (60s) because XMLTV files can be tens of MB
// for a 14-day national grid and providers are sometimes slow.
//
// IMPORTANT: do NOT set Accept-Encoding here. Go's transport
// auto-decompresses gzip ONLY when the caller leaves Accept-Encoding
// empty — if we set it ourselves, the transport assumes we want to
// handle gzip ourselves and passes raw bytes through. Combined with
// .gz-suffixed file URLs (which the server returns as
// application/octet-stream, NOT Content-Encoding: gzip), this means we
// always see raw gzip bytes regardless of how the source serves it.
// The peek-and-wrap path below handles both cases uniformly.
//
// Caller is responsible for closing the returned reader.
func FetchXMLTV(ctx context.Context, source string) (io.ReadCloser, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		resp, err := xmltvClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("xmltv fetch: status %d", resp.StatusCode)
		}
		return maybeGunzip(resp.Body)
	}
	f, err := os.Open(source)
	if err != nil {
		return nil, fmt.Errorf("xmltv open: %w", err)
	}
	return maybeGunzip(f)
}

// maybeGunzip peeks at the first two bytes of `r` and returns either a
// gzip-decompressing reader (if the magic 0x1F 0x8B is present) or the
// original reader. Closing the returned reader closes the underlying
// reader either way.
func maybeGunzip(r io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	header, err := br.Peek(2)
	if err != nil {
		// Empty or one-byte body — return as-is and let the XML parser
		// produce a sensible error.
		return wrapReadCloser(br, r), nil
	}
	if header[0] == 0x1F && header[1] == 0x8B {
		gz, err := gzip.NewReader(br)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("xmltv gunzip: %w", err)
		}
		return &gzipReadCloser{Reader: gz, gz: gz, underlying: r}, nil
	}
	return wrapReadCloser(br, r), nil
}

// wrapReadCloser pairs a *bufio.Reader (the read side, after Peek) with
// the original ReadCloser (the close side). Buffer-then-close ordering
// matters: if we returned just the bufio.Reader we'd leak the underlying
// HTTP response body or file handle.
type bufferedReadCloser struct {
	io.Reader
	closer io.Closer
}

func (b *bufferedReadCloser) Close() error { return b.closer.Close() }

func wrapReadCloser(buf io.Reader, closer io.Closer) io.ReadCloser {
	return &bufferedReadCloser{Reader: buf, closer: closer}
}

// gzipReadCloser closes both the gzip reader and the underlying source.
type gzipReadCloser struct {
	io.Reader
	gz         *gzip.Reader
	underlying io.Closer
}

func (g *gzipReadCloser) Close() error {
	g.gz.Close()
	return g.underlying.Close()
}

// ParseXMLTV reads an XMLTV document and returns the parsed channels +
// programmes. Programmes whose start/stop fail to parse are silently
// skipped (a single bad row in a 50,000-row grid shouldn't fail the
// whole pull) — the count is exposed via the returned skipped counter.
func ParseXMLTV(r io.Reader) (channels []XMLTVChannel, programs []XMLTVProgram, skipped int, err error) {
	dec := xml.NewDecoder(r)
	// XMLTV spec doesn't strictly require an encoding declaration; some
	// sources serve UTF-8 without one. Permissive charset reader avoids
	// a hard error on those.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }

	var doc XMLTVDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, 0, fmt.Errorf("xmltv decode: %w", err)
	}

	channels = make([]XMLTVChannel, 0, len(doc.Channels))
	for _, c := range doc.Channels {
		entry := XMLTVChannel{
			ID:           c.ID,
			DisplayNames: c.DisplayNames,
			LCN:          c.LCN,
		}
		if len(c.Icons) > 0 {
			entry.IconURL = c.Icons[0].Src
		}
		channels = append(channels, entry)
	}

	programs = make([]XMLTVProgram, 0, len(doc.Programmes))
	for _, p := range doc.Programmes {
		start, err := parseXMLTVTime(p.Start)
		if err != nil {
			skipped++
			continue
		}
		stop, err := parseXMLTVTime(p.Stop)
		if err != nil {
			skipped++
			continue
		}
		out := XMLTVProgram{
			ChannelID: p.Channel,
			StartsAt:  start.UTC(),
			EndsAt:    stop.UTC(),
		}
		if len(p.Titles) > 0 {
			out.Title = p.Titles[0]
		}
		if len(p.Subs) > 0 {
			out.Subtitle = p.Subs[0]
		}
		if len(p.Descs) > 0 {
			out.Description = p.Descs[0]
		}
		out.Category = append(out.Category, p.Cats...)
		// Ratings: take the first one regardless of system. Rich UI for
		// per-system filtering can come later.
		if len(p.Ratings) > 0 {
			out.Rating = p.Ratings[0].Value
		}
		// Episode-num: prefer xmltv_ns format ("0.0.0/1"), fall back to
		// onscreen format ("S5E12") or anything else as raw text.
		out.SeasonNum, out.EpisodeNum = parseEpisodeNum(p.Episodes)
		// date: 8 chars YYYYMMDD = original air date (movies) / first-aired.
		if len(p.Date) >= 8 {
			if d, err := time.Parse("20060102", p.Date[:8]); err == nil {
				out.OriginalAirDate = &d
			}
		}
		programs = append(programs, out)
	}
	return channels, programs, skipped, nil
}

// parseXMLTVTime accepts the XMLTV time format: "YYYYMMDDHHMMSS" with
// an optional " ±HHMM" timezone suffix. UTC offset is mandatory in the
// spec but plenty of real-world feeds omit it — we treat naked
// timestamps as UTC since that's what most generators actually mean.
func parseXMLTVTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	// Fast path: timestamp + offset.
	if t, err := time.Parse("20060102150405 -0700", s); err == nil {
		return t, nil
	}
	// Accept a numeric-only timestamp as UTC. Some sources use "+0000".
	if t, err := time.Parse("20060102150405", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

// parseEpisodeNum walks the <episode-num> children and extracts season +
// episode where possible. Recognized systems:
//   - xmltv_ns: "S.E.P/T" (zero-indexed). Takes S+1, E+1.
//   - onscreen / common: "S05E12" or "5x12".
//   - dd_progid / others: ignored — we don't need them.
func parseEpisodeNum(eps []xmltvEpisodeNum) (*int32, *int32) {
	for _, e := range eps {
		switch strings.ToLower(e.System) {
		case "xmltv_ns":
			s, ep, ok := parseXMLTVNs(e.Value)
			if ok {
				return s, ep
			}
		case "onscreen", "":
			s, ep, ok := parseSEFormat(e.Value)
			if ok {
				return s, ep
			}
		}
	}
	// Last-ditch: try any value as either format.
	for _, e := range eps {
		if s, ep, ok := parseXMLTVNs(e.Value); ok {
			return s, ep
		}
		if s, ep, ok := parseSEFormat(e.Value); ok {
			return s, ep
		}
	}
	return nil, nil
}

// parseXMLTVNs parses "S.E.P/T" → (S+1, E+1). Examples:
//   "0.0.0/1"  → S1 E1
//   "4.11."    → S5 E12
//   ". . . "   → nil (no info)
func parseXMLTVNs(v string) (*int32, *int32, bool) {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return nil, nil, false
	}
	parseTok := func(tok string) (*int32, bool) {
		tok = strings.TrimSpace(tok)
		// Strip the "/total" subpart if present.
		if i := strings.Index(tok, "/"); i >= 0 {
			tok = tok[:i]
		}
		if tok == "" {
			return nil, false
		}
		n, err := strconv.Atoi(tok)
		if err != nil {
			return nil, false
		}
		v := int32(n + 1) // xmltv_ns is zero-indexed
		return &v, true
	}
	s, sok := parseTok(parts[0])
	e, eok := parseTok(parts[1])
	if !sok && !eok {
		return nil, nil, false
	}
	return s, e, true
}

// parseSEFormat parses "S05E12", "s5e12", "5x12" → (5, 12).
func parseSEFormat(v string) (*int32, *int32, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return nil, nil, false
	}
	// Find the separator: 'e' or 'x'.
	var sepIdx int
	if i := strings.Index(v, "e"); i > 0 && (v[0] == 's' || isDigit(v[0])) {
		sepIdx = i
	} else if i := strings.Index(v, "x"); i > 0 {
		sepIdx = i
	} else {
		return nil, nil, false
	}
	left, right := v[:sepIdx], v[sepIdx+1:]
	left = strings.TrimPrefix(left, "s")
	s, err := strconv.Atoi(left)
	if err != nil {
		return nil, nil, false
	}
	e, err := strconv.Atoi(right)
	if err != nil {
		return nil, nil, false
	}
	sv, ev := int32(s), int32(e)
	return &sv, &ev, true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
