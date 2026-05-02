package scanner

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// readFirstCBRPage opens the CBR (RAR archive) at cbrPath, sorts its
// image entries lexicographically, and returns the bytes of the first
// page. Mirrors readFirstCBZPage for ZIP — same convention (every
// comic reader sorts pages by name) so a CBR re-archived from a CBZ
// produces the same cover.
//
// The rardecode v2 streaming API doesn't support random access, so we
// walk the entire archive once collecting (name, bytes) pairs for
// every image entry, sort them, and return the first. RAR header
// allocation overhead is small; payload bytes only get read for image
// entries we'd otherwise also have to read for page-count anyway.
func readFirstCBRPage(cbrPath string) ([]byte, bool) {
	pages, ok := readCBRPages(cbrPath, true)
	if !ok || len(pages) == 0 {
		return nil, false
	}
	return pages[0].data, true
}

// countCBRPages counts image entries in a CBR archive. Returns 0 on
// any I/O error so a single corrupt CBR doesn't fail the whole scan
// (same lenient stance as countCBZPages).
func countCBRPages(path string) int {
	pages, ok := readCBRPages(path, false)
	if !ok {
		return 0
	}
	return len(pages)
}

// servePageFromCBR streams the pageNum-th image (1-indexed,
// alphabetically sorted) from a CBR to w. Returns errBookPageNotFound
// when pageNum is out of range so the API handler can map it to 404
// without leaking why. content-type setup is the caller's job — this
// function only writes the bytes.
func servePageFromCBR(cbrPath string, pageNum int, w io.Writer) (entryName string, err error) {
	pages, ok := readCBRPages(cbrPath, true)
	if !ok {
		return "", errBookOpenFailed
	}
	if pageNum < 1 || pageNum > len(pages) {
		return "", errBookPageNotFoundCBR
	}
	if _, err := w.Write(pages[pageNum-1].data); err != nil {
		return "", err
	}
	return pages[pageNum-1].name, nil
}

type cbrPage struct {
	name string
	data []byte
}

// readCBRPages does the actual archive walk. When `readBytes` is
// true, each image entry's body is buffered into the returned slice.
// When false, only the entry names are kept (tiny allocation per
// entry) — used by countCBRPages so a 200-page comic doesn't load
// 200 MB into RAM just to learn the page count.
func readCBRPages(cbrPath string, readBytes bool) ([]cbrPage, bool) {
	f, err := os.Open(cbrPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	r, err := rardecode.NewReader(f)
	if err != nil {
		return nil, false
	}

	var pages []cbrPage
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		if header == nil {
			break
		}
		if !isCBZPageEntry(header.Name) {
			continue
		}
		entry := cbrPage{name: header.Name}
		if readBytes {
			data, rerr := io.ReadAll(r)
			if rerr != nil {
				continue
			}
			entry.data = data
		}
		pages = append(pages, entry)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].name < pages[j].name })
	return pages, true
}

// errBookOpenFailed surfaces a "we couldn't open the archive at all"
// case to the API handler distinct from "page not found." The handler
// converts the former to 500 (real problem) and the latter to 404
// (user asked for a page beyond the end).
var errBookOpenFailed = errAbstract("book: archive open failed")

// errBookPageNotFoundCBR is the CBR-side equivalent of
// errBookPageNotFound (declared in books.go) — kept in this package
// to avoid an import cycle. The API handler maps either to 404.
var errBookPageNotFoundCBR = errAbstract("book: page not found")

// errAbstract is a tiny error type kept inline so this file doesn't
// need to import the errors package for two sentinel values.
type errAbstract string

func (e errAbstract) Error() string { return string(e) }

// cbrCoverExtension reports whether a path looks like a CBR file.
// Used by the scanner dispatch in extractBookCover / processBook.
func isCBR(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".cbr")
}
