package scanner

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path"
	"path/filepath"
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
//   3. Title from the OPF metadata (currently unused; the scanner
//      falls back to the filename, which is usually fine for personal
//      libraries)
//
// EPUB is just a ZIP, so all the work happens through stdlib
// archive/zip + encoding/xml — no new dependency needed.

type epubContainer struct {
	XMLName  xml.Name `xml:"container"`
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	XMLName  xml.Name `xml:"package"`
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
//   1. Manifest item with properties="cover-image" (EPUB 3 standard)
//   2. Manifest item with id="cover" or id="cover-image" that's an
//      image type (EPUB 2 convention; many EPUB 3 readers also write
//      this for back-compat)
//   3. The first manifest item with media-type starting "image/"
//      (last-resort heuristic — better than no cover at all)
//
// Returns the raw image bytes when found; the caller re-encodes
// through stdlib JPEG just like the CBZ path so all book covers land
// as {id}-poster.jpg regardless of source format.
func readFirstEpubCover(epubPath string) ([]byte, bool) {
	pkg, opfPath, ok := openEpubPackage(epubPath)
	if !ok {
		return nil, false
	}

	// Manifest hrefs are relative to the OPF's directory — resolve
	// once so the per-candidate path lookup is a plain map hit.
	opfDir := path.Dir(opfPath)

	var coverHref, coverMediaType string

	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.Properties, "cover-image") {
			coverHref = it.Href
			coverMediaType = it.MediaType
			break
		}
	}
	if coverHref == "" {
		for _, it := range pkg.Manifest.Items {
			if (it.ID == "cover" || it.ID == "cover-image") && strings.HasPrefix(it.MediaType, "image/") {
				coverHref = it.Href
				coverMediaType = it.MediaType
				break
			}
		}
	}
	if coverHref == "" {
		for _, it := range pkg.Manifest.Items {
			if strings.HasPrefix(it.MediaType, "image/") {
				coverHref = it.Href
				coverMediaType = it.MediaType
				break
			}
		}
	}
	_ = coverMediaType // reserved — could short-circuit JPEG re-encode for already-JPEG covers
	if coverHref == "" {
		return nil, false
	}

	r, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, false
	}
	defer r.Close()

	target := path.Join(opfDir, coverHref)
	for _, f := range r.File {
		if f.Name == target || f.Name == coverHref {
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, false
			}
			return data, true
		}
	}
	return nil, false
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
