package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/onscreen/onscreen/internal/rawpreview"
)

// ExtractRAWEXIF reads the EXIF fields of a camera RAW so it lands on the
// map and timeline like a JPEG. Same (nil, nil) "nothing usable" contract as
// ExtractEXIFReader. The reads are bounded by internal/rawpreview.
func ExtractRAWEXIF(rs io.ReadSeeker) (*PhotoEXIF, error) {
	ra, size, err := rawpreview.ReaderAt(rs)
	if err != nil {
		return nil, fmt.Errorf("raw size: %w", err)
	}
	res, err := rawpreview.ExtractMetadata(ra, size)
	if err != nil && !errors.Is(err, rawpreview.ErrNoPreview) {
		if errors.Is(err, rawpreview.ErrNotRAW) {
			return nil, nil
		}
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return photoEXIFFromRAW(res), nil
}

// photoEXIFFromRAW maps rawpreview metadata onto the scanner's EXIF shape.
func photoEXIFFromRAW(res *rawpreview.Result) *PhotoEXIF {
	m := res.Meta
	out := &PhotoEXIF{}
	if m.DateTimeOriginal != "" {
		out.TakenAt = tryParseEXIFDate(m.DateTimeOriginal)
	}
	out.CameraMake = nonEmpty(m.Make)
	out.CameraModel = nonEmpty(m.Model)
	out.LensModel = nonEmpty(m.LensModel)
	out.FocalLengthMM = m.FocalLength
	out.Aperture = m.FNumber
	if m.ExposureDen != 0 {
		s := exposureFraction(m.ExposureNum, m.ExposureDen)
		out.ShutterSpeed = &s
	}
	if m.ISO > 0 {
		v := int32(m.ISO)
		out.ISO = &v
	}
	if m.Flash != nil {
		fired := (*m.Flash & 1) == 1
		out.Flash = &fired
	}
	if m.Orientation > 0 {
		o := int32(m.Orientation)
		out.Orientation = &o
	}
	if res.Preview != nil {
		w, h := int32(res.Preview.Width), int32(res.Preview.Height)
		out.Width, out.Height = &w, &h
	}
	out.GPSLat, out.GPSLon, out.GPSAlt = m.GPSLat, m.GPSLon, m.GPSAlt
	raw := map[string]any{"Format": res.Format}
	if m.Make != "" {
		raw["Make"] = m.Make
	}
	if m.Model != "" {
		raw["Model"] = m.Model
	}
	out.Raw = raw
	return out
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// exposureFraction formats ExposureTime like exposureTimeString does for
// JPEG EXIF ("1/250", "2", "0.30").
func exposureFraction(num, den uint32) string {
	switch {
	case num == 0:
		return "0"
	case num == 1:
		return fmt.Sprintf("1/%d", den)
	case den == 1:
		return fmt.Sprintf("%d", num)
	}
	v := float64(num) / float64(den)
	if v < 1 {
		inv := 1 / v
		if r := float64(int(inv + 0.5)); inv-r < 0.05 && r-inv < 0.05 {
			return fmt.Sprintf("1/%d", int(r))
		}
	}
	return fmt.Sprintf("%.2f", v)
}

// probeRAW returns the probe result for a camera RAW: the container from
// the extension and the dimensions of the preview the RAW names.
func (s *Scanner) probeRAW(ctx context.Context, path string) *ProbeResult {
	container := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	out := &ProbeResult{Container: &container}
	f, err := s.mediaStore().Open(ctx, path)
	if err != nil {
		return out
	}
	defer f.Close()
	ra, size, err := rawpreview.ReaderAt(f)
	if err != nil {
		return out
	}
	res, _ := rawpreview.ExtractMetadata(ra, size)
	if res != nil && res.Preview != nil {
		w, h := res.Preview.Width, res.Preview.Height
		out.ResolutionW, out.ResolutionH = &w, &h
	}
	return out
}
