package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HistoryEventType is a Radarr/Sonarr history event, normalised to the
// camelCase names both apps serialise today ("grabbed", "downloadFailed").
type HistoryEventType string

// History events the download-status sync reads. Radarr and Sonarr share
// these names; Radarr's folder import is movieFolderImported, Sonarr's
// seriesFolderImported.
const (
	EventGrabbed                HistoryEventType = "grabbed"
	EventDownloadFailed         HistoryEventType = "downloadFailed"
	EventDownloadFolderImported HistoryEventType = "downloadFolderImported"
	EventMovieFolderImported    HistoryEventType = "movieFolderImported"
	EventSeriesFolderImported   HistoryEventType = "seriesFolderImported"
	EventDownloadIgnored        HistoryEventType = "downloadIgnored"
)

// canonicalEvents maps a lower-cased event name to its canonical spelling, so
// "DownloadFailed" (older builds) and "downloadFailed" compare equal.
var canonicalEvents = map[string]HistoryEventType{
	"grabbed":                EventGrabbed,
	"downloadfailed":         EventDownloadFailed,
	"downloadfolderimported": EventDownloadFolderImported,
	"moviefolderimported":    EventMovieFolderImported,
	"seriesfolderimported":   EventSeriesFolderImported,
	"downloadignored":        EventDownloadIgnored,
}

// numericEvents are the enum values both apps agree on, for a build that
// serialises the enum as a number.
var numericEvents = map[int]HistoryEventType{
	1: EventGrabbed,
	3: EventDownloadFolderImported,
	4: EventDownloadFailed,
}

// UnmarshalJSON implements json.Unmarshaler: string names are canonicalised
// case-insensitively (unknown names pass through unchanged), known numeric
// enum values are mapped, anything else is "".
func (e *HistoryEventType) UnmarshalJSON(b []byte) error {
	*e = ""
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		if c, ok := canonicalEvents[strings.ToLower(s)]; ok {
			*e = c
		} else {
			*e = HistoryEventType(s)
		}
		return nil
	}
	if n, err := strconv.Atoi(string(b)); err == nil {
		*e = numericEvents[n]
	}
	return nil
}

// IsImport reports whether the event means a file was imported.
func (e HistoryEventType) IsImport() bool {
	switch e {
	case EventDownloadFolderImported, EventMovieFolderImported, EventSeriesFolderImported:
		return true
	case EventGrabbed, EventDownloadFailed, EventDownloadIgnored:
		return false
	}
	return false
}

// HistoryRecord is one row of a Radarr/Sonarr history listing.
type HistoryRecord struct {
	ID          int              `json:"id"`
	MovieID     int              `json:"movieId"`
	SeriesID    int              `json:"seriesId"`
	EpisodeID   int              `json:"episodeId"`
	SourceTitle string           `json:"sourceTitle"`
	EventType   HistoryEventType `json:"eventType"`
	Date        Timestamp        `json:"date"`
	DownloadID  string           `json:"downloadId"`
	// Data is the event's free-form detail (all values are strings in
	// practice); a downloadFailed row carries "message".
	Data map[string]any `json:"data"`
}

// Message returns the failure / detail text the app recorded on the event,
// or "".
func (h HistoryRecord) Message() string {
	for _, k := range []string{"message", "reason", "statusMessages"} {
		if s, ok := h.Data[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// MovieHistory returns every history event Radarr recorded for one movie
// (GET /api/v3/history/movie).
func (c *Client) MovieHistory(ctx context.Context, movieID int) ([]HistoryRecord, error) {
	return c.history(ctx, "/api/v3/history/movie", url.Values{"movieId": {strconv.Itoa(movieID)}})
}

// SeriesHistory returns every history event Sonarr recorded for one series
// (GET /api/v3/history/series).
func (c *Client) SeriesHistory(ctx context.Context, seriesID int) ([]HistoryRecord, error) {
	return c.history(ctx, "/api/v3/history/series", url.Values{"seriesId": {strconv.Itoa(seriesID)}})
}

func (c *Client) history(ctx context.Context, path string, q url.Values) ([]HistoryRecord, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, q, nil, &raw); err != nil {
		return nil, err
	}
	records, _, _, err := decodeRecordList(raw)
	if err != nil {
		return nil, fmt.Errorf("arr: decode history: %w", err)
	}
	out := make([]HistoryRecord, 0, len(records))
	for _, rec := range records {
		var h HistoryRecord
		if decodeObject(rec, &h) != nil {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}
