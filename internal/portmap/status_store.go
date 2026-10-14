package portmap

import (
	"context"
	"encoding/json"
	"time"
)

// KV is the shared store the status travels through. *valkey.Client
// satisfies it.
type KV interface {
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Get(ctx context.Context, key string) (string, error)
}

const (
	sharedStatusKey = "remoteaccess:status"
	// sharedStatusTTL outlives a few checks; a master that dies stops
	// refreshing it and the status reads as unknown soon after.
	sharedStatusTTL = 2 * time.Minute
)

// SharedStatus publishes the mapping node's status for every node: only the
// master maps, but the admin's request can land on any node.
type SharedStatus struct{ KV KV }

// Publish stores st (Mapper.WithPublisher). Best-effort.
func (s SharedStatus) Publish(ctx context.Context, st Status) {
	if s.KV == nil {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = s.KV.Set(ctx, sharedStatusKey, string(b), sharedStatusTTL)
}

// Load reads the last published status; ok=false when none is current.
func (s SharedStatus) Load(ctx context.Context) (Status, bool) {
	if s.KV == nil {
		return Status{}, false
	}
	raw, err := s.KV.Get(ctx, sharedStatusKey)
	if err != nil || raw == "" {
		return Status{}, false
	}
	var st Status
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return Status{}, false
	}
	return st, true
}
