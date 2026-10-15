package scanner

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// flacCommentLimit caps the Vorbis comment block readFLACComments accepts.
// Real blocks are a few KiB (lyrics push some to tens); the cap keeps a
// crafted length field from allocating gigabytes.
const flacCommentLimit = 16 << 20

// flacMetadataBlockLimit caps how many metadata blocks are walked before the
// comment block is given up on.
const flacMetadataBlockLimit = 64

// readFLACComments reads the Vorbis comment block of a FLAC stream at r's
// current position and returns every comment by lower-cased field name, each
// value in file order. Unlike dhowden/tag, which keeps one value per field,
// repeated fields (ARTISTS=A, ARTISTS=B) keep all their values. Blocks
// before the comment block (stream info, padding, pictures) are skipped
// without being read.
func readFLACComments(r io.ReadSeeker) (map[string][]string, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, err
	}
	if string(magic[:]) != "fLaC" {
		return nil, errors.New("flac: not a FLAC stream")
	}
	for i := 0; i < flacMetadataBlockLimit; i++ {
		var hdr [4]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, err
		}
		last := hdr[0]&0x80 != 0
		blockType := hdr[0] & 0x7f
		size := int64(hdr[1])<<16 | int64(hdr[2])<<8 | int64(hdr[3])
		if blockType == 4 {
			if size > flacCommentLimit {
				return nil, errors.New("flac: comment block too large")
			}
			buf := make([]byte, size)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, err
			}
			return parseVorbisComments(buf)
		}
		if last {
			break
		}
		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("flac: no comment block")
}

// parseVorbisComments parses a Vorbis comment block (little-endian lengths:
// vendor string, comment count, then "FIELD=value" strings).
func parseVorbisComments(b []byte) (map[string][]string, error) {
	next := func() ([]byte, error) {
		if len(b) < 4 {
			return nil, errors.New("vorbis comment: truncated")
		}
		n := binary.LittleEndian.Uint32(b)
		b = b[4:]
		if uint64(n) > uint64(len(b)) {
			return nil, errors.New("vorbis comment: length out of range")
		}
		v := b[:n]
		b = b[n:]
		return v, nil
	}
	if _, err := next(); err != nil { // vendor
		return nil, err
	}
	if len(b) < 4 {
		return nil, errors.New("vorbis comment: truncated")
	}
	count := binary.LittleEndian.Uint32(b)
	b = b[4:]
	out := map[string][]string{}
	for i := uint32(0); i < count; i++ {
		c, err := next()
		if err != nil {
			return out, err
		}
		k, v, ok := strings.Cut(string(c), "=")
		if !ok {
			continue
		}
		k = strings.ToLower(k)
		out[k] = append(out[k], v)
	}
	return out, nil
}

// applyMultiValued takes the multi-valued fields from a FLAC file's full
// comment list: ARTISTS and MUSICBRAINZ_ARTISTID with every value, and the
// first ARTIST / ALBUMARTIST rather than dhowden/tag's last. A file with
// several ARTIST fields and no ARTISTS lists those as its artists.
func (m *MusicTags) applyMultiValued(c map[string][]string) {
	clean := func(vs []string) []string {
		var out []string
		for _, v := range vs {
			if v = cleanTag(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	artist := clean(c["artist"])
	if len(artist) > 1 {
		m.Artist = artist[0]
	}
	if aa := clean(c["albumartist"]); len(aa) > 1 {
		m.AlbumArtist = aa[0]
	}
	if artists := clean(c["artists"]); len(artists) > 0 {
		m.Artists = artists
	} else if len(artist) > 1 {
		m.Artists = artist
	}
	if ids := c["musicbrainz_artistid"]; len(ids) > 0 {
		m.ArtistMBIDs = parseMBIDList(ids)
		if len(m.ArtistMBIDs) > 0 {
			m.MBArtistID = m.ArtistMBIDs[0]
		}
	}
}
