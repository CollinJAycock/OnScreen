package scanner

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rarEntry struct {
	name string
	data []byte
}

// storedRAR builds a RAR 1.5-format archive of uncompressed ("stored")
// entries — enough for rardecode to walk, without a rar binary.
func storedRAR(entries []rarEntry) []byte {
	le := binary.LittleEndian
	out := []byte("Rar!\x1a\x07\x00")
	block := func(htype byte, flags uint16, body []byte) {
		h := make([]byte, 7, 7+len(body))
		h[2] = htype
		le.PutUint16(h[3:], flags)
		le.PutUint16(h[5:], uint16(7+len(body)))
		h = append(h, body...)
		le.PutUint16(h[0:], uint16(crc32.ChecksumIEEE(h[2:])))
		out = append(out, h...)
	}
	block(0x73, 0, make([]byte, 6)) // archive header
	for _, e := range entries {
		body := make([]byte, 25)
		le.PutUint32(body[0:], uint32(len(e.data))) // packed size
		le.PutUint32(body[4:], uint32(len(e.data))) // unpacked size
		le.PutUint32(body[9:], crc32.ChecksumIEEE(e.data))
		le.PutUint32(body[13:], 0x5A210000) // DOS time
		body[17] = 20                       // unpack version
		body[18] = 0x30                     // method: store
		le.PutUint16(body[19:], uint16(len(e.name)))
		le.PutUint32(body[21:], 0x20)
		body = append(body, e.name...)
		block(0x74, 0x8000, body) // file header, data follows
		out = append(out, e.data...)
	}
	block(0x7b, 0, nil) // end of archive
	return out
}

func TestReadFirstCBRPage_ReturnsFirstSortedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.cbr")
	archive := storedRAR([]rarEntry{
		{"page03.jpg", []byte("third")},
		{"notes.txt", []byte("not a page")},
		{"page01.jpg", []byte("first")},
		{"__MACOSX/._page00.jpg", []byte("resource fork")},
		{"page02.png", []byte("second")},
	})
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	data, ok := readFirstCBRPage(path)
	if !ok || string(data) != "first" {
		t.Fatalf("readFirstCBRPage = (%q, %v), want (first, true)", data, ok)
	}
	if got := countCBRPages(path); got != 3 {
		t.Errorf("countCBRPages = %d, want 3", got)
	}
}

func TestReadFirstCBRPage_NoPages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.cbr")
	if err := os.WriteFile(path, storedRAR([]rarEntry{{"readme.txt", []byte("x")}}), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, ok := readFirstCBRPage(path); ok || data != nil {
		t.Errorf("readFirstCBRPage = (%q, %v), want no page", data, ok)
	}
}

func writeEPUB(t *testing.T, opf string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.epub")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"mimetype": "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"content.opf": opf,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// container.xml and the OPF used to be decoded with no cap, so a small zip
// entry inflating to millions of <itemref/> grew the spine slice until the
// scanner ran out of memory.
func TestCountEpubPages_BoundsTheOPF(t *testing.T) {
	spine := func(n int) string {
		return `<?xml version="1.0" encoding="UTF-8"?>
<package version="3.0" xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/">
<metadata><dc:title>T</dc:title></metadata><manifest/><spine>` +
			strings.Repeat(`<itemref idref="c"/>`, n) + `</spine></package>`
	}
	for _, tc := range []struct {
		name string
		opf  string
		want int
	}{
		{"normal", spine(3), 3},
		{"just under the element cap", spine(maxEpubXMLElements - 10), maxEpubXMLElements - 10},
		{"over the element cap", spine(maxEpubXMLElements + 10), 0},
		{"over the byte cap", strings.Replace(spine(3), "<spine>", "<!--"+strings.Repeat(" ", maxEpubXMLBytes)+"--><spine>", 1), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := countEpubPages(writeEPUB(t, tc.opf)); got != tc.want {
				t.Errorf("countEpubPages = %d, want %d", got, tc.want)
			}
		})
	}
}

// The bounded decoder still resolves namespaced metadata the way the plain
// decoder did.
func TestDecodeBoundedXML_Namespaces(t *testing.T) {
	var pkg epubPackage
	err := decodeBoundedXML(strings.NewReader(`<?xml version="1.0"?>
<opf:package xmlns:opf="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/">
<opf:metadata><dc:title>The Title</dc:title><dc:creator opf:role="aut">An Author</dc:creator></opf:metadata>
<opf:spine><opf:itemref idref="a"/></opf:spine></opf:package>`), &pkg)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(pkg.Metadata.Titles) != 1 || pkg.Metadata.Titles[0] != "The Title" {
		t.Errorf("titles = %v", pkg.Metadata.Titles)
	}
	if len(pkg.Metadata.Creators) != 1 || pkg.Metadata.Creators[0].Name != "An Author" || pkg.Metadata.Creators[0].Role != "aut" {
		t.Errorf("creators = %+v", pkg.Metadata.Creators)
	}
	if len(pkg.Spine.ItemRefs) != 1 {
		t.Errorf("spine = %v", pkg.Spine.ItemRefs)
	}
}
