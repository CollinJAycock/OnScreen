//go:build integration

// Round-trips the plugins-table queries. Plugins define outbound MCP
// egress targets, so a CRUD bug here is a security-critical config
// regression — wrong target host, lost enabled flag, mishandled JSON
// allowed_hosts column.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func newPluginParams(name string, allowedHosts []string, enabled bool) gen.CreatePluginParams {
	hosts, _ := json.Marshal(allowedHosts)
	return gen.CreatePluginParams{
		Name: name,
		Role: "notification",
		// transport is constrained to 'http' in 00036_plugins.sql.
		Transport:    "http",
		EndpointUrl:  "https://" + name + ".example.com/mcp",
		AllowedHosts: hosts,
		Enabled:      enabled,
	}
}

// TestPlugins_Integration_CreateAndGet round-trips a single plugin
// through Create and Get, asserting every field made it into the row
// and back out unchanged. The allowed_hosts column is JSONB so a
// silent serialization drift would show up here first.
func TestPlugins_Integration_CreateAndGet(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	want := newPluginParams("create-and-get-"+uuid.New().String()[:6],
		[]string{"a.example.com", "b.example.com"}, true)
	created, err := q.CreatePlugin(ctx, want)
	if err != nil {
		t.Fatalf("CreatePlugin: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("created plugin has nil ID")
	}
	if created.Name != want.Name || created.Role != want.Role || !created.Enabled {
		t.Errorf("created shape mismatch: %+v", created)
	}

	got, err := q.GetPlugin(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPlugin: %v", err)
	}
	if got.ID != created.ID || got.Name != want.Name || got.EndpointUrl != want.EndpointUrl {
		t.Errorf("get shape mismatch: %+v", got)
	}
	// allowed_hosts roundtrip — JSONB column.
	var hosts []string
	if err := json.Unmarshal(got.AllowedHosts, &hosts); err != nil {
		t.Fatalf("decode allowed_hosts: %v (raw=%s)", err, got.AllowedHosts)
	}
	if len(hosts) != 2 || hosts[0] != "a.example.com" || hosts[1] != "b.example.com" {
		t.Errorf("allowed_hosts round-trip: got %v, want [a.example.com b.example.com]", hosts)
	}
}

// TestPlugins_Integration_ListEnabledByRoleSkipsDisabled is the
// dispatcher's hot-path query. A plugin with Enabled=false must NOT
// appear in this list, otherwise the dispatcher would fan out events
// to disabled targets.
func TestPlugins_Integration_ListEnabledByRoleSkipsDisabled(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	enabled, err := q.CreatePlugin(ctx, newPluginParams("list-on-"+uuid.New().String()[:6], nil, true))
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := q.CreatePlugin(ctx, newPluginParams("list-off-"+uuid.New().String()[:6], nil, false))
	if err != nil {
		t.Fatal(err)
	}

	rows, err := q.ListEnabledPluginsByRole(ctx, "notification")
	if err != nil {
		t.Fatalf("ListEnabledPluginsByRole: %v", err)
	}

	var sawEnabled, sawDisabled bool
	for _, r := range rows {
		if r.ID == enabled.ID {
			sawEnabled = true
		}
		if r.ID == disabled.ID {
			sawDisabled = true
		}
	}
	if !sawEnabled {
		t.Error("enabled plugin should appear in role-filtered list")
	}
	if sawDisabled {
		t.Error("disabled plugin must NOT appear — would dispatch to off targets")
	}
}

// TestPlugins_Integration_ListEnabledByRoleFiltersByRole proves the
// role filter is exclusive: a plugin registered for role "moderation"
// must NOT come back when querying role "notification".
func TestPlugins_Integration_ListEnabledByRoleFiltersByRole(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	notifyParams := newPluginParams("role-notify-"+uuid.New().String()[:6], nil, true)
	notify, err := q.CreatePlugin(ctx, notifyParams)
	if err != nil {
		t.Fatal(err)
	}

	modParams := newPluginParams("role-meta-"+uuid.New().String()[:6], nil, true)
	// Allowed roles are 'notification', 'metadata', 'task' (00036_plugins.sql).
	modParams.Role = "metadata"
	mod, err := q.CreatePlugin(ctx, modParams)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := q.ListEnabledPluginsByRole(ctx, "notification")
	if err != nil {
		t.Fatal(err)
	}
	var sawNotify, sawMod bool
	for _, r := range rows {
		if r.ID == notify.ID {
			sawNotify = true
		}
		if r.ID == mod.ID {
			sawMod = true
		}
	}
	if !sawNotify {
		t.Error("notification plugin should appear")
	}
	if sawMod {
		t.Error("metadata plugin must NOT appear in notification-role list")
	}
}

// TestPlugins_Integration_UpdateChangesEnabledFlag proves the Enabled
// flag can be flipped via UpdatePlugin without losing other fields.
// This is the path the admin "Disable" toggle in the UI uses.
func TestPlugins_Integration_UpdateChangesEnabledFlag(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	created, err := q.CreatePlugin(ctx, newPluginParams("update-"+uuid.New().String()[:6], []string{"x.example.com"}, true))
	if err != nil {
		t.Fatal(err)
	}

	updated, err := q.UpdatePlugin(ctx, gen.UpdatePluginParams{
		ID:           created.ID,
		Name:         created.Name,
		EndpointUrl:  created.EndpointUrl,
		AllowedHosts: created.AllowedHosts,
		Enabled:      false,
	})
	if err != nil {
		t.Fatalf("UpdatePlugin: %v", err)
	}
	if updated.Enabled {
		t.Error("Enabled flag did not flip")
	}
	if updated.UpdatedAt == created.UpdatedAt {
		t.Error("updated_at was not bumped — UPDATE missing NOW() touch?")
	}
	if updated.Role != created.Role {
		t.Errorf("role mutated: got %q, want %q (role is immutable per the SQL)", updated.Role, created.Role)
	}
}

// TestPlugins_Integration_DeleteRemovesRow proves Delete actually
// removes the row, not just toggles a soft-delete flag (the schema
// uses hard delete here — a stale enabled-but-unreachable plugin
// would otherwise burn dispatcher cycles every event).
func TestPlugins_Integration_DeleteRemovesRow(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	created, err := q.CreatePlugin(ctx, newPluginParams("delete-"+uuid.New().String()[:6], nil, true))
	if err != nil {
		t.Fatal(err)
	}

	if err := q.DeletePlugin(ctx, created.ID); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	if _, err := q.GetPlugin(ctx, created.ID); err == nil {
		t.Error("GetPlugin after Delete should return ErrNoRows")
	}
}
