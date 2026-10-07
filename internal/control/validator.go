package control

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
)

var (
	ErrInvalidRouteID      = errors.New("invalid route ID: must be non-empty and alphanumeric with dots, hyphens, or underscores")
	ErrInvalidServiceID    = errors.New("invalid service ID: must be non-empty and alphanumeric with dots, hyphens, or underscores")
	ErrInvalidMethod       = errors.New("invalid HTTP method: must be GET, POST, PUT, DELETE, PATCH, HEAD, or OPTIONS")
	ErrInvalidPathTemplate = errors.New("invalid path template: must start with / and not contain .., //, or %2f")
	ErrInvalidUpstreamURL  = errors.New("invalid upstream URL: must be valid absolute http or https URI with host")
	ErrInvalidSPIFFEID     = errors.New("invalid SPIFFE ID: must match spiffe://{trust-domain}/{path}")
	ErrInvalidRateLimit    = errors.New("invalid rate limit policy: requests_per_second must be positive and burst >= requests_per_second")
	ErrInvalidTimeout      = errors.New("invalid timeout policy: request_timeout_ms must be positive")
	ErrInvalidRegoSyntax   = errors.New("invalid Rego syntax")
	ErrRegoTestFailed      = errors.New("rego unit test failed")
)

var (
	validIDRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	validMethods = map[string]struct{}{
		"GET":     {},
		"POST":    {},
		"PUT":     {},
		"DELETE":  {},
		"PATCH":   {},
		"HEAD":    {},
		"OPTIONS": {},
	}
)

// TestSummary encapsulates the outcome of executing in-memory Rego unit tests.
type TestSummary struct {
	Total    int      `json:"total"`
	Passed   int      `json:"passed"`
	Failed   int      `json:"failed"`
	Failures []string `json:"failures,omitempty"`
}

// Validator provides validation functions for routes and policy definitions.
type Validator struct{}

// NewValidator constructs a new Validator.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidateRoute validates that a RouteDefinition conforms to OpenAPI specifications and security constraints.
func (v *Validator) ValidateRoute(route *snapshotv1.RouteDefinition) error {
	if route == nil {
		return errors.New("route definition cannot be nil")
	}

	if route.RouteId == "" || !validIDRegex.MatchString(route.RouteId) {
		return ErrInvalidRouteID
	}

	if route.ServiceId == "" || !validIDRegex.MatchString(route.ServiceId) {
		return ErrInvalidServiceID
	}

	if _, ok := validMethods[route.HttpMethod]; !ok {
		return ErrInvalidMethod
	}

	if route.PathTemplate == "" || !strings.HasPrefix(route.PathTemplate, "/") {
		return ErrInvalidPathTemplate
	}
	if strings.Contains(route.PathTemplate, "..") ||
		strings.Contains(route.PathTemplate, "//") ||
		strings.Contains(strings.ToLower(route.PathTemplate), "%2f") {
		return ErrInvalidPathTemplate
	}

	if route.UpstreamUrl == "" {
		return ErrInvalidUpstreamURL
	}
	u, err := url.Parse(route.UpstreamUrl)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrInvalidUpstreamURL
	}

	if route.UpstreamSpiffeId != "" {
		spiffeURI, err := url.Parse(route.UpstreamSpiffeId)
		if err != nil || spiffeURI.Scheme != "spiffe" || spiffeURI.Host == "" || spiffeURI.Path == "" || spiffeURI.Path == "/" {
			return ErrInvalidSPIFFEID
		}
	}

	if route.RateLimit != nil {
		if route.RateLimit.RequestsPerSecond <= 0 || route.RateLimit.Burst < route.RateLimit.RequestsPerSecond {
			return ErrInvalidRateLimit
		}
	}

	if route.Timeout != nil {
		if route.Timeout.RequestTimeoutMs <= 0 || route.Timeout.UpstreamTimeoutMs < 0 {
			return ErrInvalidTimeout
		}
	}

	return nil
}

// ValidatePolicyDraft verifies that a raw Rego policy string compiles syntactically without errors.
func (v *Validator) ValidatePolicyDraft(sourceRego string) error {
	if strings.TrimSpace(sourceRego) == "" {
		return fmt.Errorf("%w: policy cannot be empty", ErrInvalidRegoSyntax)
	}

	ctx := context.Background()
	_, err := rego.New(
		rego.SetRegoVersion(ast.RegoV1),
		rego.Module("draft.rego", sourceRego),
		rego.Query("data"),
	).PrepareForEval(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRegoSyntax, err)
	}

	return nil
}

// RunRegoTests compiles both the policy and unit test modules and evaluates all rules starting with 'test_'.
func (v *Validator) RunRegoTests(ctx context.Context, policyRego string, testRego string) (*TestSummary, error) {
	if strings.TrimSpace(policyRego) == "" {
		return nil, fmt.Errorf("%w: policy module cannot be empty", ErrInvalidRegoSyntax)
	}
	if strings.TrimSpace(testRego) == "" {
		return nil, fmt.Errorf("%w: test module cannot be empty", ErrInvalidRegoSyntax)
	}

	modules := map[string]string{
		"policy.rego": policyRego,
	}
	if testRego != "" && testRego != policyRego {
		modules["test.rego"] = testRego
	}

	compiler, err := ast.CompileModulesWithOpt(modules, ast.CompileOpts{
		ParserOptions: ast.ParserOptions{
			RegoVersion: ast.RegoV1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: compilation failure: %v", ErrInvalidRegoSyntax, err)
	}

	summary := &TestSummary{
		Failures: make([]string, 0),
	}

	type testTarget struct {
		name  string
		query string
	}
	var targets []testTarget

	for _, mod := range compiler.Modules {
		pkgPath := mod.Package.Path.String()
		for _, rule := range mod.Rules {
			ruleName := string(rule.Head.Name)
			if strings.HasPrefix(ruleName, "test_") {
				targets = append(targets, testTarget{
					name:  ruleName,
					query: fmt.Sprintf("%s.%s", pkgPath, ruleName),
				})
			}
		}
	}

	summary.Total = len(targets)

	for _, target := range targets {
		q, err := rego.New(
			rego.SetRegoVersion(ast.RegoV1),
			rego.Compiler(compiler),
			rego.Query(target.query),
		).PrepareForEval(ctx)
		if err != nil {
			summary.Failed++
			summary.Failures = append(summary.Failures, fmt.Sprintf("%s: prepare failed: %v", target.name, err))
			continue
		}

		rs, err := q.Eval(ctx)
		if err != nil {
			summary.Failed++
			summary.Failures = append(summary.Failures, fmt.Sprintf("%s: evaluation error: %v", target.name, err))
			continue
		}

		passed := false
		if len(rs) > 0 && len(rs[0].Expressions) > 0 {
			val := rs[0].Expressions[0].Value
			if b, ok := val.(bool); ok {
				passed = b
			} else if val != nil {
				passed = true
			}
		}

		if passed {
			summary.Passed++
		} else {
			summary.Failed++
			summary.Failures = append(summary.Failures, fmt.Sprintf("%s failed assertion", target.name))
		}
	}

	if summary.Failed > 0 {
		return summary, fmt.Errorf("%w: %s", ErrRegoTestFailed, strings.Join(summary.Failures, "; "))
	}

	return summary, nil
}
