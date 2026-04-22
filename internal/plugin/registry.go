package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// registryDB is the subset of sqlc-generated queries the registry needs.
// Defined as an interface so tests can mock it without spinning Postgres.
type registryDB interface {
	CreatePlugin(ctx context.Context, arg gen.CreatePluginParams) (gen.Plugin, error)
	GetPlugin(ctx context.Context, id uuid.UUID) (gen.Plugin, error)
	ListPlugins(ctx context.Context) ([]gen.Plugin, error)
	ListEnabledPluginsByRole(ctx context.Context, role string) ([]gen.Plugin, error)
	UpdatePlugin(ctx context.Context, arg gen.UpdatePluginParams) (gen.Plugin, error)
	DeletePlugin(ctx context.Context, id uuid.UUID) error
}

// Registry is the persistence layer for plugin records. It hides the sqlc
// gen.Plugin type behind the domain Plugin and validates inputs so the rest
// of the codebase can treat plugins as already-well-formed.
type Registry struct {
	db registryDB
}

// NewRegistry constructs a Registry from a sqlc Queries (or any conforming
// interface in tests).
func NewRegistry(db registryDB) *Registry {
	return &Registry{db: db}
}

// CreateInput is the validated argument set for Registry.Create.
type CreateInput struct {
	Name         string
	Role         Role
	EndpointURL  string
	AllowedHosts []string
	Enabled      bool
}

// UpdateInput is the validated argument set for Registry.Update. Role and
// transport are immutable after creation — change of role would invalidate
// every dispatch site that already queried by role.
type UpdateInput struct {
	Name         string
	EndpointURL  string
	AllowedHosts []string
	Enabled      bool
}

// ErrPluginNotFound is returned by Get when no row matches the supplied ID.
var ErrPluginNotFound = errors.New("plugin not found")

// Create validates and inserts a new plugin row.
func (r *Registry) Create(ctx context.Context, in CreateInput) (Plugin, error) {
	if err := validateName(in.Name); err != nil {
		return Plugin{}, err
	}
	if !ValidRole(string(in.Role)) {
		return Plugin{}, fmt.Errorf("invalid role %q", in.Role)
	}
	if err := validateEndpoint(in.EndpointURL); err != nil {
		return Plugin{}, err
	}
	hostsJSON, err := encodeHosts(in.AllowedHosts)
	if err != nil {
		return Plugin{}, err
	}
	row, err := r.db.CreatePlugin(ctx, gen.CreatePluginParams{
		Name:         in.Name,
		Role:         string(in.Role),
		Transport:    string(TransportHTTP),
		EndpointUrl:  in.EndpointURL,
		AllowedHosts: hostsJSON,
		Enabled:      in.Enabled,
	})
	if err != nil {
		return Plugin{}, err
	}
	return fromGen(row), nil
}

// Get returns a single plugin or ErrPluginNotFound.
func (r *Registry) Get(ctx context.Context, id uuid.UUID) (Plugin, error) {
	row, err := r.db.GetPlugin(ctx, id)
	if err != nil {
		return Plugin{}, ErrPluginNotFound
	}
	return fromGen(row), nil
}

// List returns every registered plugin, ordered by name.
func (r *Registry) List(ctx context.Context) ([]Plugin, error) {
	rows, err := r.db.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Plugin, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromGen(row))
	}
	return out, nil
}

// ListEnabledByRole returns every enabled plugin in the supplied role.
// Used by the dispatcher fan-out path.
func (r *Registry) ListEnabledByRole(ctx context.Context, role Role) ([]Plugin, error) {
	rows, err := r.db.ListEnabledPluginsByRole(ctx, string(role))
	if err != nil {
		return nil, err
	}
	out := make([]Plugin, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromGen(row))
	}
	return out, nil
}

// Update applies a new name/endpoint/allowlist/enabled to an existing plugin.
func (r *Registry) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Plugin, error) {
	if err := validateName(in.Name); err != nil {
		return Plugin{}, err
	}
	if err := validateEndpoint(in.EndpointURL); err != nil {
		return Plugin{}, err
	}
	hostsJSON, err := encodeHosts(in.AllowedHosts)
	if err != nil {
		return Plugin{}, err
	}
	row, err := r.db.UpdatePlugin(ctx, gen.UpdatePluginParams{
		ID:           id,
		Name:         in.Name,
		EndpointUrl:  in.EndpointURL,
		AllowedHosts: hostsJSON,
		Enabled:      in.Enabled,
	})
	if err != nil {
		return Plugin{}, err
	}
	return fromGen(row), nil
}

// Delete removes a plugin record.
func (r *Registry) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.DeletePlugin(ctx, id)
}

// maxAllowedHosts caps the per-plugin allowlist size. Well above any realistic
// configuration; exists to keep a pathological UI submission from blowing up
// in-memory data structures.
const maxAllowedHosts = 64

// maxHostLength is the DNS name length limit (RFC 1035 §2.3.4).
const maxHostLength = 253

func validateName(name string) error {
	if name == "" {
		return errors.New("name must not be empty")
	}
	if len(name) > 200 {
		return errors.New("name must be at most 200 characters")
	}
	return nil
}

func validateEndpoint(raw string) error {
	if raw == "" {
		return errors.New("endpoint_url must not be empty")
	}
	if len(raw) > 2048 {
		return errors.New("endpoint_url must be at most 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint_url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("endpoint_url must include a host")
	}
	if h := u.Hostname(); len(h) > maxHostLength {
		return fmt.Errorf("endpoint_url host exceeds %d characters", maxHostLength)
	}
	return nil
}

// validateHost enforces that an allowlist entry is a plausible hostname:
// non-empty after trim, within DNS length limits, and free of path/scheme
// fragments (an admin typing "https://api.example.com/x" by mistake should
// fail loudly rather than silently never matching).
func validateHost(h string) error {
	if h == "" {
		return errors.New("allowed host must not be empty")
	}
	if len(h) > maxHostLength {
		return fmt.Errorf("allowed host %q exceeds %d characters", h, maxHostLength)
	}
	if strings.ContainsAny(h, "/?#@ \t") {
		return fmt.Errorf("allowed host %q must be a hostname, not a URL", h)
	}
	return nil
}

func encodeHosts(hosts []string) ([]byte, error) {
	if len(hosts) > maxAllowedHosts {
		return nil, fmt.Errorf("allowed_hosts exceeds limit of %d entries", maxAllowedHosts)
	}
	cleaned := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if err := validateHost(h); err != nil {
			return nil, err
		}
		cleaned = append(cleaned, h)
	}
	return json.Marshal(cleaned)
}
