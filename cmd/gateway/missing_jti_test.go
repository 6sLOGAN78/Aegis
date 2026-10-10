package main

import (
	"go/ast"
	"testing"

	"aegis/internal/identity"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestMissingJTIHelper(t *testing.T) {
	require.True(t, missingJTI(nil))
	require.True(t, missingJTI(&identity.UserClaims{}))
	require.False(t, missingJTI(&identity.UserClaims{RegisteredClaims: jwt.RegisteredClaims{ID: "abc"}}))
}

func TestMissingJTIGuardOrdering(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	var denyPos, revPos int
	for _, c := range wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "ac", "SetDecision") }) {
		if len(c.call.Args) == 2 {
			if l, ok := c.call.Args[1].(*ast.BasicLit); ok && l.Value == `"TOKEN_MISSING_JTI"` {
				denyPos = int(c.pos)
			}
		}
	}
	for _, c := range wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "revStore", "CheckRevocation") }) {
		revPos = int(c.pos)
	}
	require.NotZero(t, denyPos, "D-09: SetDecision deny TOKEN_MISSING_JTI must exist")
	require.NotZero(t, revPos)
	require.Less(t, denyPos, revPos, "D-09: jti guard must precede CheckRevocation")
}

func TestMissingJTISetJTIWired(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	require.NotEmpty(t, wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "ac", "SetJTI") }))
}
