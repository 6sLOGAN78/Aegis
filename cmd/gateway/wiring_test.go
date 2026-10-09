package main

// Source-level guard for the audit wiring in main(). main() is an inline
// closure that no test can execute (extracting it is out of scope for phase
// 08), so these tests parse main.go and assert the wiring invariants that
// close blocker B7 (plan 08-08).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wiringCall is one call expression found in main.go with its position.
type wiringCall struct {
	call *ast.CallExpr
	pos  token.Pos
}

func wiringParseMain(t *testing.T) (*ast.File, *ast.FuncDecl) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	require.NoError(t, err, "parse main.go")
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil {
			return f, fd
		}
	}
	require.FailNow(t, "func main not found in main.go")
	return nil, nil
}

// wiringSelector reports whether fn is the selector expression pkgOrVar.name.
func wiringSelector(fn ast.Expr, x, name string) bool {
	sel, ok := fn.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == x
}

// wiringMethod reports whether fn is any selector whose method name matches.
func wiringMethod(fn ast.Expr, name string) bool {
	sel, ok := fn.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

func wiringCalls(root ast.Node, match func(fn ast.Expr) bool) []wiringCall {
	var out []wiringCall
	ast.Inspect(root, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && match(c.Fun) {
			out = append(out, wiringCall{call: c, pos: c.Pos()})
		}
		return true
	})
	return out
}

func TestWiringAuditMiddlewareHasSink(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	calls := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "audit", "AuditMiddleware") })
	require.Equal(t, 2, len(calls),
		"08-08 B7: exactly two audit.AuditMiddleware call sites expected (user and workload listeners)")
	for i, c := range calls {
		if !assert.Len(t, c.call.Args, 3,
			"08-08 B7: audit.AuditMiddleware call #%d must pass audit.WithSink as its third argument", i+1) {
			continue
		}
		third, ok := c.call.Args[2].(*ast.CallExpr)
		assert.True(t, ok && wiringSelector(third.Fun, "audit", "WithSink"),
			"08-08 B7: third argument of audit.AuditMiddleware call #%d must be audit.WithSink(...); a listener without a sink drops denials and completions", i+1)
	}
}

func TestWiringUpstreamErrorHandlerWrapped(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	wrapped := 0
	ast.Inspect(mainFn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		lhs, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || lhs.Sel.Name != "ErrorHandler" {
			return true
		}
		rhs, ok := as.Rhs[0].(*ast.CallExpr)
		if ok && wiringSelector(rhs.Fun, "audit", "WrapUpstreamErrorHandler") {
			wrapped++
		}
		return true
	})
	assert.Equal(t, 2, wrapped,
		"08-08 AUD-04: both reverse proxies (user and workload) must assign rp.ErrorHandler = audit.WrapUpstreamErrorHandler(...)")

	all := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "audit", "WrapUpstreamErrorHandler") })
	assert.Equal(t, len(all), wrapped,
		"08-08 AUD-04: audit.WrapUpstreamErrorHandler must only be used as the right-hand side of an ErrorHandler assignment")
}

func TestWiringAppendPreForwardUntouched(t *testing.T) {
	f, _ := wiringParseMain(t)
	calls := wiringCalls(f, func(fn ast.Expr) bool { return wiringMethod(fn, "AppendPreForward") })
	assert.Equal(t, 2, len(calls),
		"08-08 D-09: exactly two AppendPreForward call sites (user and workload allow path) must remain; a third or a removal changes the allow-path guarantee")
}

func TestWiringShutdownOrder(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	first := func(x, name string) token.Pos {
		calls := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, x, name) })
		require.Equal(t, 1, len(calls), "08-08 D-10: expected exactly one %s.%s call in main()", x, name)
		return calls[0].pos
	}
	drain := first("dualServer", "Shutdown")
	pipeline := first("auditPipeline", "Shutdown")
	closeSpool := first("diskSpool", "Close")

	assert.Less(t, int(drain), int(pipeline),
		"D-10: audit pipeline must shut down after the HTTP drain (dualServer.Shutdown) so in-flight requests still reach it")
	assert.Less(t, int(pipeline), int(closeSpool),
		"D-10: audit pipeline must shut down before the spool closes (diskSpool.Close) so queued records are fsynced")
}

func TestWiringPipelineAfterMetrics(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	newMetrics := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "telemetry", "NewMetrics") })
	newPipeline := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "audit", "NewPipeline") })
	middleware := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "audit", "AuditMiddleware") })

	require.Equal(t, 1, len(newMetrics), "08-08: expected one telemetry.NewMetrics call in main()")
	require.Equal(t, 1, len(newPipeline), "08-08: expected one audit.NewPipeline call in main()")
	require.NotEmpty(t, middleware, "08-08: expected audit.AuditMiddleware calls in main()")

	assert.Less(t, int(newMetrics[0].pos), int(newPipeline[0].pos),
		"08-08: the pipeline records into the metrics registry, so it must be built after telemetry.NewMetrics")
	assert.Less(t, int(newPipeline[0].pos), int(middleware[0].pos),
		"08-08: the pipeline must exist before the first audit.AuditMiddleware call that references it")
}

func TestWiringRejectionRecorder(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	calls := wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "dualServer", "SetRejectionRecorder") })
	require.Equal(t, 1, len(calls),
		"08-08 D-16: dualServer.SetRejectionRecorder must be called exactly once so pre-middleware rejections are counted")
	require.Len(t, calls[0].call.Args, 1, "08-08 D-16: SetRejectionRecorder takes one argument")
	id, ok := calls[0].call.Args[0].(*ast.Ident)
	assert.True(t, ok && id.Name == "metrics",
		"08-08 D-16: SetRejectionRecorder must be given the telemetry registry (identifier metrics)")
}
