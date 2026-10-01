package scanner

import (
	"context"
	"testing"
	"time"
)

// Only VerifySource's own budget running out is a timeout (SourceUnverified):
// a caller that went away, or a probe that failed in time, is not.
func TestVerifyTimedOut(t *testing.T) {
	parent := context.Background()

	expired, cancel := context.WithTimeout(parent, time.Nanosecond)
	defer cancel()
	<-expired.Done()
	if !verifyTimedOut(parent, expired) {
		t.Error("budget ran out while the caller was live: want a timeout")
	}

	live, cancelLive := context.WithTimeout(parent, time.Hour)
	defer cancelLive()
	if verifyTimedOut(parent, live) {
		t.Error("probe failed within its budget: not a timeout")
	}

	gone, cancelGone := context.WithCancel(parent)
	cancelGone()
	probe, cancelProbe := context.WithTimeout(gone, time.Hour)
	defer cancelProbe()
	if verifyTimedOut(gone, probe) {
		t.Error("the caller went away: not a timeout")
	}
}
