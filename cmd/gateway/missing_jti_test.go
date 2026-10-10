package main

import (
	"context"
	"crypto/ed25519"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegis/internal/audit"
	"aegis/internal/identity"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestMissingJTIHelper(t *testing.T) {
	require.True(t, missingJTI(nil))
	require.True(t, missingJTI(&identity.UserClaims{}))
	require.False(t, missingJTI(&identity.UserClaims{RegisteredClaims: jwt.RegisteredClaims{ID: "abc"}}))
}

// isClaimsID reports whether e is the selector expression claims.ID.
func isClaimsID(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ID" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "claims"
}

// TestMissingJTIGuardOrdering pins D-09 against the user-listener revocation
// check specifically (the CheckRevocation call whose jti argument is
// claims.ID), not whichever CheckRevocation appears last in main (the
// workload listener's). The guard must be `if rejectMissingJTI(...) { return }`
// inside the same handler and before that call.
func TestMissingJTIGuardOrdering(t *testing.T) {
	_, mainFn := wiringParseMain(t)

	var userRev []wiringCall
	for _, c := range wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "revStore", "CheckRevocation") }) {
		if len(c.call.Args) == 3 && isClaimsID(c.call.Args[2]) {
			userRev = append(userRev, c)
		}
	}
	require.Len(t, userRev, 1, "D-09: exactly one user-listener CheckRevocation(ctx, claims.Subject, claims.ID) expected")
	revPos := userRev[0].pos

	// Find the enclosing handler func literal of the user CheckRevocation and
	// the guard `if rejectMissingJTI(...) { return }` in that same literal.
	var handler *ast.FuncLit
	ast.Inspect(mainFn, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok && fl.Pos() <= revPos && revPos < fl.End() {
			handler = fl // innermost wins: Inspect descends in order
		}
		return true
	})
	require.NotNil(t, handler, "user-listener CheckRevocation must sit inside a handler func literal")

	var guards []*ast.IfStmt
	ast.Inspect(handler, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		call, ok := ifs.Cond.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "rejectMissingJTI" {
			guards = append(guards, ifs)
		}
		return true
	})
	require.Len(t, guards, 1, "D-09: exactly one `if rejectMissingJTI(...)` guard in the user handler")
	g := guards[0]
	require.Less(t, int(g.Pos()), int(revPos), "D-09: jti guard must precede the user-listener CheckRevocation")
	require.NotEmpty(t, g.Body.List)
	_, isReturn := g.Body.List[len(g.Body.List)-1].(*ast.ReturnStmt)
	require.True(t, isReturn, "D-09: jti guard body must return")

	// The TOKEN_MISSING_JTI deny literal lives in rejectMissingJTI, under the missingJTI check.
	f, _ := wiringParseMain(t)
	var helper *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "rejectMissingJTI" {
			helper = fd
		}
	}
	require.NotNil(t, helper, "rejectMissingJTI must exist in main.go")
	var denies int
	for _, c := range wiringCalls(helper, func(fn ast.Expr) bool { return wiringSelector(fn, "ac", "SetDecision") }) {
		if len(c.call.Args) == 2 {
			if l, ok := c.call.Args[1].(*ast.BasicLit); ok && l.Value == `"TOKEN_MISSING_JTI"` {
				denies++
			}
		}
	}
	require.Equal(t, 1, denies, "D-09: rejectMissingJTI must SetDecision deny TOKEN_MISSING_JTI")
}

// TestMissingJTIBehaviour signs a token with no jti using the demo seed,
// validates it through the gateway's TokenValidator, and checks the guard
// answers 401 with audit reason TOKEN_MISSING_JTI (D-18). A token with a jti
// passes the guard untouched.
func TestMissingJTIBehaviour(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(defaultDemoSeed)
	validator := identity.NewTokenValidator("aegis-issuer", "aegis-gateway", priv.Public())
	sign := func(jti string) string {
		now := time.Now()
		tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, identity.UserClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "aegis-issuer",
				Subject:   "developer",
				Audience:  jwt.ClaimStrings{"aegis-gateway"},
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
				ID:        jti,
			},
			Roles: []string{"developer"},
		})
		s, err := tok.SignedString(priv)
		require.NoError(t, err)
		return s
	}

	claims, err := validator.ValidateBearerToken("Bearer " + sign(""))
	require.NoError(t, err, "a validly signed token without jti must pass signature validation")
	_, ac := audit.WithAuditContext(context.Background(), "req-missing-jti")
	rec := httptest.NewRecorder()
	require.True(t, rejectMissingJTI(rec, ac, claims, "req-missing-jti"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	ev := ac.ToCompletionEvent(http.MethodGet, "10.0.0.1", 1)
	require.Equal(t, "deny", ev.Decision)
	require.Equal(t, "TOKEN_MISSING_JTI", ev.ReasonCode)

	// nil audit context still fails closed.
	rec = httptest.NewRecorder()
	require.True(t, rejectMissingJTI(rec, nil, claims, "req-nil-ac"))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	withJTI, err := validator.ValidateBearerToken("Bearer " + sign("4f1d6c1e-0000-4000-8000-000000000001"))
	require.NoError(t, err)
	_, ac2 := audit.WithAuditContext(context.Background(), "req-with-jti")
	rec = httptest.NewRecorder()
	require.False(t, rejectMissingJTI(rec, ac2, withJTI, "req-with-jti"))
	require.Equal(t, http.StatusOK, rec.Code, "guard must not write a response for a token with jti")
	require.NotEqual(t, "TOKEN_MISSING_JTI", ac2.ToCompletionEvent(http.MethodGet, "10.0.0.1", 1).ReasonCode)
}

func TestMissingJTISetJTIWired(t *testing.T) {
	_, mainFn := wiringParseMain(t)
	require.NotEmpty(t, wiringCalls(mainFn, func(fn ast.Expr) bool { return wiringSelector(fn, "ac", "SetJTI") }))
}
