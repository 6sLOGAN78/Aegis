package manifests

// NetworkPolicy peer cross-check (Phase 12, N1): every pod that connects to
// postgres, redis or the control-plane gRPC address must be admitted by the
// target's ingress policy and allowed by its own egress policy, and no policy
// peer may name a pod label that no workload carries.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type npEdge struct {
	source obj // source pod labels
	srcDoc string
	target string // target app label
	port   int
}

func labelsOf(d manifestDoc) map[string]string {
	out := map[string]string{}
	if m, ok := dig(d.Body, "spec", "template", "metadata", "labels").(obj); ok {
		for k, v := range m {
			out[k] = str(v)
		}
	}
	return out
}

func selectorLabels(v interface{}) map[string]string {
	out := map[string]string{}
	if m, ok := dig(v, "matchLabels").(obj); ok {
		for k, val := range m {
			out[k] = str(val)
		}
	}
	return out
}

func subset(sub, super map[string]string) bool {
	for k, v := range sub {
		if super[k] != v {
			return false
		}
	}
	return true
}

// resolvedEnv returns env name -> value for a container (direct values plus
// envFrom ConfigMap data). Secret-backed values resolve to "".
func resolvedEnv(t *testing.T, docs []manifestDoc, c obj) map[string]string {
	out := map[string]string{}
	for _, ef := range list(c["envFrom"]) {
		ref := str(dig(ef, "configMapRef", "name"))
		if ref == "" {
			continue
		}
		for k, v := range configMapData(t, docs, ref) {
			out[k] = str(v)
		}
	}
	for _, e := range list(c["env"]) {
		out[str(e["name"])] = str(e["value"])
	}
	return out
}

func portFromAddr(v string, def int) int {
	if i := strings.LastIndex(v, ":"); i >= 0 {
		if n, err := strconv.Atoi(v[i+1:]); err == nil {
			return n
		}
	}
	return def
}

// policyFor returns the NetworkPolicy whose podSelector selects labels.
func policyFor(docs []manifestDoc, labels map[string]string) (manifestDoc, bool) {
	for _, d := range docs {
		if d.kind() != "NetworkPolicy" {
			continue
		}
		sel := selectorLabels(dig(d.Body, "spec", "podSelector"))
		if len(sel) > 0 && subset(sel, labels) {
			return d, true
		}
	}
	return manifestDoc{}, false
}

func rulePortsInclude(rule obj, port int) bool {
	for _, p := range list(rule["ports"]) {
		if n, ok := p["port"].(int); ok && n == port {
			return true
		}
	}
	return false
}

func TestManifestNetworkPolicyPeers(t *testing.T) {
	docs := baseDocs(t)

	targets := map[string]map[string]string{
		"postgres":            {"app": "postgres"},
		"redis":               {"app": "redis"},
		"aegis-control-plane": {"app": "aegis-control-plane"},
	}

	var edges []npEdge
	var allLabels []map[string]string
	for _, d := range docs {
		if d.kind() != "Deployment" && d.kind() != "StatefulSet" {
			continue
		}
		labels := labelsOf(d)
		allLabels = append(allLabels, labels)
		src := obj{}
		for k, v := range labels {
			src[k] = v
		}
		seen := map[string]bool{}
		add := func(target string, port int) {
			key := fmt.Sprintf("%s:%d", target, port)
			if !seen[key] {
				seen[key] = true
				edges = append(edges, npEdge{source: src, srcDoc: d.name(), target: target, port: port})
			}
		}
		for _, c := range list(podSpec(d)["containers"]) {
			for name, val := range resolvedEnv(t, docs, c) {
				switch {
				case (strings.HasPrefix(name, "AEGIS_DB_") || strings.Contains(name, "DATABASE") || strings.Contains(name, "DSN")) &&
					strings.Contains(val, "postgres"):
					add("postgres", 5432)
				case name == "AEGIS_REDIS_ADDR":
					add("redis", portFromAddr(val, 6379))
				case name == "AEGIS_CONTROL_PLANE_GRPC_ADDR":
					add("aegis-control-plane", portFromAddr(val, 9090))
				}
			}
		}
	}
	require.NotEmpty(t, edges)

	for _, e := range edges {
		e := e
		srcLabels := map[string]string{}
		for k, v := range e.source {
			srcLabels[k] = str(v)
		}
		t.Run(fmt.Sprintf("%s->%s:%d", e.srcDoc, e.target, e.port), func(t *testing.T) {
			tgt := targets[e.target]

			// (a) target ingress admits the source pod on the port
			tp, ok := policyFor(docs, tgt)
			require.True(t, ok, "no NetworkPolicy selects %s", e.target)
			admitted := false
			for _, r := range list(dig(tp.Body, "spec", "ingress")) {
				if !rulePortsInclude(r, e.port) {
					continue
				}
				for _, f := range list(r["from"]) {
					if _, hasNS := f["namespaceSelector"]; hasNS {
						continue
					}
					sel := selectorLabels(f["podSelector"])
					if len(sel) > 0 && subset(sel, srcLabels) {
						admitted = true
					}
				}
			}
			assert.True(t, admitted, "%s ingress does not admit %s (labels %v) on TCP %d", e.target, e.srcDoc, srcLabels, e.port)

			// (b) source egress allows target on the port
			sp, ok := policyFor(docs, srcLabels)
			require.True(t, ok, "no NetworkPolicy selects %s", e.srcDoc)
			allowed := false
			for _, r := range list(dig(sp.Body, "spec", "egress")) {
				if !rulePortsInclude(r, e.port) {
					continue
				}
				for _, to := range list(r["to"]) {
					if _, hasNS := to["namespaceSelector"]; hasNS {
						continue
					}
					sel := selectorLabels(to["podSelector"])
					if len(sel) > 0 && subset(sel, tgt) {
						allowed = true
					}
				}
			}
			assert.True(t, allowed, "%s egress does not allow %s on TCP %d", e.srcDoc, e.target, e.port)
		})
	}

	// No dead peers: same-namespace podSelector peers must match a real
	// workload label set. Peers with a namespaceSelector (external namespaces,
	// kube-system DNS) are out of this label space and are skipped.
	for _, d := range docs {
		if d.kind() != "NetworkPolicy" {
			continue
		}
		check := func(dir string, rules []obj, key string) {
			for _, r := range rules {
				for _, p := range list(r[key]) {
					if _, hasNS := p["namespaceSelector"]; hasNS {
						continue
					}
					sel := selectorLabels(p["podSelector"])
					if len(sel) == 0 {
						continue
					}
					found := false
					for _, l := range allLabels {
						if subset(sel, l) {
							found = true
						}
					}
					assert.True(t, found, "%s %s peer %v matches no workload pod label", d.name(), dir, sel)
				}
			}
		}
		check("ingress", list(dig(d.Body, "spec", "ingress")), "from")
		check("egress", list(dig(d.Body, "spec", "egress")), "to")
	}
}
