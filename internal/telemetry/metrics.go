package telemetry

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics manages isolated Prometheus collectors for Aegis (DIST-02).
type Metrics struct {
	Registry                 *prometheus.Registry
	HTTPRequestsTotal        *prometheus.CounterVec
	PolicyEvalDuration       prometheus.Histogram
	PolicyDecisionsTotal     *prometheus.CounterVec
	RateLimitRejectionsTotal *prometheus.CounterVec
	SnapshotActiveVersion    prometheus.Gauge
	SnapshotLeaseAgeSeconds  prometheus.Gauge
	SpoolUtilizationRatio    prometheus.Gauge
	SpoolBytesWrittenTotal   prometheus.Counter
	ConnectedGateways        prometheus.Gauge
	SnapshotPublishTotal     prometheus.Counter
}

// NewMetrics initializes an isolated Prometheus registry and registers all metrics collectors.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_http_requests_total",
				Help: "Total HTTP requests partitioned by HTTP method, route ID, and status code.",
			},
			[]string{"method", "route_id", "status"},
		),
		PolicyEvalDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "aegis_policy_eval_duration_seconds",
				Help:    "Histogram of in-memory OPA policy evaluation latencies in seconds.",
				Buckets: []float64{0.0001, 0.00025, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1},
			},
		),
		PolicyDecisionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_policy_decisions_total",
				Help: "Total authorization decisions partitioned by decision and reason code.",
			},
			[]string{"decision", "reason_code"},
		),
		RateLimitRejectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_ratelimit_rejections_total",
				Help: "Total rate limit rejections partitioned by route ID.",
			},
			[]string{"route_id"},
		),
		SnapshotActiveVersion: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_snapshot_active_version",
				Help: "Current active monotonic configuration snapshot version.",
			},
		),
		SnapshotLeaseAgeSeconds: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_snapshot_lease_age_seconds",
				Help: "Seconds elapsed since the last verified freshness lease renewal.",
			},
		),
		SpoolUtilizationRatio: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_spool_utilization_ratio",
				Help: "Current disk WAL audit spool capacity utilization ratio (0.0 to 1.0).",
			},
		),
		SpoolBytesWrittenTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aegis_spool_bytes_written_total",
				Help: "Total bytes written to the pre-forward disk WAL audit spool.",
			},
		),
		ConnectedGateways: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_control_plane_connected_gateways",
				Help: "Active connected gateway replica streams on the control plane.",
			},
		),
		SnapshotPublishTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aegis_control_plane_snapshot_publish_total",
				Help: "Total configuration snapshots published by the control plane.",
			},
		),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.PolicyEvalDuration,
		m.PolicyDecisionsTotal,
		m.RateLimitRejectionsTotal,
		m.SnapshotActiveVersion,
		m.SnapshotLeaseAgeSeconds,
		m.SpoolUtilizationRatio,
		m.SpoolBytesWrittenTotal,
		m.ConnectedGateways,
		m.SnapshotPublishTotal,
	)

	return m
}

// RecordRequest increments the HTTP request counter with low-cardinality labels.
func (m *Metrics) RecordRequest(method, routeID string, status int) {
	if routeID == "" {
		routeID = "unknown"
	}
	m.HTTPRequestsTotal.WithLabelValues(method, routeID, strconv.Itoa(status)).Inc()
}

// RecordPolicyEval records the latency duration of an in-memory OPA policy evaluation.
func (m *Metrics) RecordPolicyEval(duration time.Duration) {
	m.PolicyEvalDuration.Observe(duration.Seconds())
}

// RecordPolicyDecision records an authorization decision (allow/deny) and reason code.
func (m *Metrics) RecordPolicyDecision(decision, reasonCode string) {
	if decision == "" {
		decision = "unknown"
	}
	if reasonCode == "" {
		reasonCode = "NONE"
	}
	m.PolicyDecisionsTotal.WithLabelValues(decision, reasonCode).Inc()
}

// RecordRateLimitRejection records a rate limit rejection for a canonical route ID.
func (m *Metrics) RecordRateLimitRejection(routeID string) {
	if routeID == "" {
		routeID = "unknown"
	}
	m.RateLimitRejectionsTotal.WithLabelValues(routeID).Inc()
}

// SetActiveSnapshotVersion updates the active configuration snapshot version gauge.
func (m *Metrics) SetActiveSnapshotVersion(v int64) {
	m.SnapshotActiveVersion.Set(float64(v))
}

// SetLeaseAge updates the seconds elapsed since the last verified freshness lease renewal.
func (m *Metrics) SetLeaseAge(seconds float64) {
	m.SnapshotLeaseAgeSeconds.Set(seconds)
}

// SetSpoolUtilization updates the disk WAL audit spool capacity ratio gauge (0.0 to 1.0).
func (m *Metrics) SetSpoolUtilization(ratio float64) {
	m.SpoolUtilizationRatio.Set(ratio)
}

// RecordSpoolBytes increments the count of bytes written to the pre-forward disk WAL audit spool.
func (m *Metrics) RecordSpoolBytes(n int) {
	if n > 0 {
		m.SpoolBytesWrittenTotal.Add(float64(n))
	}
}

// SetConnectedGateways updates the count of active connected gateway streams on the control plane.
func (m *Metrics) SetConnectedGateways(n int) {
	m.ConnectedGateways.Set(float64(n))
}

// RecordSnapshotPublish increments the count of published configuration snapshots.
func (m *Metrics) RecordSnapshotPublish() {
	m.SnapshotPublishTotal.Inc()
}
