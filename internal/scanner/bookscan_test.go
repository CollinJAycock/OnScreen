package scanner

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestParseBookTitle covers the filename → title normalisation. The
// scanner doesn't try to extract series/issue numbers in Stage 1 —
// that hierarchy story is a follow-up. Just strips the extension and
// normalises common separators.
func TestParseBookTitle(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/comics/Saga Vol 1.cbz", "Saga Vol 1"},
		{"/comics/saga_vol_1.cbz", "saga vol 1"},
		{"/comics/Saga.Vol.1.cbz", "Saga Vol 1"},
		{"/comics/single.cbz", "single"},
		// CBR + EPUB share the same filename normalisation — the
		// dispatch happens later via countBookPages / readFirstBookCover.
		{"/comics/Saga.Vol.2.cbr", "Saga Vol 2"},
		{"/books/A_Wizard_of_Earthsea.epub", "A Wizard of Earthsea"},
	}
	for _, c := range cases {
		got := parseBookTitle(c.path)
		if got != c.want {
			t.Errorf("parseBookTitle(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestIsCBZPageEntry covers the entry filter the scanner and API
// handler share — image extensions allowed, directories and macOS
// metadata files (which inflate the page count if not filtered)
// excluded.
func TestIsCBZPageEntry(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Real pages
		{"001.jpg", true},
		{"page-001.png", true},
		{"chapter1/001.webp", true},
		// Directory entries
		{"chapter1/", false},
		{"", false},
		// macOS metadata noise
		{"__MACOSX/001.jpg", false},
		{"chapter1/.DS_Store", false},
		{"chapter1/._cover.jpg", false},
		// Non-image extensions
		{"info.txt", false},
		{"comicinfo.xml", false},
	}
	for _, c := range cases {
		got := isCBZPageEntry(c.name)
		if got != c.want {
			t.Errorf("isCBZPageEntry(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestCountCBZPages_RoundTrip builds a real CBZ in a temp dir and
// confirms the page count matches the number of image entries —
// catches regressions in either the entry filter or the zip walk.
func TestCountCBZPages_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cbzPath := filepath.Join(dir, "test.cbz")

	f, err := os.Create(cbzPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"001.jpg",
		"002.png",
		"003.webp",
		"chapter1/004.jpg",
		// Should be excluded
		"__MACOSX/005.jpg",
		"info.txt",
		"chapter1/",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := countCBZPages(cbzPath); got != 4 {
		t.Errorf("countCBZPages = %d, want 4 (filter should exclude __MACOSX, .txt, dir entry)", got)
	}
}

// TestIsCBR + TestIsEPUB lock the per-format dispatch so a renamed
// extension still routes to the right path. Both are pure-string
// helpers; failures usually mean someone changed the extension list
// in scanner.go without updating the dispatcher.
func TestIsCBR(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/comics/Saga.cbr", true},
		{"/comics/Saga.CBR", true},
		{"/comics/Saga.cbz", false},
		{"/books/Earthsea.epub", false},
	}
	for _, c := range cases {
		if got := isCBR(c.path); got != c.want {
			t.Errorf("isCBR(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsEPUB(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/books/Earthsea.epub", true},
		{"/books/Earthsea.EPUB", true},
		{"/books/Earthsea.pdf", false},
		{"/comics/Saga.cbz", false},
	}
	for _, c := range cases {
		if got := isEPUB(c.path); got != c.want {
			t.Errorf("isEPUB(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestCountEpubPages_RoundTrip builds a minimal valid EPUB (mimetype +
// container.xml + a 3-itemref OPF) in a temp dir and verifies the
// spine count is what we report as page count.
func TestCountEpubPages_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	epubPath := filepath.Join(dir, "tiny.epub")

	f, err := os.Create(epubPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}

	write("mimetype", "application/epub+zip")
	write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	write("OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package version="3.0" xmlns="http://www.idpf.org/2007/opf">
  <manifest>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="c3" href="ch3.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
    <itemref idref="c3"/>
  </spine>
</package>`)
	write("OEBPS/ch1.xhtml", "<html><body>1</body></html>")
	write("OEBPS/ch2.xhtml", "<html><body>2</body></html>")
	write("OEBPS/ch3.xhtml", "<html><body>3</body></html>")

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := countEpubPages(epubPath); got != 3 {
		t.Errorf("countEpubPages = %d, want 3", got)
	}
	if got := countBookPages(epubPath); got != 3 {
		t.Errorf("countBookPages dispatch = %d, want 3 (epub branch)", got)
	}
}
