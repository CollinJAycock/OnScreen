package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/notification"
)

// withClaimsForUser is the user-id-bindable variant of withClaims —
// the broker keys subscriptions by user_id, so the publish-side test
// needs to subscribe for the same id the request claims carries.
func withClaimsForUser(r *http.Request, userID uuid.UUID) *http.Request {
	claims := &auth.Claims{
		UserID:    userID,
		Username:  "u",
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

// Transfer publishes a playback.transfer SSE event the receiving
// clients filter by target_client_name. Server doesn't enforce
// per-device routing — the broker fans out per-user, and each
// connected client picks based on its own client name. The contract
// these tests lock:
//   - 400 on missing item_id / target_client_name / non-UUID item_id
//   - 204 on a valid request with the published event carrying the
//     full payload + Type=playback.transfer
//   - position_ms < 0 normalises to 0 rather than failing the request

func TestPlayback_Transfer_Success(t *testing.T) {
	broker := notification.NewBroker()
	userID := uuid.New()
	ch := broker.Subscribe(userID)
	defer broker.Unsubscribe(userID, ch)

	h := NewPlaybackHandler(nil, broker, slog.Default())

	itemID := uuid.New()
	body := bytes.NewBufferString(`{
		"item_id": "` + itemID.String() + `",
		"position_ms": 12345,
		"target_client_name": "Living Room TV"
	}`)
	req := withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", body), userID)
	rec := httptest.NewRecorder()

	h.Transfer(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	// Verify the event landed on the broker channel with the right
	// shape. Use a non-blocking receive — the publish is synchronous,
	// the channel has a buffer of 16, so the event must already be
	// queued by the time Transfer returns.
	select {
	case ev := <-ch:
		if ev.Type != "playback.transfer" {
			t.Errorf("event type: got %q, want playback.transfer", ev.Type)
		}
		var payload TransferPayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.ItemID != itemID.String() {
			t.Errorf("item_id: got %q, want %q", payload.ItemID, itemID)
		}
		if payload.PositionMS != 12345 {
			t.Errorf("position_ms: got %d, want 12345", payload.PositionMS)
		}
		if payload.TargetClientName != "Living Room TV" {
			t.Errorf("target_client_name: got %q, want Living Room TV", payload.TargetClientName)
		}
	default:
		t.Fatal("expected an event on the broker channel; got none")
	}
}

func TestPlayback_Transfer_RejectsMissingItemID(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	body := bytes.NewBufferString(`{"target_client_name":"x"}`)
	req := withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", body), uuid.New())
	rec := httptest.NewRecorder()
	h.Transfer(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPlayback_Transfer_RejectsMissingTarget(t *testing.T) {
	// target_client_name is required — broadcast-to-all-devices
	// isn't supported, since any device receiving the transfer
	// would pause whatever else is playing on it.
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	body := bytes.NewBufferString(`{"item_id":"` + uuid.New().String() + `"}`)
	req := withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", body), uuid.New())
	rec := httptest.NewRecorder()
	h.Transfer(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPlayback_Transfer_RejectsNonUUIDItem(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	body := bytes.NewBufferString(`{"item_id":"not-a-uuid","target_client_name":"x"}`)
	req := withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", body), uuid.New())
	rec := httptest.NewRecorder()
	h.Transfer(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPlayback_Transfer_NormalisesNegativePosition(t *testing.T) {
	// A frontend bug that posts a negative offset (clock-drift,
	// undefined-as-0 coercion gone wrong) shouldn't 400 the user.
	// Normalise to 0 so playback starts from the beginning.
	broker := notification.NewBroker()
	userID := uuid.New()
	ch := broker.Subscribe(userID)
	defer broker.Unsubscribe(userID, ch)

	h := NewPlaybackHandler(nil, broker, slog.Default())
	itemID := uuid.New()
	body := bytes.NewBufferString(`{
		"item_id":"` + itemID.String() + `",
		"position_ms":-500,
		"target_client_name":"Living Room TV"
	}`)
	req := withClaimsForUser(httptest.NewRequest("POST", "/api/v1/playback/transfer", body), userID)
	rec := httptest.NewRecorder()

	h.Transfer(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case ev := <-ch:
		var payload TransferPayload
		_ = json.Unmarshal(ev.Data, &payload)
		if payload.PositionMS != 0 {
			t.Errorf("position_ms: got %d, want 0 (negative normalised)", payload.PositionMS)
		}
	default:
		t.Fatal("expected an event on the broker channel")
	}
}

func TestPlayback_Transfer_RejectsUnauthenticated(t *testing.T) {
	// No claims on the context → 401. Auth_mw.Required handles this
	// in production; the explicit nil-claims check inside the
	// handler is the seatbelt.
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	body := bytes.NewBufferString(`{"item_id":"` + uuid.New().String() + `","target_client_name":"x"}`)
	req := httptest.NewRequest("POST", "/api/v1/playback/transfer", body)
	rec := httptest.NewRecorder()
	h.Transfer(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPlayback_Transfer_RejectsBadJSON(t *testing.T) {
	h := NewPlaybackHandler(nil, notification.NewBroker(), slog.Default())
	req := withClaimsForUser(
		httptest.NewRequest("POST", "/api/v1/playback/transfer", io.NopCloser(strings.NewReader("not-json"))),
		uuid.New(),
	)
	rec := httptest.NewRecorder()
	h.Transfer(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
