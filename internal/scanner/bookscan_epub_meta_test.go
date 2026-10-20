package scanner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// writeMetaEPUB writes a one-chapter EPUB whose OPF <metadata> is
// metadataXML (the inner elements) and returns its path.
func writeMetaEPUB(t *testing.T, dir, name, metadataXML string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	write := func(n, body string) {
		w, err := zw.Create(n)
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
<package version="3.0" xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
  <metadata>`+metadataXML+`</metadata>
  <manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c1"/></spine>
</package>`)
	write("OEBPS/ch1.xhtml", "<html><body>1</body></html>")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// Standard Ebooks' shape: EPUB 3 roles in refining metas (an author who
// also wrote the preface, and an editor), and the description as escaped
// HTML.
const standardEbooksMeta = `
    <dc:title id="title">The Picture of Dorian Gray</dc:title>
    <dc:creator id="author">Oscar Wilde</dc:creator>
    <meta property="role" refines="#author" scheme="marc:relators">aut</meta>
    <meta property="role" refines="#author" scheme="marc:relators">wpr</meta>
    <dc:creator id="ed">Some Editor</dc:creator>
    <meta property="role" refines="#ed" scheme="marc:relators">edt</meta>
    <dc:date>2014-05-25T00:00:00Z</dc:date>
    <dc:description id="description">
      &lt;p&gt;&lt;i&gt;The Picture of Dorian Gray&lt;/i&gt; was first published in 1890.&lt;/p&gt;
    </dc:description>`

func TestReadEpubMetadata(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, meta             string
		title, author, summary string
	}{
		{
			name: "epub3 refines roles and an HTML description", meta: standardEbooksMeta,
			title:   "The Picture of Dorian Gray",
			author:  "Oscar Wilde",
			summary: "The Picture of Dorian Gray was first published in 1890.",
		},
		{
			name: "epub2 opf:role on the creator",
			meta: `<dc:title>Emma</dc:title>
			       <dc:creator opf:role="aut">Jane Austen</dc:creator>
			       <dc:creator opf:role="ill">A. Illustrator</dc:creator>`,
			title: "Emma", author: "Jane Austen",
		},
		{
			name: "no roles at all: every creator",
			meta: `<dc:title>  Good   Omens </dc:title>
			       <dc:creator>Terry Pratchett</dc:creator>
			       <dc:creator>Neil Gaiman</dc:creator>`,
			title: "Good Omens", author: "Terry Pratchett, Neil Gaiman",
		},
		{
			name: "no metadata", meta: ``,
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeMetaEPUB(t, dir, filepath.Base(t.Name())+string(rune('a'+i))+".epub", c.meta)
			m, ok := readEpubMetadata(p)
			if !ok {
				t.Fatal("readEpubMetadata: not ok")
			}
			if m.Title != c.title || m.Author != c.author || m.Description != c.summary {
				t.Errorf("got %+v, want title=%q author=%q summary=%q", m, c.title, c.author, c.summary)
			}
		})
	}

	if _, ok := readEpubMetadata(filepath.Join(dir, "missing.epub")); ok {
		t.Error("a missing file should not be ok")
	}
}

// A new EPUB is named from its OPF, not its filename.
func TestProcessBook_UsesEpubMetadata(t *testing.T) {
	dir := t.TempDir()
	p := writeMetaEPUB(t, dir, "oscar-wilde_the-picture-of-dorian-gray.epub", standardEbooksMeta)
	svc := newMockMediaService()
	s := newTestScanner(svc)

	if _, err := s.processBook(context.Background(), uuid.New(), p, []string{dir}); err != nil {
		t.Fatal(err)
	}
	if len(svc.flatCalls) != 1 {
		t.Fatalf("FindOrCreateItem calls = %d, want 1", len(svc.flatCalls))
	}
	got := svc.flatCalls[0]
	if got.Title != "The Picture of Dorian Gray" {
		t.Errorf("title = %q", got.Title)
	}
	if got.OriginalTitle == nil || *got.OriginalTitle != "Oscar Wilde" {
		t.Errorf("author (original_title) = %v", got.OriginalTitle)
	}
	if got.Summary == nil || *got.Summary == "" {
		t.Error("summary not set from the description")
	}
}

// A book without OPF metadata keeps the filename title.
func TestProcessBook_FilenameFallback(t *testing.T) {
	dir := t.TempDir()
	p := writeMetaEPUB(t, dir, "A_Wizard_of_Earthsea.epub", ``)
	svc := newMockMediaService()
	s := newTestScanner(svc)
	if _, err := s.processBook(context.Background(), uuid.New(), p, []string{dir}); err != nil {
		t.Fatal(err)
	}
	if got := svc.flatCalls[0]; got.Title != "A Wizard of Earthsea" || got.OriginalTitle != nil {
		t.Errorf("got title=%q author=%v, want the filename title and no author", got.Title, got.OriginalTitle)
	}
}

// An EPUB scanned before OPF metadata was read is renamed in place on an
// unchanged file; a title someone else set is left alone.
func TestHealUnchangedBook(t *testing.T) {
	dir := t.TempDir()
	p := writeMetaEPUB(t, dir, "oscar-wilde_the-picture-of-dorian-gray.epub", standardEbooksMeta)
	svc := newMockMediaService()
	s := newTestScanner(svc)
	ctx := context.Background()

	old := &media.Item{ID: uuid.New(), Type: "book", Title: parseBookTitle(p), SortTitle: "x"}
	svc.items[old.ID] = old
	s.healUnchangedBook(ctx, "book", old, p)
	if old.Title != "The Picture of Dorian Gray" {
		t.Errorf("healed title = %q", old.Title)
	}
	if old.OriginalTitle == nil || *old.OriginalTitle != "Oscar Wilde" {
		t.Errorf("healed author = %v", old.OriginalTitle)
	}

	// Named by enrichment or an admin: untouched.
	named := &media.Item{ID: uuid.New(), Type: "book", Title: "Dorian Gray (Annotated)"}
	svc.items[named.ID] = named
	s.healUnchangedBook(ctx, "book", named, p)
	if named.Title != "Dorian Gray (Annotated)" || named.OriginalTitle != nil {
		t.Errorf("a title set elsewhere was changed: %+v", named)
	}

	// Not a book library, or not an EPUB: untouched.
	other := &media.Item{ID: uuid.New(), Type: "book", Title: parseBookTitle(p)}
	svc.items[other.ID] = other
	s.healUnchangedBook(ctx, "audiobook", other, p)
	if other.Title != parseBookTitle(p) {
		t.Error("healed outside a book library")
	}
}
