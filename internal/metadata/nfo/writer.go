package nfo

// Writing NFO sidecars: the reverse of the parsers above, for libraries with
// "Save metadata as NFO files next to media" turned on. The output is plain
// Kodi XML that this package's own parsers (and Kodi, Jellyfin, Emby) read
// back.
//
// Ownership: every file written here carries Marker, and WriteFile only
// ever replaces a file that carries it. An NFO someone else wrote — Kodi,
// Radarr, Sonarr, tinyMediaManager, the operator by hand — is never touched.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Marker is the XML comment that identifies an NFO OnScreen wrote.
const Marker = "<!-- written by OnScreen -->"

// TempSuffix ends the name of the temporary file a write goes through before
// it is renamed into place. The library watcher ignores such files.
const TempSuffix = ".onscreen-tmp"

// ErrNotOurs is returned by WriteFile when the target exists and wasn't
// written by OnScreen; it is left as it is.
var ErrNotOurs = errors.New("nfo: existing file was not written by OnScreen")

// markerScanBytes is how far into an existing file WriteFile looks for
// Marker; ours puts it on the second line.
const markerScanBytes = 4096

type outUniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type outMovie struct {
	XMLName       xml.Name      `xml:"movie"`
	Title         string        `xml:"title"`
	OriginalTitle string        `xml:"originaltitle,omitempty"`
	SortTitle     string        `xml:"sorttitle,omitempty"`
	Year          int           `xml:"year,omitempty"`
	Rating        string        `xml:"rating,omitempty"`
	Plot          string        `xml:"plot,omitempty"`
	Tagline       string        `xml:"tagline,omitempty"`
	Runtime       int           `xml:"runtime,omitempty"`
	MPAA          string        `xml:"mpaa,omitempty"`
	UniqueIDs     []outUniqueID `xml:"uniqueid"`
	Genres        []string      `xml:"genre"`
	Studios       []string      `xml:"studio"`
	Premiered     string        `xml:"premiered,omitempty"`
}

type outShow struct {
	XMLName       xml.Name      `xml:"tvshow"`
	Title         string        `xml:"title"`
	OriginalTitle string        `xml:"originaltitle,omitempty"`
	SortTitle     string        `xml:"sorttitle,omitempty"`
	Year          int           `xml:"year,omitempty"`
	Rating        string        `xml:"rating,omitempty"`
	Plot          string        `xml:"plot,omitempty"`
	MPAA          string        `xml:"mpaa,omitempty"`
	UniqueIDs     []outUniqueID `xml:"uniqueid"`
	Genres        []string      `xml:"genre"`
	Studios       []string      `xml:"studio"`
	Premiered     string        `xml:"premiered,omitempty"`
}

type outEpisode struct {
	XMLName   xml.Name      `xml:"episodedetails"`
	Title     string        `xml:"title"`
	Season    *int          `xml:"season"`
	Episode   *int          `xml:"episode"`
	Rating    string        `xml:"rating,omitempty"`
	Plot      string        `xml:"plot,omitempty"`
	Runtime   int           `xml:"runtime,omitempty"`
	UniqueIDs []outUniqueID `xml:"uniqueid"`
	Aired     string        `xml:"aired,omitempty"`
}

// MarshalMovie renders m as a movie NFO (Kodi's <movie>), marked as ours.
func MarshalMovie(m *Movie) ([]byte, error) {
	return render(outMovie{
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		SortTitle:     sortTitleIfDifferent(m.SortTitle, m.Title),
		Year:          m.Year,
		Rating:        formatRating(m.Rating),
		Plot:          m.Plot,
		Tagline:       m.Tagline,
		Runtime:       m.RuntimeMin,
		MPAA:          m.MPAA,
		UniqueIDs:     uniqueIDs(m.TMDBID, m.IMDBID, m.TVDBID, "tmdb"),
		Genres:        m.Genres,
		Studios:       m.Studios,
		Premiered:     formatDate(m.Premiered),
	})
}

