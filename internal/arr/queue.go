package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Queue paging bounds. A page of 250 keeps each response a few hundred KB even
// with the embedded movie/series blocks; 20 pages (5000 downloads) is far past
// any real queue, and exists only so a misbehaving instance that ignores
// paging can't keep the sync looping.
const (
	queuePageSize = 250
	queueMaxPages = 20
)

// QueueRecord is one row of GET /api/v3/queue on Radarr or Sonarr: a release
// the app has handed to a download client and is tracking until import.
// Radarr rows carry MovieID; Sonarr rows SeriesID + EpisodeID, and a season
// pack appears once per episode, every copy sharing the pack's DownloadID and
// total Size/SizeLeft.
type QueueRecord struct {
	ID        int `json:"id"`
	MovieID   int `json:"movieId"`
	SeriesID  int `json:"seriesId"`
	EpisodeID int `json:"episodeId"`
	// SeasonNumber is set on Sonarr v4 rows; older builds only carry it
	// inside Episode.
	SeasonNumber int    `json:"seasonNumber"`
	Title        string `json:"title"`
	// Size / SizeLeft are bytes, sent as floating-point numbers.
	Size     FlexFloat `json:"size"`
	SizeLeft FlexFloat `json:"sizeleft"`
	// TimeLeft is a .NET TimeSpan ("01:02:03"); EstimatedCompletionTime is
	// the absolute form. Either can be missing while a download is queued.
	TimeLeft                string    `json:"timeleft"`
	EstimatedCompletionTime Timestamp `json:"estimatedCompletionTime"`
	// Status is the download client's view: queued, paused, downloading,
	// completed, delay, downloadClientUnavailable, warning, failed, fallback.
	Status string `json:"status"`
	// TrackedDownloadStatus is the app's health verdict: ok, warning, error.
	TrackedDownloadStatus string `json:"trackedDownloadStatus"`
	// TrackedDownloadState is where the app is in its pipeline: downloading,
	// importBlocked, importPending, importing, imported, failedPending,
	// failed, ignored.
	TrackedDownloadState string          `json:"trackedDownloadState"`
	StatusMessages       []StatusMessage `json:"statusMessages"`
	ErrorMessage         string          `json:"errorMessage"`
	DownloadID           string          `json:"downloadId"`
	Protocol             string          `json:"protocol"`
	DownloadClient       string          `json:"downloadClient"`
	// Episode is embedded on Sonarr rows when includeEpisode=true.
	Episode *QueueEpisode `json:"episode"`
}

// StatusMessage is one of a queue row's warnings, grouped under a title
// (usually the release or file name).
type StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// QueueEpisode is the subset of the embedded episode block the sync uses to
// label a queue row ("S01E03").
type QueueEpisode struct {
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
}

// Progress returns the downloaded fraction in [0, 1], or ok=false when the
// row carries no usable size (a queued magnet before metadata, a malformed
// row).
func (r QueueRecord) Progress() (float64, bool) {
	size, left := float64(r.Size), float64(r.SizeLeft)
	if size <= 0 {
		return 0, false
	}
	if left < 0 {
		left = 0
	}
	p := (size - left) / size
	switch {
	case p < 0:
		p = 0
	case p > 1:
		p = 1
	}
	return p, true
}

// ETA returns when the download is expected to finish: the absolute
// estimatedCompletionTime when present, else now + timeleft. The zero time
// means unknown.
func (r QueueRecord) ETA(now time.Time) time.Time {
	if !r.EstimatedCompletionTime.IsZero() {
		return r.EstimatedCompletionTime.Time
	}
	if d, ok := parseTimeSpan(r.TimeLeft); ok {
		return now.Add(d).UTC()
	}
	return time.Time{}
}

// Messages flattens the row's problem text: the error message first, then
// every status message (falling back to a group's title when it has no
// messages). Duplicates and blanks are dropped.
func (r QueueRecord) Messages() []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(r.ErrorMessage)
	for _, sm := range r.StatusMessages {
		if len(sm.Messages) == 0 {
			add(sm.Title)
			continue
		}
		for _, m := range sm.Messages {
			add(m)
		}
	}
	return out
}

// Queue is a fully paged queue listing.
type Queue struct {
	Records []QueueRecord
	// TotalRecords is what the instance reported, which can exceed
	// len(Records) when Truncated.
	TotalRecords int
	// Truncated is true when paging stopped at queueMaxPages.
	Truncated bool
}

// RadarrQueue fetches every page of Radarr's download queue. The embedded
// movie block is left off: rows are matched to requests by movieId, and the
// full movie resource (images, overview, ...) would multiply the payload.
func (c *Client) RadarrQueue(ctx context.Context) (*Queue, error) {
	return c.queue(ctx, url.Values{
		"includeMovie":             {"false"},
		"includeUnknownMovieItems": {"false"},
	})
}

// SonarrQueue fetches every page of Sonarr's download queue, with the episode
// block embedded so rows can be labelled.
func (c *Client) SonarrQueue(ctx context.Context) (*Queue, error) {
	return c.queue(ctx, url.Values{
		"includeSeries":             {"false"},
		"includeEpisode":            {"true"},
		"includeUnknownSeriesItems": {"false"},
	})
}

// queue walks /api/v3/queue page by page. Each record is decoded on its own
// so a row with an odd field keeps the rest of the page; a row that isn't a
// JSON object at all is skipped.
func (c *Client) queue(ctx context.Context, base url.Values) (*Queue, error) {
	out := &Queue{}
	for page := 1; page <= queueMaxPages; page++ {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("pageSize", strconv.Itoa(queuePageSize))
		var raw json.RawMessage
		if err := c.do(ctx, http.MethodGet, "/api/v3/queue", q, nil, &raw); err != nil {
			return nil, err
		}
		records, total, paged, err := decodeRecordList(raw)
		if err != nil {
			return nil, err
		}
		out.TotalRecords = total
		for _, rec := range records {
			var r QueueRecord
			if decodeObject(rec, &r) != nil {
				continue
			}
			out.Records = append(out.Records, r)
		}
		// A bare-array response is unpaged; an empty or short page is the end.
		if !paged || len(records) < queuePageSize || page*queuePageSize >= total {
			return out, nil
		}
	}
	out.Truncated = true
	return out, nil
}

// QueueCount returns how many downloads the instance is tracking, from a
// one-row page's totalRecords (cheaper than walking the queue).
func (c *Client) QueueCount(ctx context.Context) (int, error) {
	q := url.Values{"page": {"1"}, "pageSize": {"1"}}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v3/queue", q, nil, &raw); err != nil {
		return 0, err
	}
	_, total, _, err := decodeRecordList(raw)
	if err != nil {
		return 0, err
	}
	return total, nil
}
