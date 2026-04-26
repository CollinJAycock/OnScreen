package nfo

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzParseMovie exercises ParseMovie against adversarial XML. Movie
// NFO files are user-curated for years and we accept anything from
// MediaInfo, MakeMKV, NFO Builder, hand-written, etc. — the parser
// must not panic on any byte sequence.
//
// Run as: go test -fuzz=FuzzParseMovie -fuzztime=30s ./internal/metadata/nfo/
func FuzzParseMovie(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>The Matrix</title>
  <year>1999</year>
  <plot>A computer hacker learns…</plot>
  <runtime>136</runtime>
  <mpaa>R</mpaa>
  <rating>8.7</rating>
  <genre>Sci-Fi</genre>
  <genre>Action</genre>
  <director>Lana Wachowski</director>
  <uniqueid type="tmdb">603</uniqueid>
  <uniqueid type="imdb">tt0133093</uniqueid>
</movie>`))
	f.Add([]byte(``))
	f.Add([]byte(`<movie></movie>`))
	f.Add([]byte(`<movie><year>not-a-year</year></movie>`))
	f.Add([]byte(`<movie><runtime>9999999999</runtime></movie>`))
	f.Add([]byte(`<movie><rating>nan</rating></movie>`))
	f.Add([]byte(`<movie><premiered>not-a-date</premiered></movie>`))
	f.Add([]byte(`<movie><uniqueid type=""></uniqueid></movie>`))
	f.Add([]byte(strings.Repeat(`<genre>x</genre>`, 1000)))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseMovie(bytes.NewReader(data))
	})
}

// FuzzParseShow — same rationale for tvshow.nfo.
func FuzzParseShow(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<tvshow>
  <title>Severance</title>
  <year>2022</year>
  <status>Continuing</status>
  <mpaa>TV-MA</mpaa>
  <uniqueid type="tvdb">371980</uniqueid>
</tvshow>`))
	f.Add([]byte(``))
	f.Add([]byte(`<tvshow><year></year></tvshow>`))
	f.Add([]byte(`<tvshow><actor><name></name></actor></tvshow>`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseShow(bytes.NewReader(data))
	})
}

// FuzzParseEpisode — same rationale for per-episode .nfo.
func FuzzParseEpisode(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<episodedetails>
  <title>Pilot</title>
  <season>1</season>
  <episode>1</episode>
  <plot>Mark begins his first day…</plot>
  <aired>2022-02-18</aired>
</episodedetails>`))
	f.Add([]byte(``))
	f.Add([]byte(`<episodedetails><season>not-a-num</season></episodedetails>`))
	f.Add([]byte(`<episodedetails><aired>0000-00-00</aired></episodedetails>`))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseEpisode(bytes.NewReader(data))
	})
}
