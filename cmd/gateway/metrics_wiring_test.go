package main

import "testing"

func TestPolicyReasonLabel(t *testing.T) {
	cases := map[string]string{
		"POLICY_LEASE_EXPIRED":      "POLICY_LEASE_EXPIRED",
		"OTHER":                     "OTHER",
		"":                          "OTHER",
		"some-policy-author-string": "OTHER",
		"user-" + "abc123":          "OTHER",
	}
	for in, want := range cases {
		if got := policyReasonLabel(in); got != want {
			t.Errorf("policyReasonLabel(%q)=%q want %q", in, got, want)
		}
	}
}

// WR-02: an allow decision without a policy reason is labelled ALLOWED, not OTHER.
func TestPolicyReasonLabelAllow(t *testing.T) {
	if got := policyReasonLabel(decisionReasonOrAllowed("")); got != "ALLOWED" {
		t.Errorf("allow label = %q, want ALLOWED", got)
	}
	if got := policyReasonLabel(decisionReasonOrAllowed("ALLOWED")); got != "ALLOWED" {
		t.Errorf("explicit ALLOWED label = %q, want ALLOWED", got)
	}
	if got := policyReasonLabel(decisionReasonOrAllowed("custom-allow-reason")); got != "OTHER" {
		t.Errorf("unknown allow reason label = %q, want OTHER", got)
	}
}
