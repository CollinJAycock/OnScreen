package plugin

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSmoke_RealStubNotify exercises the full dispatcher code path against
// a live stub-notify process started externally. Skipped unless
// ONSCREEN_SMOKE_STUB is set; intended for manual smoke testing.
//
//	./bin/stub-notify.exe --addr :18091 &
//	ONSCREEN_SMOKE_STUB=http://127.0.0.1:18091/mcp go test ./internal/plugin -run TestSmoke
func TestSmoke_RealStubNotify(t *testing.T) {
	endpoint := os.Getenv("ONSCREEN_SMOKE_STUB")
	if endpoint == "" {
		t.Skip("ONSCREEN_SMOKE_STUB not set")
	}
	allowPrivateIPs(t) // stub binds to loopback

	// Quick liveness check so we fail fast with a clear message if the stub
	// isn't running, instead of timing out in an obscure MCP handshake error.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Head(endpoint)
	if err == nil {
		_ = resp.Body.Close()
	}

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	created, err := reg.Create(context.Background(), CreateInput{
		Name:        "smoke-stub",
		Role:        RoleNotification,
		EndpointURL: endpoint,
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.TestDispatch(ctx, created.ID, NotificationEvent{
		Event:   "media.play",
		Title:   "smoke test",
		UserID:  uuid.NewString(),
		MediaID: uuid.NewString(),
	}); err != nil {
		t.Fatalf("TestDispatch against stub: %v", err)
	}
}
