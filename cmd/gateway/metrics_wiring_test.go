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
