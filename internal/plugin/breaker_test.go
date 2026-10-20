package plugin

import (
	"testing"
	"time"
)

func TestBreaker_OpensAtThresholdAndProbesAfterCooldown(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	b := newBreaker()
	b.now = func() time.Time { return now }

	for i := 1; i < breakerThreshold; i++ {
		if opened, _, _ := b.failure(); opened {
			t.Fatalf("breaker opened after %d failures, threshold is %d", i, breakerThreshold)
		}
		if ok, _ := b.allow(); !ok {
			t.Fatalf("breaker refused a call after %d failures", i)
		}
	}
	opened, until, fails := b.failure()
	if !opened || fails != breakerThreshold {
		t.Fatalf("expected the breaker to open at %d failures, got opened=%v fails=%d", breakerThreshold, opened, fails)
	}
	if want := now.Add(breakerCooldown); !until.Equal(want) {
		t.Errorf("open until = %v, want %v", until, want)
	}
	if ok, _ := b.allow(); ok {
		t.Fatal("open breaker allowed a call")
	}

	// After the cooldown one probe goes through; failing it re-opens at once.
	now = now.Add(breakerCooldown + time.Second)
	if ok, _ := b.allow(); !ok {
		t.Fatal("breaker still refusing after the cooldown")
	}
	if opened, _, _ := b.failure(); !opened {
		t.Fatal("a failed probe should re-open the breaker immediately")
	}

	// A successful probe closes it and reports the recovery once.
	now = now.Add(breakerCooldown + time.Second)
	if !b.success() {
		t.Error("success after failures should report closed=true")
	}
	if b.success() {
		t.Error("a second success should not report a recovery")
	}
	if fails, openUntil := b.state(); fails != 0 || !openUntil.IsZero() {
		t.Errorf("after success: fails=%d openUntil=%v", fails, openUntil)
	}
}
