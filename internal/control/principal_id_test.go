package control

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCanonicalPrincipalID_SchemeCaseAndWhitespace guards CR-01: a SPIFFE id
// must be classified regardless of scheme case and canonicalised to the key
// the gateway checks; padded or URI-shaped non-spiffe ids are rejected.
func TestCanonicalPrincipalID_SchemeCaseAndWhitespace(t *testing.T) {
	const want = "spiffe://aegis.local/workload/orders"
	for _, in := range []string{
		"spiffe://aegis.local/workload/orders",
		"SPIFFE://aegis.local/workload/orders",
		"Spiffe://AEGIS.LOCAL/workload/orders",
		"SPIFFE%3A%2F%2Faegis.local%2Fworkload%2Forders",
	} {
		got, err := canonicalPrincipalID(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{
		" spiffe://aegis.local/workload/orders",
		"spiffe://aegis.local/workload/orders ",
		"%20spiffe%3A%2F%2Faegis.local%2Fworkload%2Forders",
		" spiffe://aegis.local/workload/orders",
		" developer",
		"developer ",
		"SPIFFE://evil.local/workload/orders",
		"SPIFFE:aegis.local/workload/orders",
		"https://aegis.local/workload/orders",
		"spiffe:/aegis.local/workload/orders",
	} {
		_, err := canonicalPrincipalID(in)
		assert.Error(t, err, "%q", in)
	}

	// Plain user ids still pass through unchanged.
	got, err := canonicalPrincipalID("developer")
	require.NoError(t, err)
	assert.Equal(t, "developer", got)
}
