package control

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const specPath = "../../api/openapi/control-v1.yaml"

var paramRe = regexp.MustCompile(`\{[^}]+\}`)

func normPath(p string) string {
	p = strings.TrimSuffix(p, "/")
	return paramRe.ReplaceAllString(p, "{id}")
}

type specDoc struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Parameters map[string]struct {
			Required bool `yaml:"required"`
		} `yaml:"parameters"`
	} `yaml:"components"`
}

type specOp struct {
	Parameters []struct {
		Ref string `yaml:"$ref"`
	} `yaml:"parameters"`
	Responses map[string]yaml.Node `yaml:"responses"`
}

var httpMethods = map[string]bool{"get": true, "post": true, "put": true, "delete": true, "patch": true}

func loadSpec(t *testing.T) (specDoc, map[string]specOp) {
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err)
	var doc specDoc
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	ops := map[string]specOp{}
	for p, item := range doc.Paths {
		for m, node := range item {
			if !httpMethods[m] {
				continue
			}
			var op specOp
			require.NoError(t, node.Decode(&op))
			ops[strings.ToUpper(m)+" "+normPath(p)] = op
		}
	}
	return doc, ops
}

func hasRef(op specOp, ref string) bool {
	for _, p := range op.Parameters {
		if p.Ref == ref {
			return true
		}
	}
	return false
}

func TestOpenAPIDrift_RoutesInSpec(t *testing.T) {
	h := setupTestHarness(t)
	routes := map[string]bool{}
	err := chi.Walk(h.server.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/control/v1/") {
			routes[method+" "+normPath(route)] = true
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, routes)

	_, ops := loadSpec(t)
	var missingInSpec, missingInRouter []string
	for r := range routes {
		if _, ok := ops[r]; !ok {
			missingInSpec = append(missingInSpec, r)
		}
	}
	for o := range ops {
		if !routes[o] {
			missingInRouter = append(missingInRouter, o)
		}
	}
	sort.Strings(missingInSpec)
	sort.Strings(missingInRouter)
	require.Empty(t, missingInSpec, "router routes absent from OpenAPI spec")
	require.Empty(t, missingInRouter, "spec operations with no router route")
}

func TestOpenAPIDrift_IdempotencyKey(t *testing.T) {
	doc, ops := loadSpec(t)
	require.True(t, doc.Components.Parameters["IdempotencyKey"].Required, "IdempotencyKey must be required: true")

	gated := []string{
		"POST /control/v1/routes",
		"POST /control/v1/policies",
		"POST /control/v1/policies/{id}/publish",
		"POST /control/v1/policies/{id}/rollback",
		"POST /control/v1/snapshots/rollback",
		"POST /control/v1/principals/{id}/quarantine",
		"DELETE /control/v1/principals/{id}/quarantine",
		"POST /control/v1/quarantine",
		"POST /control/v1/revocations",
	}
	gatedSet := map[string]bool{}
	for _, g := range gated {
		gatedSet[g] = true
		op, ok := ops[g]
		require.True(t, ok, "missing op %s", g)
		require.True(t, hasRef(op, "#/components/parameters/IdempotencyKey"), "%s must reference IdempotencyKey", g)
	}
	for k, op := range ops {
		if !gatedSet[k] {
			require.False(t, hasRef(op, "#/components/parameters/IdempotencyKey"), "%s must not reference IdempotencyKey", k)
		}
	}
}

func TestOpenAPIDrift_Publish(t *testing.T) {
	_, ops := loadSpec(t)
	pub := ops["POST /control/v1/policies/{id}/publish"]
	_, ok := pub.Responses["422"]
	require.True(t, ok, "publish must document 422")
	require.True(t, hasRef(pub, "#/components/parameters/IfMatch"))
	for _, k := range []string{"POST /control/v1/policies/{id}/rollback", "POST /control/v1/snapshots/rollback"} {
		require.True(t, hasRef(ops[k], "#/components/parameters/IfMatch"), k)
	}
}
