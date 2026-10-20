package scanner

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"html"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// EPUB on-disk shape we care about:
//   - META-INF/container.xml points at the OPF (package document)
//   - the OPF lists <manifest> items and a <spine> ordering them
//   - any item with media-type starting "image/" can be the cover;
//     the convention is an item with id="cover" or properties="cover-image"
//
// We only need three things from the file:
//   1. Spine length → page count (each spine entry is rendered as one
//      "page" in our reader; epub.js does its own re-pagination
//      client-side so this is mostly a "how big is this book" hint)
//   2. Cover image bytes → poster
//   3. Title, author and description from the OPF metadata
//      (readEpubMetadata); the scanner falls back to the filename when
//      the book doesn't carry them
//
// EPUB is just a ZIP, so all the work happens through stdlib
// archive/zip + encoding/xml — no new dependency needed.

type epubContainer struct {
	XMLName   xml.Name `xml:"container"`
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	XMLName xml.Name `xml:"package"`
	// Dublin Core fields of the OPF's <metadata>. The tags carry no
	// namespace, so dc:title, dc:creator and dc:description match
	// whatever prefix the book uses.
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []struct {
			Name string `xml:",chardata"`
			ID   string `xml:"id,attr"`
			// EPUB 2 puts the role on the element (opf:role="aut").
			Role string `xml:"role,attr"`
		} `xml:"creator"`
		Descriptions []string `xml:"description"`
		// EPUB 3 puts it in a refining <meta property="role">aut</meta>.
		Metas []struct {
			Refines  string `xml:"refines,attr"`
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// countEpubPages returns the number of spine items (chapters). This
// is what the reader's "Page N of M" indicator binds to. Real
// reflowable pagination happens in epub.js client-side based on
// viewport + font size — the spine length is a coarse hint, not the
// actual pagination.
func countEpubPages(path string) int {
	pkg, _, ok := openEpubPackage(path)
	if !ok {
		return 0
	}
	return len(pkg.Spine.ItemRefs)
}

// readFirstEpubCover finds the cover image inside an EPUB. Tries:
//  1. Manifest item with properties="cover-image" (EPUB 3 standard)
//  2. Manifest item with id="cover" or id="cover-image" that's an
//     image type (EPUB 2 convention; many EPUB 3 readers also write
//     this for back-compat)
//  3. The first raster manifest image (last-resort heuristic — better
//     than no cover at all)
//
// An SVG cover (Standard Ebooks' "advanced" EPUB) yields the raster it
// embeds; one with none is skipped, never handed back as the cover.
//
// Returns the raw image bytes when found; the caller re-encodes
// through stdlib JPEG just like the CBZ path so all book covers land
// as {id}-poster.jpg regardless of source format.
func readFirstEpubCover(epubPath string) ([]byte, bool) {
	pkg, opfPath, ok := openEpubPackage(epubPath)
	if !ok {
		return nil, false
	}

	// Manifest hrefs are relative to the OPF's directory.
	opfDir := path.Dir(opfPath)

	// Candidates in order: the marked cover, the conventional ids, then
	// any raster image. SVG items only count as the marked cover: an
	// advanced EPUB's titlepage.svg or logo.svg is lettering, not art.
	var candidates []string
	seen := map[string]bool{}
	add := func(href string) {
		if href != "" && !seen[href] {
			seen[href] = true
			candidates = append(candidates, href)
		}
	}
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.Properties, "cover-image") {
			add(it.Href)
		}
	}
	for _, it := range pkg.Manifest.Items {
		if (it.ID == "cover" || it.ID == "cover-image") && strings.HasPrefix(it.MediaType, "image/") {
			add(it.Href)
		}
	}
	for _, it := range pkg.Manifest.Items {
		if strings.HasPrefix(it.MediaType, "image/") && it.MediaType != "image/svg+xml" {
			add(it.Href)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	r, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, false
	}
	defer r.Close()

	for _, href := range candidates {
		entry := path.Join(opfDir, href)
		data, ok := readZipEntry(r, entry, href)
		if !ok {
			continue
		}
		if isRasterImage(data) {
			return data, true
		}
		// A Standard Ebooks "advanced" cover is an SVG: the painting as
		// an embedded (or linked) JPEG with the title drawn over it as
		// paths. The stdlib can't draw SVG, so take the painting.
		if raster, ok := svgEmbeddedRaster(r, data, path.Dir(entry)); ok {
			return raster, true
		}
	}
	return nil, false
}

// readZipEntry returns an entry's bytes (capped at maxBookEntryBytes),
// trying each name in turn: the OPF-relative path, then the raw href.
func readZipEntry(r *zip.ReadCloser, names ...string) ([]byte, bool) {
	for _, name := range names {
		for _, f := range r.File {
			if f.Name != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			data, err := io.ReadAll(io.LimitReader(rc, maxBookEntryBytes))
			rc.Close()
			if err != nil {
				return nil, false
			}
			return data, true
		}
	}
	return nil, false
}

// svgImageHref finds the first <image> reference in an SVG: href or
// xlink:href, a data: URI or a path.
var svgImageHref = regexp.MustCompile(`<image\b[^>]*?\b(?:xlink:)?href\s*=\s*["']([^"']+)["']`)

// svgEmbeddedRaster returns the raster image an SVG cover carries: a
// base64 data: URI (Standard Ebooks embeds the cover painting that
// way) or an archive path relative to the SVG. ok=false for a pure
// vector SVG.
func svgEmbeddedRaster(r *zip.ReadCloser, svg []byte, svgDir string) ([]byte, bool) {
	if !looksLikeSVG(svg) {
		return nil, false
	}
	m := svgImageHref.FindSubmatch(svg)
	if m == nil {
		return nil, false
	}
	ref := strings.TrimSpace(html.UnescapeString(string(m[1])))
	if strings.HasPrefix(ref, "data:") {
		comma := strings.IndexByte(ref, ',')
		if comma < 0 || !strings.Contains(ref[:comma], ";base64") {
			return nil, false
		}
		data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(ref[comma+1:]), ""))
		if err != nil || !isRasterImage(data) {
			return nil, false
		}
		return data, true
	}
	data, ok := readZipEntry(r, path.Join(svgDir, ref))
	if !ok || !isRasterImage(data) {
		return nil, false
	}
	return data, true
}

