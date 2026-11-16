package main

import "aegis/internal/audit"

// allowReasonLabel is the reason the audit log records for an allow decision with no
// policy-supplied reason (audit logger default).
const allowReasonLabel = "ALLOWED"

// policyReasonSet is the closed set of reason labels permitted on the
// policy-decision metric. It is built once from the audit reason table (deny
// reasons) plus the audit allow reason, so the metric cardinality is bounded
// regardless of what a policy author returns.
var policyReasonSet = func() map[string]struct{} {
	labels := audit.ClosedReasonLabels()
	set := make(map[string]struct{}, len(labels)+1)
	for _, l := range labels {
		set[l] = struct{}{}
	}
	set[allowReasonLabel] = struct{}{}
	return set
}()

// policyReasonLabel normalizes a policy reason code to the closed audit set,
// mapping anything unknown or empty to "OTHER" (T-12-17).
func policyReasonLabel(reason string) string {
	if _, ok := policyReasonSet[reason]; ok {
		return reason
	}
	return "OTHER"
}

// decisionReasonOrAllowed mirrors the audit allow-reason default so the metric
// label for allow decisions matches the recorded decision reason. A policy-supplied
// allow reason outside the closed set still maps to "OTHER" via policyReasonLabel.
func decisionReasonOrAllowed(reason string) string {
	if reason == "" {
		return allowReasonLabel
	}
	return reason
}
