package control

import (
	"context"
	"testing"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validRoute() *snapshotv1.RouteDefinition {
	return &snapshotv1.RouteDefinition{
		RouteId:          "orders.list",
		ServiceId:        "orders",
		HttpMethod:       "GET",
		PathTemplate:     "/api/orders",
		UpstreamUrl:      "https://orders:8081",
		UpstreamSpiffeId: "spiffe://aegis.local/service/orders",
		RateLimit: &snapshotv1.RateLimitPolicy{
			RequestsPerSecond: 100,
			Burst:             200,
		},
		Timeout: &snapshotv1.TimeoutPolicy{
			RequestTimeoutMs:  15000,
			UpstreamTimeoutMs: 5000,
		},
		RequiresWorkloadMtls: true,
	}
}

func TestValidator_ValidateRoute_Valid(t *testing.T) {
	v := NewValidator()
	route := validRoute()
	err := v.ValidateRoute(route)
	assert.NoError(t, err)
}

func TestValidator_ValidateRoute_InvalidRouteID(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		name    string
		routeID string
	}{
		{"empty", ""},
		{"spaces", "orders list"},
		{"special_chars", "orders$list!"},
		{"slash", "orders/list"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validRoute()
			r.RouteId = tc.routeID
			err := v.ValidateRoute(r)
			assert.ErrorIs(t, err, ErrInvalidRouteID)
		})
	}
}

func TestValidator_ValidateRoute_InvalidServiceID(t *testing.T) {
	v := NewValidator()
	r := validRoute()
	r.ServiceId = ""
	err := v.ValidateRoute(r)
	assert.ErrorIs(t, err, ErrInvalidServiceID)

	r.ServiceId = "orders service!"
	err = v.ValidateRoute(r)
	assert.ErrorIs(t, err, ErrInvalidServiceID)
}

func TestValidator_ValidateRoute_InvalidMethod(t *testing.T) {
	v := NewValidator()

	methods := []string{"", "BOGUS", "CONNECT", "TRACE", "get", "post"}
	for _, m := range methods {
		t.Run("method_"+m, func(t *testing.T) {
			r := validRoute()
			r.HttpMethod = m
			err := v.ValidateRoute(r)
			assert.ErrorIs(t, err, ErrInvalidMethod)
		})
	}
}

func TestValidator_ValidateRoute_InvalidPathTemplate(t *testing.T) {
	v := NewValidator()

	invalidPaths := []string{
		"",
		"orders",
		"api/orders",
		"../admin",
		"/api/../admin",
		"//orders",
		"/api//orders",
		"%2fadmin",
		"/api/%2Fadmin",
		"/api/%2fadmin",
	}

	for _, p := range invalidPaths {
		t.Run("path_"+p, func(t *testing.T) {
			r := validRoute()
			r.PathTemplate = p
			err := v.ValidateRoute(r)
			assert.ErrorIs(t, err, ErrInvalidPathTemplate)
		})
	}
}

func TestValidator_ValidateRoute_InvalidUpstreamURL(t *testing.T) {
	v := NewValidator()

	invalidURLs := []string{
		"",
		"orders:8081",
		"ftp://orders:8081",
		"://orders",
	}

	for _, u := range invalidURLs {
		t.Run("url_"+u, func(t *testing.T) {
			r := validRoute()
			r.UpstreamUrl = u
			err := v.ValidateRoute(r)
			assert.ErrorIs(t, err, ErrInvalidUpstreamURL)
		})
	}
}

func TestValidator_ValidateRoute_InvalidSPIFFEID(t *testing.T) {
	v := NewValidator()

	invalidSPIFFEs := []string{
		"http://aegis.local/service/orders",
		"spiffe://",
		"spiffe://aegis.local",
		"spiffe://aegis.local/",
	}

	for _, s := range invalidSPIFFEs {
		t.Run("spiffe_"+s, func(t *testing.T) {
			r := validRoute()
			r.UpstreamSpiffeId = s
			err := v.ValidateRoute(r)
			assert.ErrorIs(t, err, ErrInvalidSPIFFEID)
		})
	}
}

func TestValidator_ValidateRoute_InvalidRateLimit(t *testing.T) {
	v := NewValidator()

	r := validRoute()
	r.RateLimit = &snapshotv1.RateLimitPolicy{
		RequestsPerSecond: 0,
		Burst:             100,
	}
	err := v.ValidateRoute(r)
	assert.ErrorIs(t, err, ErrInvalidRateLimit)

	r.RateLimit = &snapshotv1.RateLimitPolicy{
		RequestsPerSecond: 100,
		Burst:             50,
	}
	err = v.ValidateRoute(r)
	assert.ErrorIs(t, err, ErrInvalidRateLimit)
}

func TestValidator_ValidateRoute_InvalidTimeout(t *testing.T) {
	v := NewValidator()

	r := validRoute()
	r.Timeout = &snapshotv1.TimeoutPolicy{
		RequestTimeoutMs: 0,
	}
	err := v.ValidateRoute(r)
	assert.ErrorIs(t, err, ErrInvalidTimeout)
}

func TestValidator_ValidatePolicyDraft_Valid(t *testing.T) {
	v := NewValidator()

	validRego := `
package aegis.authz

default allow := false

allow if {
    input.role == "admin"
}
`
	err := v.ValidatePolicyDraft(validRego)
	assert.NoError(t, err)
}

func TestValidator_ValidatePolicyDraft_Invalid(t *testing.T) {
	v := NewValidator()

	invalidRegos := []string{
		"",
		"package",
		"package aegis.authz\nallow := { syntax error here",
	}

	for i, regoCode := range invalidRegos {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			err := v.ValidatePolicyDraft(regoCode)
			assert.ErrorIs(t, err, ErrInvalidRegoSyntax)
		})
	}
}

func TestValidator_RunRegoTests_Success(t *testing.T) {
	v := NewValidator()
	ctx := context.Background()

	policyRego := `
package aegis.authz

default allow := false

allow if {
    input.role == "admin"
}
`

	testRego := `
package aegis.authz_test

import data.aegis.authz

test_admin_allowed if {
    authz.allow with input as {"role": "admin"}
}

test_user_denied if {
    not authz.allow with input as {"role": "user"}
}
`

	summary, err := v.RunRegoTests(ctx, policyRego, testRego)
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, 2, summary.Total)
	assert.Equal(t, 2, summary.Passed)
	assert.Equal(t, 0, summary.Failed)
	assert.Empty(t, summary.Failures)
}

func TestValidator_RunRegoTests_Failure(t *testing.T) {
	v := NewValidator()
	ctx := context.Background()

	policyRego := `
package aegis.authz

default allow := false

allow if {
    input.role == "admin"
}
`

	testRego := `
package aegis.authz_test

import data.aegis.authz

test_failing_case if {
    authz.allow with input as {"role": "guest"}
}
`

	summary, err := v.RunRegoTests(ctx, policyRego, testRego)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRegoTestFailed)
	require.NotNil(t, summary)
	assert.Equal(t, 1, summary.Total)
	assert.Equal(t, 0, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
	assert.Len(t, summary.Failures, 1)
	assert.Contains(t, summary.Failures[0], "test_failing_case")
}
