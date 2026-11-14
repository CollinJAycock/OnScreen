package scanner

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// maxCBRDictBytes caps the RAR decompression window a .cbr may declare (the
// library default is 4 GiB, allocated up front) so a crafted archive can't force
// a multi-GB allocation during a library scan. Real comic archives use far less.
const maxCBRDictBytes = 256 << 20

// readFirstCBRPage opens the CBR (RAR archive) at cbrPath and returns
// the bytes of the image entry that sorts first by name. Mirrors
// readFirstCBZPage for ZIP — same convention (every comic reader sorts
// pages by name) so a CBR re-archived from a CBZ produces the same cover.
//
// rardecode v2 is streaming-only, so this walks the archive once. It used
// to buffer every image entry (up to maxBookEntryBytes each) just to sort
// them and return the first, so a 300-page omnibus, or a crafted archive of
// many large entries, pinned gigabytes per scan. Now only the current best
// candidate is held: an entry's bytes are read only when its name sorts
// before every name seen so far, so peak memory is at most two entries (the
// best and the one being read). api/v1/books.go bounds its page reads the
// same way.
func readFirstCBRPage(cbrPath string) ([]byte, bool) {
	r, closeFn, ok := openCBR(cbrPath)
	if !ok {
		return nil, false
	}
	defer closeFn()

	var (
		bestName string
		best     []byte
		found    bool
	)
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
		if !isCBZPageEntry(header.Name) || (found && header.Name >= bestName) {
			continue
		}
		// Cap per-entry decompression — see comment in api/v1/books.go.
		data, rerr := io.ReadAll(io.LimitReader(r, maxBookEntryBytes))
		if rerr != nil {
			continue
		}
		bestName, best, found = header.Name, data, true
	}
	return best, found
}

// countCBRPages counts image entries in a CBR archive. Returns 0 on
// any I/O error so a single corrupt CBR doesn't fail the whole scan
// (same lenient stance as countCBZPages). Only names are looked at, so
// a 200-page comic doesn't load 200 MB into RAM just to learn the count.
func countCBRPages(path string) int {
	r, closeFn, ok := openCBR(path)
	if !ok {
		return 0
	}
	defer closeFn()
	n := 0
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0
		}
		if header == nil {
			break
		}
		if isCBZPageEntry(header.Name) {
			n++
		}
	}
	return n
}

// openCBR opens a RAR archive with the dictionary cap applied.
func openCBR(path string) (*rardecode.Reader, func(), bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	r, err := rardecode.NewReader(f, rardecode.MaxDictionarySize(maxCBRDictBytes))
	if err != nil {
		_ = f.Close()
		return nil, nil, false
	}
	return r, func() { _ = f.Close() }, true
}

// cbrCoverExtension reports whether a path looks like a CBR file.
// Used by the scanner dispatch in extractBookCover / processBook.
func isCBR(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".cbr")
}