func looksLikeSVG(b []byte) bool {
	head := b
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, []byte("<svg"))
}

// isRasterImage reports whether b starts like a JPEG, PNG, GIF or WebP:
// something a browser shows when it's served as an image, unlike an SVG
// saved under a .jpg name.
func isRasterImage(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return true
	case bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return true
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return true
	}
	return false
}

// epubMetadata is what the scanner takes from an EPUB's OPF: the title,
// the author(s) as one display string, and the description as plain
// text. Empty fields mean the book didn't say.
type epubMetadata struct {
	Title       string
	Author      string
	Description string
}

// readEpubMetadata reads the OPF's Dublin Core title, creators and
// description. Authors are the creators marked "aut" (EPUB 2 opf:role or
// an EPUB 3 refining role meta); with no roles at all, every creator.
// The publication date is left out on purpose: dc:date is usually the
// ebook edition's date (Standard Ebooks, Gutenberg), not the work's.
func readEpubMetadata(epubPath string) (epubMetadata, bool) {
	pkg, _, ok := openEpubPackage(epubPath)
	if !ok {
		return epubMetadata{}, false
	}
	var m epubMetadata
	for _, t := range pkg.Metadata.Titles {
		if t = collapseSpace(t); t != "" {
			m.Title = t
			break
		}
	}

	// creator id → its EPUB 3 roles; one creator can have several
	// (Standard Ebooks marks Wilde both "aut" and "wpr").
	roles := map[string][]string{}
	for _, meta := range pkg.Metadata.Metas {
		if meta.Property == "role" && strings.HasPrefix(meta.Refines, "#") {
			id := strings.TrimPrefix(meta.Refines, "#")
			roles[id] = append(roles[id], strings.TrimSpace(meta.Value))
		}
	}
	var authors, all []string
	anyRole := false
	for _, c := range pkg.Metadata.Creators {
		name := collapseSpace(c.Name)
		if name == "" {
			continue
		}
		all = append(all, name)
		cr := roles[c.ID]
		if c.Role != "" {
			cr = append(cr, c.Role)
		}
		if len(cr) > 0 {
			anyRole = true
		}
		for _, r := range cr {
			if r == "aut" {
				authors = append(authors, name)
				break
			}
		}
	}
	if !anyRole {
		authors = all
	}
	m.Author = strings.Join(authors, ", ")

	for _, d := range pkg.Metadata.Descriptions {
		if d = plainText(d); d != "" {
			m.Description = d
			break
		}
	}
	return m, true
}

var htmlTag = regexp.MustCompile(`(?s)<[^>]*>`)

// plainText turns an OPF description, which is often escaped HTML
// ("&lt;p&gt;…" decoding to "<p>…"), into plain text for the summary.
func plainText(s string) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return collapseSpace(s)
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// openEpubPackage parses META-INF/container.xml to find the OPF, then
// parses the OPF and returns it. opfPath is the in-archive path of
// the OPF (so callers can resolve relative manifest hrefs against
// path.Dir(opfPath)).
func openEpubPackage(epubPath string) (epubPackage, string, bool) {
	var pkg epubPackage
	r, err := zip.OpenReader(epubPath)
	if err != nil {
		return pkg, "", false
	}
	defer r.Close()

	// Step 1: container.xml → first rootfile path.
	var container epubContainer
	if !readZipXML(r, "META-INF/container.xml", &container) || len(container.Rootfiles) == 0 {
		return pkg, "", false
	}
	opfPath := container.Rootfiles[0].FullPath
	if opfPath == "" {
		return pkg, "", false
	}

	// Step 2: parse the OPF.
	if !readZipXML(r, opfPath, &pkg) {
		return pkg, "", false
	}
	return pkg, opfPath, true
}

// readZipXML finds an entry by name and unmarshals its XML body into
// dst. Returns true on success; failures (missing entry, malformed
// XML) return false silently — the caller handles "we couldn't read
// this EPUB" by treating it as an empty book, same way unreadable
// CBZs are handled.
func readZipXML(r *zip.ReadCloser, entryName string, dst any) bool {
	for _, f := range r.File {
		if f.Name != entryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false
		}
		defer rc.Close()
		dec := xml.NewDecoder(rc)
		if err := dec.Decode(dst); err != nil {
			return false
		}
		return true
	}
	return false
}

// isEPUB reports whether a path looks like an EPUB file. Used by the
// scanner + API dispatch to route to the right reader path.
func isEPUB(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".epub")
}
