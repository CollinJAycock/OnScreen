package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/requests"
)

type fakeDownloadSyncer struct {
	gotFilter *requests.SyncFilter
	res       requests.SyncResult
	err       error
}

func (f *fakeDownloadSyncer) SyncDownloads(_ context.Context, filter requests.SyncFilter) (requests.SyncResult, error) {
	f.gotFilter = &filter
	return f.res, f.err
}

func TestArrRequestSyncHandler_SyncsEverythingAndSummarises(t *testing.T) {
	f := &fakeDownloadSyncer{res: requests.SyncResult{Requests: 4, Services: 2, Updated: 3, Failed: 1, ServiceErrors: 1}}
	out, err := NewArrRequestSyncHandler(f).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.gotFilter == nil || *f.gotFilter != (requests.SyncFilter{}) {
		t.Errorf("filter = %+v, want the zero (sync everything) filter", f.gotFilter)
	}
	for _, want := range []string{"4 requests", "2 arr services", "3 updated", "1 failed", "1 service errors"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary %q missing %q", out, want)
		}
	}
}

func TestArrRequestSyncHandler_PropagatesError(t *testing.T) {
	f := &fakeDownloadSyncer{err: errors.New("db down")}
	if _, err := NewArrRequestSyncHandler(f).Run(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("err = %v, want the wrapped DB error", err)
	}
}
