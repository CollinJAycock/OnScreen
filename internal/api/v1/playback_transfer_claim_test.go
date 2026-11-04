package v1

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/notification"
)

// A transfer is left for /playback/transfer/claim as well as published:
// the polling client (a Roku) gets it once, by its own name, within the
// TTL, and nobody else's.

func transferTo(t *testing.T, h *PlaybackHandler, user uuid.UUID, item uuid.UUID, pos int64, target string) {
	t.Helper()
	body, _ := json.Marshal(TransferRequest{ItemID: item.String(), PositionMS: pos, TargetClientName: target})
	rec := httptest.NewRecorder()
	h.Transfer(rec, withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", bytes.NewReader(body)), user))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("transfer: status %d body=%s", rec.Code, rec.Body.String())
	}
}

func claim(h *PlaybackHandler, user uuid.UUID, name string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(ClaimTransferRequest{ClientName: name})
	rec := httptest.NewRecorder()
	h.ClaimTransfer(rec, withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer/claim", bytes.NewReader(body)), user))
	return rec
}

func claimedPayload(t *testing.T, rec *httptest.ResponseRecorder) TransferPayload {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("claim: status %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data TransferPayload `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("claim body: %v", err)
	}
	return env.Data
}

func TestPlayback_ClaimTransfer_OnceByName(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	user, item := uuid.New(), uuid.New()
	transferTo(t, h, user, item, 61000, "Roku")

	if rec := claim(h, user, "Living Room TV"); rec.Code != http.StatusNoContent {
		t.Fatalf("another name: status %d, want 204", rec.Code)
	}
	got := claimedPayload(t, claim(h, user, " roku "))
	if got.ItemID != item.String() || got.PositionMS != 61000 || got.TargetClientName != "Roku" {
		t.Errorf("claimed %+v", got)
	}
	if rec := claim(h, user, "Roku"); rec.Code != http.StatusNoContent {
		t.Errorf("second claim: status %d, want 204 (a transfer plays once)", rec.Code)
	}
}

func TestPlayback_ClaimTransfer_OtherUserNeverSeesIt(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	owner := uuid.New()
	transferTo(t, h, owner, uuid.New(), 0, "Roku")
	if rec := claim(h, uuid.New(), "Roku"); rec.Code != http.StatusNoContent {
		t.Fatalf("other user: status %d, want 204", rec.Code)
	}
	claimedPayload(t, claim(h, owner, "Roku"))
}

func TestPlayback_ClaimTransfer_NewerReplacesOlder(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	user, second := uuid.New(), uuid.New()
	transferTo(t, h, user, uuid.New(), 0, "Roku")
	transferTo(t, h, user, second, 5000, "Roku")
	if got := claimedPayload(t, claim(h, user, "Roku")); got.ItemID != second.String() {
		t.Errorf("claimed %s, want the newer %s", got.ItemID, second)
	}
}

func TestPlayback_ClaimTransfer_ExpiresAfterTTL(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	now := time.Now()
	h.pending.now = func() time.Time { return now }
	user := uuid.New()
	transferTo(t, h, user, uuid.New(), 0, "Roku")
	now = now.Add(pendingTransferTTL)
	if rec := claim(h, user, "Roku"); rec.Code != http.StatusNoContent {
		t.Fatalf("stale: status %d, want 204", rec.Code)
	}
}

func TestPlayback_ClaimTransfer_SweepDropsStale(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	now := time.Now()
	h.pending.now = func() time.Time { return now }
	transferTo(t, h, uuid.New(), uuid.New(), 0, "Old TV")
	now = now.Add(pendingTransferTTL + time.Second)
	transferTo(t, h, uuid.New(), uuid.New(), 0, "Roku")
	if n := len(h.pending.m); n != 1 {
		t.Errorf("users with pending transfers: %d, want 1 (the stale one swept)", n)
	}
}

func TestPlayback_ClaimTransfer_BadRequests(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	if rec := claim(h, uuid.New(), "  "); rec.Code != http.StatusBadRequest {
		t.Errorf("blank name: status %d, want 400", rec.Code)
	}
	rec := httptest.NewRecorder()
	h.ClaimTransfer(rec, withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer/claim", bytes.NewBufferString("{")), uuid.New()))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: status %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ClaimTransfer(rec, httptest.NewRequest("POST", "/api/v1/playback/transfer/claim", bytes.NewBufferString(`{"client_name":"Roku"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: status %d, want 401", rec.Code)
	}
}
