package nfo

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMovieRoundTrip(t *testing.T) {
	prem := time.Date(1979, 5, 25, 0, 0, 0, 0, time.UTC)
	in := &Movie{
		Title:         "Alien",
		OriginalTitle: "Alien",
		SortTitle:     "Alien 1",
		Year:          1979,
		Plot:          "In space, no one can hear you scream & <run>.",
		Tagline:       "In space no one can hear you scream.",
		RuntimeMin:    117,
		MPAA:          "R",
		Rating:        8.5,
		Genres:        []string{"Horror", "Science Fiction"},
		Studios:       []string{"Brandywine Productions", "20th Century Fox"},
		Premiered:     &prem,
		TMDBID:        348,
		IMDBID:        "tt0078748",
	}
	data, err := MarshalMovie(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(Marker)) {
		t.Fatalf("no marker:\n%s", data)
	}
	if !bytes.Contains(data, []byte(`<uniqueid type="tmdb" default="true">348</uniqueid>`)) {
		t.Errorf("tmdb uniqueid not the default:\n%s", data)
	}
	got, err := ParseMovie(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse back: %v\n%s", err, data)
	}
	if got.Title != in.Title || got.SortTitle != in.SortTitle || got.Year != 1979 || got.Plot != in.Plot ||
		got.Tagline != in.Tagline || got.RuntimeMin != 117 || got.MPAA != "R" || got.Rating != 8.5 ||
		got.TMDBID != 348 || got.IMDBID != "tt0078748" || got.Premiered == nil || !got.Premiered.Equal(prem) {
		t.Errorf("round trip = %+v", got)
	}
	if !reflect.DeepEqual(got.Genres, in.Genres) || !reflect.DeepEqual(got.Studios, in.Studios) {
		t.Errorf("genres %v studios %v", got.Genres, got.Studios)
	}
}

func TestShowAndEpisodeRoundTrip(t *testing.T) {
	data, err := MarshalShow(&Show{Title: "Breaking Bad", Year: 2008, ContentRating: "TV-MA",
		Studios: []string{"AMC"}, TMDBID: 1396, TVDBID: 81189, IMDBID: "tt0903747"})
	if err != nil {
		t.Fatal(err)
	}
	show, err := ParseShow(bytes.NewReader(data))
	if err != nil || show.Title != "Breaking Bad" || show.TVDBID != 81189 || show.TMDBID != 1396 ||
		show.ContentRating != "TV-MA" || len(show.Studios) != 1 || show.Studios[0] != "AMC" {
		t.Errorf("show = %+v, %v\n%s", show, err, data)
	}
	if !bytes.Contains(data, []byte(`<uniqueid type="tvdb" default="true">81189</uniqueid>`)) {
		t.Errorf("tvdb should be a show's default id:\n%s", data)
	}

	aired := time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC)
	data, err = MarshalEpisode(&Episode{Title: "Pilot", Season: 0, Episode: 1, Aired: &aired, RuntimeMin: 58})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := ParseEpisode(bytes.NewReader(data))
	if err != nil || ep.Title != "Pilot" || ep.Season != 0 || ep.Episode != 1 || ep.RuntimeMin != 58 ||
		ep.Aired == nil || !ep.Aired.Equal(aired) {
		t.Errorf("episode = %+v, %v", ep, err)
	}
	if !bytes.Contains(data, []byte("<season>0</season>")) {
		t.Errorf("season 0 (specials) must be written:\n%s", data)
	}
}

func TestWriteFileOwnership(t *testing.T) {
	dir := t.TempDir()
	ours, _ := MarshalMovie(&Movie{Title: "One"})
	ours2, _ := MarshalMovie(&Movie{Title: "Two"})

	// New file: written.
	p := filepath.Join(dir, "movie.nfo")
	if w, err := WriteFile(p, ours); err != nil || !w {
		t.Fatalf("new file: written=%v err=%v", w, err)
	}
	// Same content: not rewritten.
	if w, err := WriteFile(p, ours); err != nil || w {
		t.Errorf("unchanged: written=%v err=%v", w, err)
	}
	// Our file, new content: replaced.
	if w, err := WriteFile(p, ours2); err != nil || !w {
		t.Errorf("replace ours: written=%v err=%v", w, err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, ours2) {
		t.Errorf("content not replaced")
	}

	// Someone else's NFO: left alone.
	theirs := filepath.Join(dir, "Film (2020).nfo")
	kodi := []byte(`<?xml version="1.0"?><movie><title>Curated</title></movie>`)
	if err := os.WriteFile(theirs, kodi, 0o644); err != nil {
		t.Fatal(err)
	}
	if w, err := WriteFile(theirs, ours); !errors.Is(err, ErrNotOurs) || w {
		t.Errorf("foreign NFO: written=%v err=%v; want ErrNotOurs", w, err)
	}
	if b, _ := os.ReadFile(theirs); !bytes.Equal(b, kodi) {
		t.Error("foreign NFO was modified")
	}
	if !IsOurs(p) || IsOurs(theirs) || IsOurs(filepath.Join(dir, "missing.nfo")) {
		t.Error("IsOurs misjudged a file")
	}

	// No temporary files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), TempSuffix) {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Data without the marker is refused outright.
	if _, err := WriteFile(filepath.Join(dir, "x.nfo"), []byte("<movie/>")); err == nil {
		t.Error("unmarked data should be refused")
	}
}

func TestWriteFileUnwritableDir(t *testing.T) {
	data, _ := MarshalMovie(&Movie{Title: "X"})
	if _, err := WriteFile(filepath.Join(t.TempDir(), "no-such-dir", "movie.nfo"), data); err == nil {
		t.Error("write into a missing directory should fail, not panic")
	}
}