// MarshalShow renders s as a tvshow.nfo, marked as ours.
func MarshalShow(s *Show) ([]byte, error) {
	return render(outShow{
		Title:         s.Title,
		OriginalTitle: s.OriginalTitle,
		SortTitle:     sortTitleIfDifferent(s.SortTitle, s.Title),
		Year:          s.Year,
		Rating:        formatRating(s.Rating),
		Plot:          s.Plot,
		MPAA:          s.ContentRating,
		UniqueIDs:     uniqueIDs(s.TMDBID, s.IMDBID, s.TVDBID, "tvdb"),
		Genres:        s.Genres,
		Studios:       s.Studios,
		Premiered:     formatDate(s.Premiered),
	})
}

// MarshalEpisode renders e as an episode NFO (<episodedetails>), marked as
// ours. Season 0 (specials) is written; a zero episode number is not.
func MarshalEpisode(e *Episode) ([]byte, error) {
	out := outEpisode{
		Title:     e.Title,
		Rating:    formatRating(e.Rating),
		Plot:      e.Plot,
		Runtime:   e.RuntimeMin,
		UniqueIDs: uniqueIDs(e.TMDBID, e.IMDBID, e.TVDBID, "tmdb"),
		Aired:     formatDate(e.Aired),
	}
	season, episode := e.Season, e.Episode
	out.Season = &season
	if episode > 0 {
		out.Episode = &episode
	}
	return render(out)
}

func render(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("nfo: marshal: %w", err)
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>` + "\n")
	b.WriteString(Marker + "\n")
	b.Write(body)
	b.WriteString("\n")
	return b.Bytes(), nil
}

// uniqueIDs lists the provider ids that are set; def names the default one.
func uniqueIDs(tmdb int, imdb string, tvdb int, def string) []outUniqueID {
	var out []outUniqueID
	if tvdb > 0 {
		out = append(out, outUniqueID{Type: "tvdb", Value: strconv.Itoa(tvdb)})
	}
	if tmdb > 0 {
		out = append(out, outUniqueID{Type: "tmdb", Value: strconv.Itoa(tmdb)})
	}
	if imdb = strings.TrimSpace(imdb); imdb != "" {
		out = append(out, outUniqueID{Type: "imdb", Value: imdb})
	}
	// The preferred id is the default; else the first one present.
	for i := range out {
		if out[i].Type == def {
			out[i].Default = true
			return out
		}
	}
	if len(out) > 0 {
		out[0].Default = true
	}
	return out
}

func sortTitleIfDifferent(sort, title string) string {
	if sort == "" || sort == title {
		return ""
	}
	return sort
}

func formatRating(r float64) string {
	if r <= 0 {
		return ""
	}
	return strconv.FormatFloat(r, 'f', 1, 64)
}

func formatDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// IsOurs reports whether the NFO at path was written by OnScreen. A missing
// or unreadable file is not ours.
func IsOurs(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, markerScanBytes))
	return bytes.Contains(head, []byte(Marker))
}

// WriteFile writes data (which must carry Marker) to path when path doesn't
// exist or holds an NFO OnScreen wrote; otherwise it returns ErrNotOurs and
// leaves the file alone. It writes a temporary file in the same directory
// and renames it into place, so a reader never sees half a file. written is
// false when the file already held exactly data (nothing is touched then, so
// an unchanged refresh doesn't disturb the file's mtime or a folder watcher).
func WriteFile(path string, data []byte) (written bool, err error) {
	if !bytes.Contains(data, []byte(Marker)) {
		return false, errors.New("nfo: refusing to write an NFO without the OnScreen marker")
	}
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !bytes.Contains(existing[:min(len(existing), markerScanBytes)], []byte(Marker)) {
			return false, ErrNotOurs
		}
		if bytes.Equal(existing, data) {
			return false, nil
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, fmt.Errorf("nfo: read %s: %w", path, err)
	}

	dir, base := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, "."+base+".*"+TempSuffix)
	if err != nil {
		return false, fmt.Errorf("nfo: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("nfo: write %s: %w", tmpPath, werr)
	}
	// A file that appeared since the check above, and isn't ours, wins.
	if _, statErr := os.Stat(path); statErr == nil && existing == nil && !IsOurs(path) {
		_ = os.Remove(tmpPath)
		return false, ErrNotOurs
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("nfo: rename into %s: %w", path, err)
	}
	return true, nil
}
