//go:build integration

package settings

import (
	"context"
	"errors"
	"testing"
)

// TestService_Integration_DVRConfigRoundTrip: nothing stored reads back as
// all-off; a valid config round-trips; an invalid one is refused and leaves
// the stored config alone.
func TestService_Integration_DVRConfigRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test (testcontainer)")
	}
	svc := newServiceForTest(t)
	ctx := context.Background()

	if got := svc.DVR(ctx); got != (DVRConfig{}).WithDefaults() {
		t.Fatalf("initial = %+v, want all-off defaults", got)
	}
	want := DVRConfig{
		Remux: true, RemuxContainer: DVRContainerMP4,
		Transcode: true, TranscodeCodec: DVRCodecHEVC, TranscodeQuality: DVRQualitySmall,
		DeleteOriginal: true, Commercials: DVRCommercialsCut,
	}
	if err := svc.SetDVR(ctx, want); err != nil {
		t.Fatalf("SetDVR: %v", err)
	}
	if got := svc.DVR(ctx); got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if err := svc.SetDVR(ctx, DVRConfig{Commercials: DVRCommercialsCut}); !errors.Is(err, ErrInvalidDVRConfig) {
		t.Fatalf("cut without a rewrite: err = %v", err)
	}
	if got := svc.DVR(ctx); got != want {
		t.Fatalf("refused save changed the stored config: %+v", got)
	}
}
