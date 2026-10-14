package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// movieHashChunk is how much of each end of the file the OpenSubtitles hash
// reads.
const movieHashChunk = 64 * 1024

// MovieHash computes the OpenSubtitles "moviehash" of a local file: the file
// size plus the sum, as little-endian uint64 words with wrap-around, of the
// first and last 64 KiB, printed as 16 hex digits. Subtitles uploaded against
// a release carry its hash, so a hash match means the subtitle was timed for
// exactly this file. Reads 128 KiB whatever the file's size.
func MovieHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return movieHashReader(f, st.Size())
}

func movieHashReader(r io.ReaderAt, size int64) (string, error) {
	if size < movieHashChunk {
		return "", errors.New("file too small for a movie hash")
	}
	hash := uint64(size)
	buf := make([]byte, movieHashChunk)
	for _, off := range []int64{0, size - movieHashChunk} {
		if _, err := r.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read: %w", err)
		}
		for i := 0; i < movieHashChunk; i += 8 {
			hash += binary.LittleEndian.Uint64(buf[i : i+8])
		}
	}
	return fmt.Sprintf("%016x", hash), nil
}
