package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus counters, histograms, and gauges for OnScreen.
// Constructed once at startup and injected where needed.
type Metrics struct {
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	DBQueryDuration      *prometheus.HistogramVec
	TranscodeActive      prometheus.Gauge
	TranscodeJobsTotal   *prometheus.CounterVec
	ScannerFilesTotal    *prometheus.CounterVec
	HubRefreshDuration   *prometheus.HistogramVec
	WatchEventsTotal     *prometheus.CounterVec
	RateLimitFailOpen    prometheus.Counter
	WebhookFailuresTotal *prometheus.CounterVec
}

// NewMetrics registers and returns all Prometheus metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	f := promauto.With(reg)

	return &Metrics{
		HTTPRequestsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "onscreen_http_requests_total",
			Help: "Total HTTP requests by method, path, and status code.",
		}, []string{"method", "path", "status"}),

		HTTPRequestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "onscreen_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"}),

		DBQueryDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "onscreen_db_query_duration_seconds",
			Help:    "Database query duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"query"}),

		TranscodeActive: f.NewGauge(prometheus.GaugeOpts{
			Name: "onscreen_transcode_sessions_active",
			Help: "Number of active transcode sessions.",
		}),

		TranscodeJobsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "onscreen_transcode_jobs_total",
			Help: "Total transcode jobs dispatched by status.",
		}, []string{"status"}),

		ScannerFilesTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "onscreen_scanner_files_scanned_total",
			Help: "Total files scanned per library.",
		}, []string{"library_id"}),

		HubRefreshDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "onscreen_hub_cache_refresh_duration_seconds",
			Help:    "Hub cache materialized view refresh duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"hub"}),

		WatchEventsTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "onscreen_watch_events_total",
			Help: "Total watch events by type.",
		}, []string{"event_type"}),

		RateLimitFailOpen: f.NewCounter(prometheus.CounterOpts{
			Name: "onscreen_ratelimit_failopen_total",
			Help: "Number of requests allowed through due to Valkey rate-limiter unavailability.",
		}),

		WebhookFailuresTotal: f.NewCounterVec(prometheus.CounterOpts{
			Name: "onscreen_webhook_failures_total",
			Help: "Webhook delivery failures after all retries, by URL.",
		}, []string{"url"}),
	}
}

// MetricsHandler returns an HTTP handler for the /metrics endpoint.
func MetricsHandler(reg prometheus.Gatherer) http.Handler {
	if reg == nil {
		reg = prometheus.DefaultGatherer
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{EnableOpenMetrics: true})
}
