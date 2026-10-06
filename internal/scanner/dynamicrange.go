package scanner

import (
	"context"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// healDynamicRange re-reads the dynamic range of an unchanged file that an
// older scan tagged "dolby_vision" (a row with no Dolby Vision profile, see
// migration 00036), without leaving the fast skip.
//
// Those scans tagged every file with a Dolby Vision configuration record, and
// every client refuses such a file. detectHDR now tags a file by its base
// layer and keeps "dolby_vision" for profile 5, so a profile 7 / 8.1 release,
// or a file whose record sits over plain SDR video, plays again. The file
// hasn't changed, so nothing else re-probes it: this probes it once, writes
// only hdr_type and dv_profile (the integrity verdict stays), and the stored
// profile stops it from being picked again. A file that can't be probed is
// remembered for the life of the process so it isn't retried on every scan.
func (s *Scanner) healDynamicRange(ctx context.Context, file *media.File, path string) {
	if file == nil || file.HDRType == nil || *file.HDRType != "dolby_vision" || file.DVProfile != nil {
		return
	}
	if _, seen := s.dynamicRangeChecked.LoadOrStore(file.ID, struct{}{}); seen {
		return
	}
	source := path
	if u, err := s.mediaStore().SignedURL(ctx, path, probeURLTTL); err == nil && u != "" {
		source = u
	}
	probe := s.probeFile
	if probe == nil {
		probe = ProbeFile
	}
	res, err := probe(ctx, source)
	if err != nil || res == nil || res.VideoCodec == nil {
		s.logger.WarnContext(ctx, "dynamic range not re-read; file could not be probed",
			"path", path, "file_id", file.ID, "err", err)
		return
	}
	if err := s.media.SetFileDynamicRange(ctx, file.ID, res.HDRType, res.DVProfile); err != nil {
		s.logger.WarnContext(ctx, "dynamic range re-read but not saved",
			"path", path, "file_id", file.ID, "err", err)
		return
	}
	hdr, profile := "sdr", 0
	if res.HDRType != nil {
		hdr = *res.HDRType
	}
	if res.DVProfile != nil {
		profile = *res.DVProfile
	}
	s.logger.InfoContext(ctx, "dynamic range re-read for a file tagged Dolby Vision",
		"path", path, "file_id", file.ID, "hdr_type", hdr, "dv_profile", profile)
	file.HDRType, file.DVProfile = res.HDRType, res.DVProfile
}
