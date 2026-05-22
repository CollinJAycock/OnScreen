package main

import (
	"math"
	"testing"
)

// Regression: float64PtrToNumeric must actually populate the value. It used
// to call pgtype.Numeric.Scan(float64), which returns "cannot scan float64"
// and leaves the value invalid → every Numeric column we wrote from a float
// (frame_rate, replaygain_*, item rating / audience_rating) was silently
// persisted NULL. The fix scans the decimal string form instead.
func TestFloat64PtrToNumeric(t *testing.T) {
	f := 23.976
	n := float64PtrToNumeric(&f)
	if !n.Valid {
		t.Fatal("float64PtrToNumeric returned an invalid (NULL) Numeric — the float64 conversion is broken again")
	}
	got, err := n.Float64Value()
	if err != nil || !got.Valid {
		t.Fatalf("Float64Value: err=%v valid=%v", err, got.Valid)
	}
	if math.Abs(got.Float64-f) > 1e-9 {
		t.Errorf("round-trip = %v, want %v", got.Float64, f)
	}

	// nil → NULL (Valid=false), and no panic.
	if n := float64PtrToNumeric(nil); n.Valid {
		t.Error("nil input should yield an invalid (NULL) Numeric")
	}
}
