package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// mockRegistryDB is an in-memory implementation of registryDB for tests.
type mockRegistryDB struct {
	rows map[uuid.UUID]gen.Plugin

	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
}

func newMockRegistryDB() *mockRegistryDB {
	return &mockRegistryDB{rows: map[uuid.UUID]gen.Plugin{}}
}

func (m *mockRegistryDB) CreatePlugin(_ context.Context, arg gen.CreatePluginParams) (gen.Plugin, error) {
	if m.createErr != nil {
		return gen.Plugin{}, m.createErr
	}
	row := gen.Plugin{
		ID:           uuid.New(),
		Name:         arg.Name,
		Role:         arg.Role,
		Transport:    arg.Transport,
		EndpointUrl:  arg.EndpointUrl,
		AllowedHosts: arg.AllowedHosts,
		Enabled:      arg.Enabled,
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	m.rows[row.ID] = row
	return row, nil
}

func (m *mockRegistryDB) GetPlugin(_ context.Context, id uuid.UUID) (gen.Plugin, error) {
	if m.getErr != nil {
		return gen.Plugin{}, m.getErr
	}
	row, ok := m.rows[id]
	if !ok {
		return gen.Plugin{}, errors.New("not found")
	}
	return row, nil
}

func (m *mockRegistryDB) ListPlugins(_ context.Context) ([]gen.Plugin, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]gen.Plugin, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *mockRegistryDB) ListEnabledPluginsByRole(_ context.Context, role string) ([]gen.Plugin, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := []gen.Plugin{}
	for _, r := range m.rows {
		if r.Role == role && r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockRegistryDB) UpdatePlugin(_ context.Context, arg gen.UpdatePluginParams) (gen.Plugin, error) {
	if m.updateErr != nil {
		return gen.Plugin{}, m.updateErr
	}
	row, ok := m.rows[arg.ID]
	if !ok {
		return gen.Plugin{}, errors.New("not found")
	}
	row.Name = arg.Name
	row.EndpointUrl = arg.EndpointUrl
	row.AllowedHosts = arg.AllowedHosts
	row.Enabled = arg.Enabled
	m.rows[arg.ID] = row
	return row, nil
}

func (m *mockRegistryDB) DeletePlugin(_ context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.rows, id)
	return nil
}

func TestRegistry_Create_Validates(t *testing.T) {
	cases := []struct {
		name    string
		input   CreateInput
		wantErr string
	}{
		{
			name:    "empty name",
			input:   CreateInput{Role: RoleNotification, EndpointURL: "https://example.com/mcp"},
			wantErr: "name must not be empty",
		},
		{
			name:    "invalid role",
			input:   CreateInput{Name: "x", Role: "weather", EndpointURL: "https://example.com/mcp"},
			wantErr: "invalid role",
		},
		{
			name:    "missing scheme",
			input:   CreateInput{Name: "x", Role: RoleNotification, EndpointURL: "example.com/mcp"},
			wantErr: "scheme must be http or https",
		},
		{
			name:    "ftp scheme",
			input:   CreateInput{Name: "x", Role: RoleNotification, EndpointURL: "ftp://example.com/"},
			wantErr: "scheme must be http or https",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(newMockRegistryDB())
			_, err := r.Create(context.Background(), tc.input)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestRegistry_Create_RoundTrip(t *testing.T) {
	db := newMockRegistryDB()
	r := NewRegistry(db)

	created, err := r.Create(context.Background(), CreateInput{
		Name:         "discord-bot",
		Role:         RoleNotification,
		EndpointURL:  "https://hooks.example.com/mcp",
		AllowedHosts: []string{"hooks.example.com", "cdn.example.com"},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Transport != TransportHTTP {
		t.Errorf("expected transport %q, got %q", TransportHTTP, created.Transport)
	}
	if len(created.AllowedHosts) != 2 {
		t.Errorf("expected 2 allowed hosts, got %d", len(created.AllowedHosts))
	}

	// Round-trip the JSONB allowed_hosts column.
	row := db.rows[created.ID]
	var hosts []string
	if err := json.Unmarshal(row.AllowedHosts, &hosts); err != nil {
		t.Fatalf("unmarshal allowed_hosts: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "hooks.example.com" {
		t.Errorf("unexpected hosts in DB: %v", hosts)
	}
}

func TestRegistry_Create_RejectsMalformedHosts(t *testing.T) {
	cases := []struct {
		name  string
		hosts []string
		want  string
	}{
		{"url as host", []string{"https://evil.example.com/x"}, "must be a hostname"},
		{"host with space", []string{"evil example.com"}, "must be a hostname"},
		{"host with path", []string{"example.com/x"}, "must be a hostname"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry(newMockRegistryDB())
			_, err := r.Create(context.Background(), CreateInput{
				Name: "x", Role: RoleNotification,
				EndpointURL: "https://example.com/mcp", AllowedHosts: tc.hosts,
			})
			if err == nil {
				t.Fatalf("expected error for %v, got nil", tc.hosts)
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got %q", tc.want, err.Error())
			}
		})
	}
}

func TestRegistry_Create_RejectsTooManyHosts(t *testing.T) {
	hosts := make([]string, maxAllowedHosts+1)
	for i := range hosts {
		hosts[i] = "ok.example.com"
	}
	r := NewRegistry(newMockRegistryDB())
	_, err := r.Create(context.Background(), CreateInput{
		Name: "x", Role: RoleNotification,
		EndpointURL: "https://example.com/mcp", AllowedHosts: hosts,
	})
	if err == nil || !contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected limit error, got %v", err)
	}
}

func TestRegistry_ListEnabledByRole_FiltersDisabled(t *testing.T) {
	db := newMockRegistryDB()
	r := NewRegistry(db)
	_, _ = r.Create(context.Background(), CreateInput{
		Name: "live", Role: RoleNotification,
		EndpointURL: "https://a.example.com/mcp", Enabled: true,
	})
	_, _ = r.Create(context.Background(), CreateInput{
		Name: "off", Role: RoleNotification,
		EndpointURL: "https://b.example.com/mcp", Enabled: false,
	})
	_, _ = r.Create(context.Background(), CreateInput{
		Name: "wrong-role", Role: RoleMetadata,
		EndpointURL: "https://c.example.com/mcp", Enabled: true,
	})

	got, err := r.ListEnabledByRole(context.Background(), RoleNotification)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Name != "live" {
		t.Errorf("expected [live], got %v", names(got))
	}
}

func names(ps []Plugin) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
