// Package testvalkey provides an in-process Redis server for integration tests.
package testvalkey

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/onscreen/onscreen/internal/valkey"
)

// New starts an in-process miniredis server and returns a connected *valkey.Client.
// The server is stopped when the test ends via t.Cleanup.
func New(t *testing.T) *valkey.Client {
	t.Helper()

	s := miniredis.RunT(t)

	client, err := valkey.New(context.Background(), fmt.Sprintf("redis://%s", s.Addr()))
	if err != nil {
		t.Fatalf("connect to miniredis: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}
