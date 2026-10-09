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

	// Audit pipeline metrics (phase 08). Labels are closed enums only.
	AuditRecordsWrittenTotal    *prometheus.CounterVec
	AuditRecordsDroppedTotal    *prometheus.CounterVec
	AuditRecordsSuppressedTotal *prometheus.CounterVec
	AuditSuppressorOverflow     prometheus.Counter
	AuditCompletionQueueDepth   prometheus.Gauge
	AuditDegraded               prometheus.Gauge
	AuditFlushDuration          prometheus.Histogram
	HTTPRejectedTotal           *prometheus.CounterVec
}

// Closed label sets for the audit and rejection series. Pre-initializing them
// to zero makes absence of a series mean "not scraped" rather than "zero".
var (
	auditKinds         = []string{"completion", "denial"}
	auditDropReasons   = []string{"queue_full", "hard_limit", "write_error", "closed", "timeout", "unauth_cap"}
	rejectionReasons   = []string{"concurrency", "header_too_large", "ambiguous_credentials"}
	otherRejectionName = "other"
)

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
		AuditRecordsWrittenTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_audit_records_written_total",
				Help: "Audit records durably written to the spool, partitioned by kind (completion, denial).",
			},
			[]string{"kind"},
		),
		AuditRecordsDroppedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_audit_records_dropped_total",
				Help: "Audit records dropped before durable write, partitioned by kind and closed drop reason.",
			},
			[]string{"kind", "reason"},
		),
		AuditRecordsSuppressedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_audit_records_suppressed_total",
				Help: "Repeated denial audit rows suppressed by the governor, partitioned by reason code.",
			},
			[]string{"reason_code"},
		),
		AuditSuppressorOverflow: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "aegis_audit_suppressor_overflow_total",
				Help: "Times the denial suppressor key table was full and a key was not tracked.",
			},
		),
		AuditCompletionQueueDepth: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_audit_completion_queue_depth",
				Help: "Current depth of the bounded completion-event queue.",
			},
		),
		AuditDegraded: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "aegis_audit_degraded",
				Help: "1 while the audit spool write-fault flag is set, otherwise 0.",
			},
		),
		AuditFlushDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "aegis_audit_flush_duration_seconds",
				Help:    "Histogram of audit spool group-commit flush durations in seconds.",
				Buckets: []float64{0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1},
			},
		),
		HTTPRejectedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "aegis_http_rejected_total",
				Help: "Requests rejected before the audit middleware (no audit row), partitioned by closed reason.",
			},
			[]string{"reason"},
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
		m.AuditRecordsWrittenTotal,
		m.AuditRecordsDroppedTotal,
		m.AuditRecordsSuppressedTotal,
		m.AuditSuppressorOverflow,
		m.AuditCompletionQueueDepth,
		m.AuditDegraded,
		m.AuditFlushDuration,
		m.HTTPRejectedTotal,
	)

	for _, kind := range auditKinds {
		m.AuditRecordsWrittenTotal.WithLabelValues(kind).Add(0)
		for _, reason := range auditDropReasons {
			m.AuditRecordsDroppedTotal.WithLabelValues(kind, reason).Add(0)
		}
	}
	for _, reason := range rejectionReasons {
		m.HTTPRejectedTotal.WithLabelValues(reason).Add(0)
	}
	m.HTTPRejectedTotal.WithLabelValues(otherRejectionName).Add(0)
	m.AuditRecordsSuppressedTotal.WithLabelValues("OTHER").Add(0)

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

// RecordAuditWritten counts n audit records of the given kind durably written.
func (m *Metrics) RecordAuditWritten(kind string, n int) {
	if n > 0 {
		m.AuditRecordsWrittenTotal.WithLabelValues(kind).Add(float64(n))
	}
}

// RecordAuditDropped counts n audit records of the given kind dropped for a closed reason.
func (m *Metrics) RecordAuditDropped(kind, reason string, n int) {
	if n > 0 {
		m.AuditRecordsDroppedTotal.WithLabelValues(kind, reason).Add(float64(n))
	}
}

// RecordAuditSuppressed counts n denial rows suppressed for the reason code.
// Cardinality is bounded upstream by the governor's closed reason-label set.
func (m *Metrics) RecordAuditSuppressed(reasonCode string, n int) {
	if n <= 0 {
		return
	}
	if reasonCode == "" {
		reasonCode = "OTHER"
	}
	m.AuditRecordsSuppressedTotal.WithLabelValues(reasonCode).Add(float64(n))
}

// RecordAuditSuppressorOverflow counts a suppressor key-table overflow.
func (m *Metrics) RecordAuditSuppressorOverflow() {
	m.AuditSuppressorOverflow.Inc()
}

// ObserveAuditFlush records the duration of one spool group-commit flush.
func (m *Metrics) ObserveAuditFlush(d time.Duration) {
	m.AuditFlushDuration.Observe(d.Seconds())
}

// SetAuditDegraded sets the write-fault gauge to 1 (degraded) or 0.
func (m *Metrics) SetAuditDegraded(degraded bool) {
	if degraded {
		m.AuditDegraded.Set(1)
		return
	}
	m.AuditDegraded.Set(0)
}

// SetAuditQueueDepth sets the current completion queue depth.
func (m *Metrics) SetAuditQueueDepth(n int) {
	m.AuditCompletionQueueDepth.Set(float64(n))
}

// InitAuditSuppressedLabels pre-initializes suppressed series to zero for the
// supplied reason codes (and always OTHER).
func (m *Metrics) InitAuditSuppressedLabels(reasonCodes []string) {
	for _, code := range reasonCodes {
		if code != "" {
			m.AuditRecordsSuppressedTotal.WithLabelValues(code).Add(0)
		}
	}
	m.AuditRecordsSuppressedTotal.WithLabelValues("OTHER").Add(0)
}

// RecordRejection counts a request rejected before the audit middleware.
// Anything outside the closed reason set is recorded as "other".
func (m *Metrics) RecordRejection(reason string) {
	switch reason {
	case "concurrency", "header_too_large", "ambiguous_credentials":
	default:
		reason = otherRejectionName
	}
	m.HTTPRejectedTotal.WithLabelValues(reason).Inc()
}
