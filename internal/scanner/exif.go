package scanner

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// exifLocalLayouts are the date layouts EXIF DateTimeOriginal uses in the wild.
// The standard is the first one; the rest are tolerated to handle phone cameras
// and editors that ignore the spec.
var exifLocalLayouts = []string{
	"2006:01:02 15:04:05",
	"2006-01-02 15:04:05",
	"2006:01:02T15:04:05",
	"2006-01-02T15:04:05",
}

// PhotoEXIF is the parsed EXIF data the scanner persists for a photo. All
// fields are optional; absent tags simply remain nil.
type PhotoEXIF struct {
	TakenAt       *time.Time
	CameraMake    *string
	CameraModel   *string
	LensModel     *string
	FocalLengthMM *float64
	Aperture      *float64
	ShutterSpeed  *string
	ISO           *int32
	Flash         *bool
	Orientation   *int32
	Width         *int32
	Height        *int32
	GPSLat        *float64
	GPSLon        *float64
	GPSAlt        *float64
	Raw           map[string]any
}

// ExtractEXIF reads EXIF tags from a file path. Returns (nil, nil) when the
// file has no EXIF block — this is normal for PNG/GIF/screenshots and is not
// treated as an error.
func ExtractEXIF(path string) (*PhotoEXIF, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open photo: %w", err)
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		// goexif returns a sentinel for "no EXIF block" — treat as a soft miss.
		if exif.IsCriticalError(err) {
			return nil, nil
		}
		// Non-critical errors (some tags failed to parse) still leave usable data.
	}
	if x == nil {
		return nil, nil
	}

	out := &PhotoEXIF{}

	if t, terr := x.DateTime(); terr == nil {
		tt := t
		out.TakenAt = &tt
	} else {
		// goexif's DateTime parser is strict; fall back to the raw tag and try
		// the layouts we know phone cameras emit.
		if tag, gerr := x.Get(exif.DateTimeOriginal); gerr == nil {
			if s, serr := tag.StringVal(); serr == nil {
				if tt := tryParseEXIFDate(s); tt != nil {
					out.TakenAt = tt
				}
			}
		}
	}

	out.CameraMake = stringTagPtr(x, exif.Make)
	out.CameraModel = stringTagPtr(x, exif.Model)
	out.LensModel = stringTagPtr(x, exif.LensModel)

	if v, ok := ratioTagFloat(x, exif.FocalLength); ok {
		out.FocalLengthMM = &v
	}
	if v, ok := ratioTagFloat(x, exif.FNumber); ok {
		out.Aperture = &v
	}
	if s, ok := exposureTimeString(x); ok {
		out.ShutterSpeed = &s
	}
	if iso, ok := intTag(x, exif.ISOSpeedRatings); ok {
		v := int32(iso)
		out.ISO = &v
	}
	if v, ok := intTag(x, exif.Flash); ok {
		// EXIF Flash is a bitfield; bit 0 is the "flash fired" flag.
		fired := (v & 1) == 1
		out.Flash = &fired
	}
	if v, ok := intTag(x, exif.Orientation); ok {
		o := int32(v)
		out.Orientation = &o
	}
	if v, ok := intTag(x, exif.PixelXDimension); ok {
		w := int32(v)
		out.Width = &w
	}
	if v, ok := intTag(x, exif.PixelYDimension); ok {
		h := int32(v)
		out.Height = &h
	}

	if lat, lon, err := x.LatLong(); err == nil {
		// LatLong returns 0,0 with a non-error result for some malformed tags;
		// reject the literal-zero island unless paired with a non-zero altitude.
		if !(lat == 0 && lon == 0) {
			la, lo := lat, lon
			out.GPSLat = &la
			out.GPSLon = &lo
		}
	}
	if alt, ok := gpsAltitude(x); ok {
		out.GPSAlt = &alt
	}

	out.Raw = collectRawEXIF(x)

	return out, nil
}

// tryParseEXIFDate walks the known layouts. EXIF dates are typically local time
// without a zone; we keep them as-is rather than guessing UTC.
func tryParseEXIFDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0000") {
		return nil
	}
	for _, layout := range exifLocalLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

