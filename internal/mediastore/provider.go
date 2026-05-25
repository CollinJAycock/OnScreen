package mediastore

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Provider is a hot-swappable Store: it delegates every call to the backend
// currently configured, and Set atomically replaces that backend when the admin
// changes storage settings — so the serving and transcode tiers pick up a new
// backend without a restart. Because it satisfies Store itself, handlers hold it
// via WithMediaStore exactly like any other backend and never learn it can swap.
//
// A plain RWMutex (not atomic.Value) guards the backend because the concrete
// type changes across a swap (Local ⇆ *S3), which atomic.Value forbids. Reads
// happen per stream request; the read-lock is uncontended except during the
// rare Set.
type Provider struct {
	mu  sync.RWMutex
	cur Store
}

var (
	_ Store  = (*Provider)(nil)
	_ Lister = (*Provider)(nil)
)

// NewProvider returns a Provider serving from s, or from Local when s is nil.
func NewProvider(s Store) *Provider {
	if s == nil {
		s = Local{}
	}
	return &Provider{cur: s}
}

// Set replaces the active backend. A nil backend resets to Local (the safe
// default), so a misconfigured swap degrades to local serving rather than
// nil-panicking the serve path.
func (p *Provider) Set(s Store) {
	if s == nil {
		s = Local{}
	}
	p.mu.Lock()
	p.cur = s
	p.mu.Unlock()
}

func (p *Provider) current() Store {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cur
}

// Open implements Store.
func (p *Provider) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	return p.current().Open(ctx, key)
}

// Stat implements Store.
func (p *Provider) Stat(ctx context.Context, key string) (FileInfo, error) {
	return p.current().Stat(ctx, key)
}

// SignedURL implements Store.
func (p *Provider) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return p.current().SignedURL(ctx, key, ttl)
}

// Walk implements Lister by delegating to the active backend when it supports
// listing. A backend that isn't a Lister yields a clear error rather than a
// panic, so a caller can fall back (e.g. the scanner keeps its local walk).
func (p *Provider) Walk(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	cur := p.current()
	l, ok := cur.(Lister)
	if !ok {
		return fmt.Errorf("mediastore: backend %T does not support listing", cur)
	}
	return l.Walk(ctx, prefix, fn)
}
