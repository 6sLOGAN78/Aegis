package policy

import (
	"context"
	"fmt"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
)

// Engine wraps an in-memory precompiled OPA query evaluator.
type Engine struct {
	preparedQuery rego.PreparedEvalQuery
}

// NewEngine precompiles the Rego authorization module into a prepared query.
func NewEngine(ctx context.Context, regoCode string) (*Engine, error) {
	query, err := rego.New(
		rego.SetRegoVersion(ast.RegoV1),
		rego.Query("data.aegis.authz.decision"),
		rego.Module("authz.rego", regoCode),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to precompile rego query: %w", err)
	}

	return &Engine{preparedQuery: query}, nil
}

// Evaluate runs the precompiled query against typed policy input in memory.
func (e *Engine) Evaluate(ctx context.Context, input PolicyInput) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{
			Allow:           false,
			ReasonCode:      "EVALUATION_ERROR",
			SnapshotVersion: input.SnapshotVersion,
		}, err
	}
	rs, err := e.preparedQuery.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return Decision{
			Allow:           false,
			ReasonCode:      "EVALUATION_ERROR",
			SnapshotVersion: input.SnapshotVersion,
		}, err
	}

	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{
			Allow:           false,
			ReasonCode:      "DENIED_EMPTY_RESULT",
			SnapshotVersion: input.SnapshotVersion,
		}, nil
	}

	val, ok := rs[0].Expressions[0].Value.(map[string]interface{})
	if !ok {
		return Decision{
			Allow:           false,
			ReasonCode:      "MALFORMED_DECISION",
			SnapshotVersion: input.SnapshotVersion,
		}, nil
	}

	allow, _ := val["allow"].(bool)
	reasonCode, _ := val["reason_code"].(string)

	return Decision{
		Allow:           allow,
		ReasonCode:      reasonCode,
		SnapshotVersion: input.SnapshotVersion,
	}, nil
}
