package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HealthCheck is one row of GET /api/v3/health: a problem the app has
// detected with itself (indexers down, a download client unreachable, a root
// folder missing, an update available ...).
type HealthCheck struct {
	Source string `json:"source"`
	// Type is ok, notice, warning or error (lower-cased on decode).
	Type    string     `json:"type"`
	Message string     `json:"message"`
	WikiURL FlexString `json:"wikiUrl"`
}

// Health fetches the app's current health checks. Rows reporting "ok" are
// dropped — the endpoint only lists problems, but some builds echo a
// resolved check once as ok.
func (c *Client) Health(ctx context.Context) ([]HealthCheck, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v3/health", nil, nil, &raw); err != nil {
		return nil, err
	}
	records, _, _, err := decodeRecordList(raw)
	if err != nil {
		return nil, fmt.Errorf("arr: decode health: %w", err)
	}
	out := make([]HealthCheck, 0, len(records))
	for _, rec := range records {
		var h HealthCheck
		if decodeObject(rec, &h) != nil {
			continue
		}
		h.Type = strings.ToLower(strings.TrimSpace(h.Type))
		if h.Type == "ok" || (h.Type == "" && strings.TrimSpace(h.Message) == "") {
			continue
		}
		if h.Type == "" {
			h.Type = "warning"
		}
		out = append(out, h)
	}
	return out, nil
}

// DiskSpace is one row of GET /api/v3/diskspace: a mount the app can see.
type DiskSpace struct {
	Path       string    `json:"path"`
	Label      string    `json:"label"`
	FreeSpace  FlexFloat `json:"freeSpace"`
	TotalSpace FlexFloat `json:"totalSpace"`
}

// DiskSpace fetches free / total bytes for every mount the app reports.
func (c *Client) DiskSpace(ctx context.Context) ([]DiskSpace, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v3/diskspace", nil, nil, &raw); err != nil {
		return nil, err
	}
	records, _, _, err := decodeRecordList(raw)
	if err != nil {
		return nil, fmt.Errorf("arr: decode diskspace: %w", err)
	}
	out := make([]DiskSpace, 0, len(records))
	for _, rec := range records {
		var d DiskSpace
		if decodeObject(rec, &d) != nil || strings.TrimSpace(d.Path) == "" {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
