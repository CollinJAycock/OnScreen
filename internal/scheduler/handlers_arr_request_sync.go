package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onscreen/onscreen/internal/requests"
)

// RequestDownloadSyncer is the request-service surface the arr_request_sync
// task drives. Satisfied by *requests.Service.
type RequestDownloadSyncer interface {
	SyncDownloads(ctx context.Context, filter requests.SyncFilter) (requests.SyncResult, error)
}

// NewArrRequestSyncHandler returns the arr_request_sync task: refresh the live
// Radarr/Sonarr download state of every in-flight media request (searching,
// queued, downloading, import pending, stalled, failed), fire the one-time
// failure notices, and move requests whose failed download was re-grabbed
// back to downloading. The arr webhook triggers targeted syncs between runs;
// this sweep is what catches everything else (no webhook configured, missed
// deliveries, progress while a download runs).
//
// An unreachable or disabled instance doesn't fail the run — its requests
// keep their last state and the summary counts the service error. Only a
// database failure listing the work is an error.
func NewArrRequestSyncHandler(svc RequestDownloadSyncer) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		res, err := svc.SyncDownloads(ctx, requests.SyncFilter{})
		if err != nil {
			return "", fmt.Errorf("arr request sync: %w", err)
		}
		return res.String(), nil
	}
}
