package m3u

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParse_ExtendedPlaylist(t *testing.T) {
	text := "#EXTM3U\r\n" +
		"#PLAYLIST:Road trip\r\n" +
		"#EXTINF:245,Daft Punk - One More Time\r\n" +
		"/mnt/music/Daft Punk/Discovery/01 One More Time.flac\r\n" +
		"\r\n" +
		"#EXTINF:-1,Just A Title\r\n" +
		"Artist/Album/02.mp3\r\n" +
		"plain/no/extinf.ogg\r\n" +
		"#extinf:12.6 tvg-id=\"x\",A - B - C\n" +
		"http://example.com/stream.mp3"
	got, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Line: 4, Path: "/mnt/music/Daft Punk/Discovery/01 One More Time.flac", Seconds: 245, Artist: "Daft Punk", Title: "One More Time"},
		{Line: 7, Path: "Artist/Album/02.mp3", Seconds: -1, Title: "Just A Title"},
		{Line: 8, Path: "plain/no/extinf.ogg", Seconds: -1},
		{Line: 10, Path: "http://example.com/stream.mp3", Seconds: 13, Artist: "A", Title: "B - C"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParse_LoneCarriageReturns(t *testing.T) {
	got, err := Parse("#EXTM3U\r#EXTINF:1,X - Y\ra.mp3\rb.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "a.mp3" || got[0].Title != "Y" || got[1].Path != "b.mp3" || got[1].Line != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestParse_EntryCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxEntries; i++ {
		b.WriteString("a.mp3\n")
	}
	if _, err := Parse(b.String()); !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("err = %v, want ErrTooManyEntries", err)
	}
}

func TestDecode(t *testing.T) {
	// UTF-8 with BOM.
	s, err := Decode([]byte("\xEF\xBB\xBF#EXTM3U\nBjörk.flac"), false)
	if err != nil || s != "#EXTM3U\nBjörk.flac" {
		t.Fatalf("utf8: %q %v", s, err)
	}
	// Latin-1 ö (0xF6) is not UTF-8.
	latin := []byte("Bj\xF6rk.mp3")
	if _, err := Decode(latin, false); !errors.Is(err, ErrNotUTF8) {
		t.Fatalf("m3u8 latin1: err = %v", err)
	}
	s, err = Decode(latin, true)
	if err != nil || s != "Björk.mp3" {
		t.Fatalf("latin1: %q %v", s, err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want string
		kind Kind
	}{
		{"/mnt/music/a.flac", "/mnt/music/a.flac", KindAbsolute},
		{`M:\Music\a.flac`, `M:\Music\a.flac`, KindAbsolute},
		{`\\nas\media\a.flac`, `\\nas\media\a.flac`, KindAbsolute},
		{"Artist/a.flac", "Artist/a.flac", KindRelative},
		{`..\Artist\a.flac`, `..\Artist\a.flac`, KindRelative},
		{"https://host/a.mp3", "https://host/a.mp3", KindURL},
		{"rtsp://cam/stream", "rtsp://cam/stream", KindURL},
		{"file:///mnt/My%20Music/a.flac", "/mnt/My Music/a.flac", KindAbsolute},
		{"file:///C:/Music/a.flac", "C:/Music/a.flac", KindAbsolute},
		{"file://localhost/mnt/a.flac", "/mnt/a.flac", KindAbsolute},
		{"file://nas/media/a.flac", `\\nas\media\a.flac`, KindAbsolute},
	}
	for _, c := range cases {
		got, kind := Classify(c.in)
		if got != c.want || kind != c.kind {
			t.Errorf("Classify(%q) = %q,%d want %q,%d", c.in, got, kind, c.want, c.kind)
		}
	}
}

func TestJoin(t *testing.T) {
	cases := []struct{ base, rel, want string }{
		{"/mnt/music", "Artist/a.flac", "/mnt/music/Artist/a.flac"},
		{"/mnt/music/", `Artist\a.flac`, "/mnt/music/Artist/a.flac"},
		{"/mnt/music/lists", "../Artist/a.flac", "/mnt/music/Artist/a.flac"},
		{"/mnt/music", "../../etc/passwd", "/etc/passwd"},
		{`\\nas\media`, `a\b.flac`, `\\nas\media\a\b.flac`},
		{`M:\Music`, `Artist\a.flac`, `M:\Music\Artist\a.flac`},
		{"/", "a.flac", "/a.flac"},
		{"", "a.flac", ""},
	}
	for _, c := range cases {
		if got := Join(c.base, c.rel); got != c.want {
			t.Errorf("Join(%q,%q) = %q, want %q", c.base, c.rel, got, c.want)
		}
	}
}

func TestComponentsAndTrailingMatch(t *testing.T) {
	a := FoldedComponents(`M:\Music\Daft Punk\Discovery\01.flac`)
	b := FoldedComponents("/mnt/media/music/daft punk/discovery/01.flac")
	if n := TrailingMatch(a, b, 3); n != 3 {
		t.Fatalf("trailing = %d, want 3", n)
	}
	if n := TrailingMatch(a, FoldedComponents("/x/Other/Discovery/01.flac"), 3); n != 2 {
		t.Fatalf("trailing = %d, want 2", n)
	}
	if got := Components("./a//b/./c"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("components = %v", got)
	}
}

func TestWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Entry(245, "Daft Punk", "One More Time", "/mnt/music/a.flac")
	w.Entry(-5, "", "Movie\nInjected", "Movies/m.mkv")
	w.Entry(1, "x", "y", "  ") // empty path skipped
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:245,Daft Punk - One More Time\n/mnt/music/a.flac\n" +
		"#EXTINF:-1,Movie Injected\nMovies/m.mkv\n"
	if buf.String() != want {
		t.Fatalf("got %q\nwant %q", buf.String(), want)
	}
	// Round trip.
	entries, err := Parse(buf.String())
	if err != nil || len(entries) != 2 || entries[0].Artist != "Daft Punk" || entries[1].Seconds != -1 {
		t.Fatalf("round trip: %+v %v", entries, err)
	}
}
