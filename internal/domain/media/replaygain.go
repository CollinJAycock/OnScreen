package media

// ReplayGain is one file's stored ReplayGain values and where they came from
// (ReplayGainFromTags, ReplayGainAnalyzed, or nil with every value nil).
type ReplayGain struct {
	TrackGain *float64
	TrackPeak *float64
	AlbumGain *float64
	AlbumPeak *float64
	Source    *string
}

// ReplayGainOnRescan decides what a re-scan of a changed file does to the
// ReplayGain values stored for it (existing), given what the scan read (p).
// write=false leaves the stored values alone.
//
//   - Tags carry ReplayGain: they are stored, as source "tags", whatever was
//     there before — tags always win over a measurement.
//   - The tags were read and carry none, but the stored values came from
//     tags: the tags were removed, so the values go too (and the file joins
//     the loudness_analysis queue).
//   - The stored values were measured and the file's bytes changed (size or
//     content hash): the measurement is of other audio, so it is dropped and
//     the file is measured again.
//
// Anything else — notably a scan that couldn't read the tags
// (p.ReplayGainKnown false) — keeps what is stored.
func ReplayGainOnRescan(existing File, p CreateFileParams) (ReplayGain, bool) {
	if p.ReplayGainKnown && (p.ReplayGainTrackGain != nil || p.ReplayGainTrackPeak != nil ||
		p.ReplayGainAlbumGain != nil || p.ReplayGainAlbumPeak != nil) {
		src := ReplayGainFromTags
		rg := ReplayGain{
			TrackGain: p.ReplayGainTrackGain,
			TrackPeak: p.ReplayGainTrackPeak,
			AlbumGain: p.ReplayGainAlbumGain,
			AlbumPeak: p.ReplayGainAlbumPeak,
			Source:    &src,
		}
		return rg, !sameReplayGain(existing, rg)
	}
	switch rgSource(existing.ReplayGainSource) {
	case ReplayGainFromTags:
		if p.ReplayGainKnown {
			return ReplayGain{}, true
		}
	case ReplayGainAnalyzed:
		if fileBytesChanged(existing, p) {
			return ReplayGain{}, true
		}
	}
	return ReplayGain{}, false
}

// fileBytesChanged reports whether the scanned file differs from the stored
// row: another size, or another content hash when both are known.
func fileBytesChanged(existing File, p CreateFileParams) bool {
	if existing.FileSize != p.FileSize {
		return true
	}
	return existing.FileHash != nil && p.FileHash != nil && *existing.FileHash != *p.FileHash
}

func sameReplayGain(f File, rg ReplayGain) bool {
	return samePtr(f.ReplayGainTrackGain, rg.TrackGain) &&
		samePtr(f.ReplayGainTrackPeak, rg.TrackPeak) &&
		samePtr(f.ReplayGainAlbumGain, rg.AlbumGain) &&
		samePtr(f.ReplayGainAlbumPeak, rg.AlbumPeak) &&
		rgSource(f.ReplayGainSource) == rgSource(rg.Source)
}

func rgSource(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
