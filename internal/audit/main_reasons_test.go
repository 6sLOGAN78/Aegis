package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const govClassifyHint = "add it to the governor classification table in internal/audit/governor.go"

func govStringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func govParse(t *testing.T, parts ...string) *ast.File {
	t.Helper()
	path := filepath.Join(parts...)
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err, "parse %s", path)
	return f
}

func TestEveryMainReasonIsClassified(t *testing.T) {
	f := govParse(t, "..", "..", "cmd", "gateway", "main.go")

	found := 0
	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetDecision" || len(call.Args) < 2 {
			return true
		}
		if d, ok := govStringLit(call.Args[0]); !ok || d != "deny" {
			return true
		}
		reason, ok := govStringLit(call.Args[1])
		if !ok {
			t.Logf("non-literal deny reason at %v (revocation store / policy engine value, identified class by default)", call.Args[1])
			return true
		}
		found++
		seen[reason] = true
		_, classified := reasonClassOf(reason)
		assert.True(t, classified, "deny reason %q in cmd/gateway/main.go is not classified: %s", reason, govClassifyHint)
		return true
	})
	require.GreaterOrEqual(t, found, 20, "expected at least 20 literal deny reasons in main.go; parser or path failure?")

	for _, r := range []string{"POLICY_LEASE_EXPIRED", "UNINITIALIZED", "DEPENDENCY_OUTAGE_REDIS", "AUDIT_SPOOL_WRITE_ERROR"} {
		c, ok := reasonClassOf(r)
		require.True(t, ok, r)
		assert.Equal(t, classOutage, c, r)
	}
	for _, r := range []string{"BAD_REQUEST_INVALID_PATH", "UNAUTHORIZED", "UNAUTHORIZED_NO_CERT", "UNAUTHORIZED_INVALID_SPIFFE"} {
		c, ok := reasonClassOf(r)
		require.True(t, ok, r)
		assert.Equal(t, classUnauth, c, r)
	}
	for _, r := range []string{"PRINCIPAL_QUARANTINED", "TOKEN_REVOKED", "DENIED_DEFAULT", "DENIED_WORKLOAD_FORBIDDEN"} {
		c, ok := reasonClassOf(r)
		require.True(t, ok, r)
		assert.Equal(t, classIdentified, c, r)
	}

	// The default values main.go assigns to its variable reason must be classified too.
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != "reason" {
			return true
		}
		if v, ok := govStringLit(as.Rhs[0]); ok {
			_, classified := reasonClassOf(v)
			assert.True(t, classified, "default reason %q in cmd/gateway/main.go is not classified: %s", v, govClassifyHint)
		}
		return true
	})
}

func TestEveryPolicyReasonIsClassified(t *testing.T) {
	f := govParse(t, "..", "..", "internal", "policy", "engine.go")

	engine := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ReasonCode" {
			return true
		}
		if v, ok := govStringLit(kv.Value); ok {
			engine[v] = true
		}
		return true
	})
	require.GreaterOrEqual(t, len(engine), 3, "expected EVALUATION_ERROR, DENIED_EMPTY_RESULT, MALFORMED_DECISION in engine.go")
	names := make([]string, 0, len(engine))
	for v := range engine {
		names = append(names, v)
	}
	sort.Strings(names)
	for _, v := range names {
		_, classified := reasonClassOf(v)
		assert.True(t, classified, "policy engine reason %q in internal/policy/engine.go is not classified: %s", v, govClassifyHint)
	}

	rego, err := os.ReadFile(filepath.Join("..", "..", "policies", "rego", "authz.rego"))
	require.NoError(t, err)
	codes := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(DENIED_[A-Z_]+)"`).FindAllSubmatch(rego, -1) {
		codes[string(m[1])] = true
	}
	for _, want := range []string{"DENIED_DEFAULT", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "DENIED_WORKLOAD_ADMIN_FORBIDDEN", "DENIED_INVALID_PRINCIPAL"} {
		assert.True(t, codes[want], "expected %s in authz.rego", want)
	}
	for c := range codes {
		_, classified := reasonClassOf(c)
		assert.True(t, classified, "rego reason %q in policies/rego/authz.rego is not classified: %s", c, govClassifyHint)
	}

	_, found := reasonClassOf("POLICY_EVALUATION_ERROR")
	assert.False(t, found, "POLICY_EVALUATION_ERROR is an error code, not a reason")
	assert.Equal(t, "OTHER", ReasonLabel("POLICY_EVALUATION_ERROR"))
}