func stringTagPtr(x *exif.Exif, name exif.FieldName) *string {
	tag, err := x.Get(name)
	if err != nil {
		return nil
	}
	v, err := tag.StringVal()
	if err != nil {
		return nil
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func ratioTagFloat(x *exif.Exif, name exif.FieldName) (float64, bool) {
	tag, err := x.Get(name)
	if err != nil {
		return 0, false
	}
	num, den, err := tag.Rat2(0)
	if err != nil || den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

// exposureTimeString returns shutter speed as "1/250" or "0.5" — the human
// fraction form survives JSON serialization and beats a raw float.
func exposureTimeString(x *exif.Exif) (string, bool) {
	tag, err := x.Get(exif.ExposureTime)
	if err != nil {
		return "", false
	}
	num, den, err := tag.Rat2(0)
	if err != nil || den == 0 {
		return "", false
	}
	if num == 0 {
		return "0", true
	}
	if num == 1 {
		return fmt.Sprintf("1/%d", den), true
	}
	if den == 1 {
		return fmt.Sprintf("%d", num), true
	}
	// Sub-second exposure with non-1 numerator — fall back to decimal.
	v := float64(num) / float64(den)
	if v < 1 {
		// Rephrase as 1/N for readability.
		inv := 1 / v
		if math.Abs(inv-math.Round(inv)) < 0.05 {
			return fmt.Sprintf("1/%d", int(math.Round(inv))), true
		}
	}
	return fmt.Sprintf("%.2f", v), true
}

func intTag(x *exif.Exif, name exif.FieldName) (int, bool) {
	tag, err := x.Get(name)
	if err != nil {
		return 0, false
	}
	v, err := tag.Int(0)
	if err != nil {
		return 0, false
	}
	return v, true
}

func gpsAltitude(x *exif.Exif) (float64, bool) {
	tag, err := x.Get(exif.GPSAltitude)
	if err != nil {
		return 0, false
	}
	num, den, err := tag.Rat2(0)
	if err != nil || den == 0 {
		return 0, false
	}
	alt := float64(num) / float64(den)
	// Altitude ref tag 1 means "below sea level" — invert.
	if ref, rerr := x.Get(exif.GPSAltitudeRef); rerr == nil {
		if v, ierr := ref.Int(0); ierr == nil && v == 1 {
			alt = -alt
		}
	}
	return alt, true
}

// collectRawEXIF dumps every parsed tag into a map for the JSONB catch-all.
// We stringify everything to keep the schema flat and round-trippable.
func collectRawEXIF(x *exif.Exif) map[string]any {
	raw := map[string]any{}
	walker := exifWalker{out: raw}
	_ = x.Walk(walker)
	if len(raw) == 0 {
		return nil
	}
	return raw
}

type exifWalker struct {
	out map[string]any
}

func (w exifWalker) Walk(name exif.FieldName, tag *tiff.Tag) error {
	if tag == nil {
		return nil
	}
	w.out[string(name)] = tag.String()
	return nil
}

// ToUpsertParams maps the parsed EXIF into the sqlc Upsert struct.
func (p *PhotoEXIF) ToUpsertParams(itemID uuid.UUID) gen.UpsertPhotoMetadataParams {
	params := gen.UpsertPhotoMetadataParams{
		ItemID:        itemID,
		CameraMake:    p.CameraMake,
		CameraModel:   p.CameraModel,
		LensModel:     p.LensModel,
		FocalLengthMm: p.FocalLengthMM,
		Aperture:      p.Aperture,
		ShutterSpeed:  p.ShutterSpeed,
		Iso:           p.ISO,
		Flash:         p.Flash,
		Orientation:   p.Orientation,
		Width:         p.Width,
		Height:        p.Height,
		GpsLat:        p.GPSLat,
		GpsLon:        p.GPSLon,
		GpsAlt:        p.GPSAlt,
	}
	if p.TakenAt != nil {
		params.TakenAt = pgtype.Timestamptz{Time: *p.TakenAt, Valid: true}
	}
	if p.Raw != nil {
		if b, err := json.Marshal(p.Raw); err == nil {
			params.RawExif = b
		}
	}
	return params
}
