package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	for x := 0; x < 4; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{200, 30, 30, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeCoverEPUB writes an EPUB whose manifest is items and whose archive
// holds files (paths relative to the OPF's epub/ directory).
func writeCoverEPUB(t *testing.T, dir, name, items string, files map[string][]byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	put := func(n string, b []byte) {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	put("mimetype", []byte("application/epub+zip"))
	put("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="epub/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	put("epub/content.opf", []byte(`<?xml version="1.0" encoding="utf-8"?>
<package version="3.0" xmlns="http://www.idpf.org/2007/opf">
  <metadata/>
  <manifest>`+items+`<item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`))
	put("epub/text/ch1.xhtml", []byte("<html><body>1</body></html>"))
	for n, b := range files {
		put("epub/"+n, b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// Standard Ebooks' advanced cover: an SVG with the painting embedded as a
// base64 JPEG and the lettering as paths.
func seCoverSVG(jpg []byte) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" version="1.1" viewBox="0 0 1400 2100">
	<title>The cover</title>
	<image width="1400" height="2100" x="0" y="0" xlink:href="data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString(jpg) + `"/>
	<path d="M0 0h10v10z"/>
</svg>`)
}

func TestReadFirstEpubCover_SVG(t *testing.T) {
	dir := t.TempDir()
	jpg := tinyJPEG(t)
	cases := []struct {
		name  string
		items string
		files map[string][]byte
		want  []byte // nil: no cover
	}{
		{
			name:  "svg cover embedding a base64 jpeg",
			items: `<item href="images/cover.svg" id="cover.svg" media-type="image/svg+xml" properties="cover-image"/><item href="images/titlepage.svg" id="titlepage.svg" media-type="image/svg+xml"/>`,
			files: map[string][]byte{"images/cover.svg": seCoverSVG(jpg), "images/titlepage.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`)},
			want:  jpg,
		},
		{
			name:  "svg cover linking a jpeg in the book",
			items: `<item href="images/cover.svg" id="cover.svg" media-type="image/svg+xml" properties="cover-image"/><item href="images/art.jpg" id="art" media-type="image/jpeg"/>`,
			files: map[string][]byte{"images/cover.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="art.jpg"/></svg>`), "images/art.jpg": jpg},
			want:  jpg,
		},
		{
			name:  "vector-only svg cover falls back to a raster image",
			items: `<item href="images/cover.svg" id="cover.svg" media-type="image/svg+xml" properties="cover-image"/><item href="images/plate.jpg" id="plate" media-type="image/jpeg"/>`,
			files: map[string][]byte{"images/cover.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`), "images/plate.jpg": jpg},
			want:  jpg,
		},
		{
			name:  "vector-only svgs: no cover rather than an svg",
			items: `<item href="images/cover.svg" id="cover.svg" media-type="image/svg+xml" properties="cover-image"/><item href="images/logo.svg" id="logo.svg" media-type="image/svg+xml"/>`,
			files: map[string][]byte{"images/cover.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`), "images/logo.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		},
		{
			name:  "plain jpeg cover",
			items: `<item href="images/cover.jpg" id="cover" media-type="image/jpeg" properties="cover-image"/>`,
			files: map[string][]byte{"images/cover.jpg": jpg},
			want:  jpg,
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeCoverEPUB(t, dir, string(rune('a'+i))+".epub", c.items, c.files)
			got, ok := readFirstEpubCover(p)
			if c.want == nil {
				if ok {
					t.Fatalf("got a %d-byte cover, want none", len(got))
				}
				return
			}
			if !ok || !bytes.Equal(got, c.want) {
				t.Fatalf("ok=%v, %d bytes; want the %d-byte jpeg", ok, len(got), len(c.want))
			}
		})
	}
}

func TestIsRasterImage(t *testing.T) {
	cases := map[string]bool{
		"\xFF\xD8\xFF\xE0jpeg":                 true,
		"\x89PNG\r\n\x1a\nrest":                true,
		"GIF89a...":                            true,
		"RIFF\x00\x00\x00\x00WEBPVP8 ":         true,
		`<?xml version="1.0"?><svg xmlns="…">`: false,
		"":                                     false,
	}
	for in, want := range cases {
		if got := isRasterImage([]byte(in)); got != want {
			t.Errorf("isRasterImage(%q) = %v, want %v", in, got, want)
		}
	}
}

// A poster an older scan saved as raw SVG is rewritten from the EPUB on an
// unchanged file; a good poster is left alone.
func TestHealBookPoster(t *testing.T) {
	dir := t.TempDir()
	jpg := tinyJPEG(t)
	p := writeCoverEPUB(t, dir, "upton-sinclair_the-jungle_advanced.epub",
		`<item href="images/cover.svg" id="cover.svg" media-type="image/svg+xml" properties="cover-image"/>`,
		map[string][]byte{"images/cover.svg": seCoverSVG(jpg)})
	s := newTestScanner(newMockMediaService())
	ctx := context.Background()

	item := &media.Item{ID: uuid.New(), Type: "book", Title: "The Jungle"}
	poster := filepath.Join(dir, item.ID.String()+"-poster.jpg")
	if err := os.WriteFile(poster, seCoverSVG(jpg), 0o644); err != nil {
		t.Fatal(err)
	}
	s.healBookPoster(ctx, "book", item, p)
	got, _ := os.ReadFile(poster)
	if !isRasterImage(got) {
		t.Fatalf("poster still not an image: %q", got[:min(len(got), 20)])
	}

	// A good poster stays byte-for-byte.
	good := []byte(string(got))
	s.healBookPoster(ctx, "book", item, p)
	again, _ := os.ReadFile(poster)
	if !bytes.Equal(again, good) {
		t.Error("a good poster was rewritten")
	}
}
