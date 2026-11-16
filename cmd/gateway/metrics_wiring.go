package main

import "aegis/internal/audit"

// policyReasonSet is the closed set of reason labels permitted on the
// policy-decision metric. It is built once from the audit reason table so the
// metric cardinality is bounded regardless of what a policy author returns.
var policyReasonSet = func() map[string]struct{} {
	labels := audit.ClosedReasonLabels()
	set := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		set[l] = struct{}{}
	}
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
// label for allow decisions matches the recorded decision reason.
func decisionReasonOrAllowed(reason string) string {
	if reason == "" {
		return "ALLOWED"
	}
	return reason
}
