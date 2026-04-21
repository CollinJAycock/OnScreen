package subtitles

import (
	"context"
	"sync"

	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
)

// OpenSubtitlesCreds is the subset of stored settings needed to build a client.
// A separate type keeps this package independent of the settings package.
type OpenSubtitlesCreds struct {
	Enabled  bool
	APIKey   string
	Username string
	Password string
}

// DynamicProvider builds and caches an opensubtitles.Client based on the
// credentials returned by credsFn. When the credentials change (e.g. the user
// updates them via the settings page) the cached client is replaced on the
// next call. This lets settings changes take effect without a server restart.
type DynamicProvider struct {
	credsFn   func(context.Context) OpenSubtitlesCreds
	userAgent string

	mu     sync.Mutex
	cached OpenSubtitlesCreds
	client *opensubtitles.Client
}

// NewDynamicProvider wires the provider to a credentials source.
func NewDynamicProvider(credsFn func(context.Context) OpenSubtitlesCreds, userAgent string) *DynamicProvider {
	return &DynamicProvider{credsFn: credsFn, userAgent: userAgent}
}

// current returns a client matching the latest credentials, rebuilding only
// when something changed. Returns nil when disabled or APIKey is empty.
func (d *DynamicProvider) current(ctx context.Context) *opensubtitles.Client {
	creds := d.credsFn(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if !creds.Enabled || creds.APIKey == "" {
		d.client = nil
		d.cached = creds
		return nil
	}
	if d.client != nil && d.cached == creds {
		return d.client
	}
	d.client = opensubtitles.New(creds.APIKey, creds.Username, creds.Password, d.userAgent)
	d.cached = creds
	return d.client
}

func (d *DynamicProvider) Configured() bool {
	return d.current(context.Background()) != nil
}

func (d *DynamicProvider) Search(ctx context.Context, opts opensubtitles.SearchOpts) ([]opensubtitles.SearchResult, error) {
	c := d.current(ctx)
	if c == nil {
		return nil, ErrNoProvider
	}
	return c.Search(ctx, opts)
}

func (d *DynamicProvider) Download(ctx context.Context, fileID int) (*opensubtitles.DownloadInfo, error) {
	c := d.current(ctx)
	if c == nil {
		return nil, ErrNoProvider
	}
	return c.Download(ctx, fileID)
}

func (d *DynamicProvider) FetchFile(ctx context.Context, link string) ([]byte, error) {
	c := d.current(ctx)
	if c == nil {
		return nil, ErrNoProvider
	}
	return c.FetchFile(ctx, link)
}
