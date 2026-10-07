// Package compose holds a hermetic lint over the Docker Compose profiles
// (mvp, hardened, distributed). It needs no Docker daemon: it parses the YAML
// and checks that the files stay aligned with the env vars the binaries read.
//
// Failure messages name the audit blocker they relate to:
//
//	B1 - gateway has no control-plane/Redis/spool wiring (no policy/routes loaded)
//	B3 - audit worker does not drain every gateway spool
//	B4 - control plane has no Redis, so quarantine returns 503
package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const spoolMount = "/var/log/aegis/wal"

var profiles = []string{"mvp", "hardened", "distributed"}

// gatewayAllowlist is the set of AEGIS_* keys a gateway actually reads
// (internal/config/config.go, cmd/gateway/main.go, internal/proxy/router.go).
var gatewayAllowlist = map[string]bool{
	"AEGIS_PORT":                       true,
	"AEGIS_WORKLOAD_PORT":              true,
	"AEGIS_MAX_CONCURRENT":             true,
	"AEGIS_TLS_CERT_PATH":              true,
	"AEGIS_TLS_KEY_PATH":               true,
	"AEGIS_CLIENT_CERT_PATH":           true,
	"AEGIS_CLIENT_KEY_PATH":            true,
	"AEGIS_WORKLOAD_CA_PATH":           true,
	"AEGIS_ASSERTION_PRIVATE_KEY_PATH": true,
	"AEGIS_ASSERTION_PUBLIC_KEY_PATH":  true,
	"AEGIS_CONTROL_PLANE_GRPC_ADDR":    true,
	"AEGIS_REDIS_ADDR":                 true,
	"AEGIS_REDIS_PASSWORD":             true,
	"AEGIS_SPOOL_DIR":                  true,
	"AEGIS_SPOOL_MAX_BYTES":            true,
	"AEGIS_ISSUER":                     true,
	"AEGIS_AUDIENCE":                   true,
	"AEGIS_ISSUER_PUBLIC_KEY":          true,
	"AEGIS_CONTROL_PLANE_PUBLIC_KEY":   true,
	"AEGIS_GATEWAY_ID":                 true,
	"AEGIS_METRICS_PORT":               true,
	"AEGIS_ASSERTION_PRIVATE_KEY":      true,
	"AEGIS_DRAIN_TIMEOUT":              true,
	"AEGIS_UPSTREAM_SCHEME":            true,
}

type service = map[string]interface{}

type mount struct {
	source   string
	target   string
	readOnly bool
}

func composePath(profile string) string {
	return filepath.Join("..", "..", "deployments", "compose", "docker-compose."+profile+".yml")
}

func loadCompose(t *testing.T, profile string) (map[string]service, map[string]interface{}) {
	t.Helper()
	data, err := os.ReadFile(composePath(profile))
	require.NoError(t, err, "failed to read compose file for profile %s", profile)

	var doc map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &doc), "failed to parse compose YAML for profile %s", profile)

	rawServices, ok := doc["services"].(map[string]interface{})
	require.True(t, ok, "profile %s: services must be a map", profile)

	services := make(map[string]service, len(rawServices))
	for name, raw := range rawServices {
		svc, ok := raw.(map[string]interface{})
		require.True(t, ok, "profile %s: service %s must be a map", profile, name)
		services[name] = svc
	}

	topVolumes := map[string]interface{}{}
	if v, ok := doc["volumes"].(map[string]interface{}); ok {
		topVolumes = v
	}
	return services, topVolumes
}

// envMap parses environment in list form ("K=V") and tolerates map form.
func envMap(svc service) map[string]string {
	out := map[string]string{}
	switch env := svc["environment"].(type) {
	case []interface{}:
		for _, e := range env {
			s := fmt.Sprint(e)
			if k, v, found := strings.Cut(s, "="); found {
				out[k] = v
			} else {
				out[s] = ""
			}
		}
	case map[string]interface{}:
		for k, v := range env {
			if v == nil {
				out[k] = ""
			} else {
				out[k] = fmt.Sprint(v)
			}
		}
	}
	return out
}

// dependsOn returns dependency name -> condition. List form yields service_started.
func dependsOn(svc service) map[string]string {
	out := map[string]string{}
	switch d := svc["depends_on"].(type) {
	case []interface{}:
		for _, n := range d {
			out[fmt.Sprint(n)] = "service_started"
		}
	case map[string]interface{}:
		for n, v := range d {
			cond := "service_started"
			if m, ok := v.(map[string]interface{}); ok {
				if c, ok := m["condition"].(string); ok {
					cond = c
				}
			}
			out[n] = cond
		}
	}
	return out
}

func volumeMounts(svc service) []mount {
	var out []mount
	vols, _ := svc["volumes"].([]interface{})
	for _, v := range vols {
		parts := strings.Split(fmt.Sprint(v), ":")
		if len(parts) < 2 {
			continue
		}
		m := mount{source: parts[0], target: parts[1]}
		if len(parts) >= 3 && strings.Contains(parts[2], "ro") {
			m.readOnly = true
		}
		out = append(out, m)
	}
	return out
}

func publishedPorts(svc service) []string {
	var out []string
	ports, _ := svc["ports"].([]interface{})
	for _, p := range ports {
		out = append(out, fmt.Sprint(p))
	}
	sort.Strings(out)
	return out
}

func exposedPorts(svc service) []string {
	var out []string
	ports, _ := svc["expose"].([]interface{})
	for _, p := range ports {
		out = append(out, fmt.Sprint(p))
	}
	sort.Strings(out)
	return out
}

func namesMatching(services map[string]service, base string) []string {
	var out []string
	for name := range services {
		if name == base || strings.HasPrefix(name, base+"-") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func gatewayNames(services map[string]service) []string { return namesMatching(services, "gateway") }
func workerNames(services map[string]service) []string {
	return namesMatching(services, "audit-worker")
}

// spoolVolumes returns the named volumes mounted at the spool path.
func spoolVolumes(svc service) []mount {
	var out []mount
	for _, m := range volumeMounts(svc) {
		if m.target == spoolMount {
			out = append(out, m)
		}
	}
	return out
}

func healthcheckTest(svc service) string {
	hc, ok := svc["healthcheck"].(map[string]interface{})
	if !ok {
		return ""
	}
	switch tst := hc["test"].(type) {
	case string:
		return tst
	case []interface{}:
		parts := make([]string, 0, len(tst))
		for _, p := range tst {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func TestControlPlaneHasRedis(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			require.Contains(t, services, "redis", "%s: redis service missing (B4)", profile)
			cp, ok := services["control-plane"]
			require.True(t, ok, "%s: control-plane service missing (B4)", profile)
			assert.Equal(t, "redis:6379", envMap(cp)["AEGIS_REDIS_ADDR"],
				"%s: control-plane must set AEGIS_REDIS_ADDR=redis:6379 or quarantine returns 503 (B4)", profile)
		})
	}
}

func TestMVPTopology(t *testing.T) {
	services, topVolumes := loadCompose(t, "mvp")

	for _, name := range []string{"postgres", "redis", "control-plane", "seed", "gateway", "audit-worker",
		"demo-issuer", "orders", "payments", "admin"} {
		assert.Contains(t, services, name, "mvp: service %s missing; mvp must run the full secure slice (B1)", name)
	}

	if gw, ok := services["gateway"]; ok {
		env := envMap(gw)
		assert.Equal(t, "control-plane:9090", env["AEGIS_CONTROL_PLANE_GRPC_ADDR"], "mvp gateway: no control-plane address (B1)")
		assert.Equal(t, "redis:6379", env["AEGIS_REDIS_ADDR"], "mvp gateway: no Redis address (B1/B4)")
		assert.Equal(t, spoolMount, env["AEGIS_SPOOL_DIR"], "mvp gateway: no spool dir (B1/B3)")
		assert.Len(t, spoolVolumes(gw), 1, "mvp gateway: must mount a named volume at %s (B3)", spoolMount)
	}

	assert.Contains(t, topVolumes, "postgres_data", "mvp: top-level volume postgres_data missing")
	assert.Contains(t, topVolumes, "aegis_wal_spool", "mvp: top-level volume aegis_wal_spool missing (B3)")

	for _, name := range []string{"orders", "payments", "admin", "control-plane", "postgres", "redis"} {
		if svc, ok := services[name]; ok {
			assert.Empty(t, publishedPorts(svc), "mvp: %s must not publish host ports (BYP-01)", name)
		}
	}
}

func TestNoDeadEnv(t *testing.T) {
	dead := []string{"AEGIS_ROUTES_PATH", "AEGIS_POLICY_PATH"}
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			for name, svc := range services {
				env := envMap(svc)
				for _, key := range dead {
					assert.NotContains(t, env, key,
						"%s/%s sets %s which no binary reads; routes and policy come from the seed service (B1)", profile, name, key)
				}
			}
		})
	}
}

func TestGatewayWiring(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			gateways := gatewayNames(services)
			require.NotEmpty(t, gateways, "%s: no gateway service found", profile)
			for _, name := range gateways {
				env := envMap(services[name])
				assert.Equal(t, "control-plane:9090", env["AEGIS_CONTROL_PLANE_GRPC_ADDR"], "%s/%s: AEGIS_CONTROL_PLANE_GRPC_ADDR (B1)", profile, name)
				assert.Equal(t, "redis:6379", env["AEGIS_REDIS_ADDR"], "%s/%s: AEGIS_REDIS_ADDR (B1/B4)", profile, name)
				assert.Equal(t, spoolMount, env["AEGIS_SPOOL_DIR"], "%s/%s: AEGIS_SPOOL_DIR (B1/B3)", profile, name)
				for key := range env {
					if strings.HasPrefix(key, "AEGIS_") {
						assert.True(t, gatewayAllowlist[key], "%s/%s: %s is not read by the gateway binary", profile, name, key)
					}
				}
			}
		})
	}
}

func TestAuditWorkerPerSpool(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			gateways := gatewayNames(services)
			workers := workerNames(services)
			require.NotEmpty(t, gateways, "%s: no gateway", profile)

			assert.Equal(t, len(gateways), len(workers),
				"%s: need one audit-worker per gateway spool, got %d gateways and %d workers (B3)", profile, len(gateways), len(workers))

			gwBySpool := map[string][]string{}
			for _, g := range gateways {
				sv := spoolVolumes(services[g])
				if assert.Len(t, sv, 1, "%s/%s: must mount exactly one volume at %s (B3)", profile, g, spoolMount) {
					gwBySpool[sv[0].source] = append(gwBySpool[sv[0].source], g)
				}
			}
			for spool, gws := range gwBySpool {
				assert.Len(t, gws, 1, "%s: spool %s is shared by gateways %v; segments would interleave (B3)", profile, spool, gws)
			}

			workerBySpool := map[string][]string{}
			for _, w := range workers {
				sv := spoolVolumes(services[w])
				if assert.Len(t, sv, 1, "%s/%s: worker must mount exactly one spool volume (B3)", profile, w) {
					assert.False(t, sv[0].readOnly, "%s/%s: worker must mount the spool read-write to archive and prune (B3)", profile, w)
					workerBySpool[sv[0].source] = append(workerBySpool[sv[0].source], w)
				}
				_, ok := dependsOn(services[w])["control-plane"]
				assert.True(t, ok, "%s/%s: worker must depend_on control-plane, which creates audit_events (B3)", profile, w)
			}
			for spool := range gwBySpool {
				assert.Len(t, workerBySpool[spool], 1,
					"%s: spool %s must be drained by exactly one audit-worker, got %v (B3)", profile, spool, workerBySpool[spool])
			}
		})
	}
}

func TestGatewayStopGracePeriod(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			for _, name := range gatewayNames(services) {
				svc := services[name]
				drain := 30 * time.Second
				if v, ok := envMap(svc)["AEGIS_DRAIN_TIMEOUT"]; ok {
					d, err := time.ParseDuration(v)
					require.NoError(t, err, "%s/%s: AEGIS_DRAIN_TIMEOUT %q", profile, name, v)
					drain = d
				}
				raw, ok := svc["stop_grace_period"].(string)
				if !assert.True(t, ok, "%s/%s: stop_grace_period missing; default 10s SIGKILLs a draining gateway (exit 137)", profile, name) {
					continue
				}
				grace, err := time.ParseDuration(raw)
				require.NoError(t, err, "%s/%s: stop_grace_period %q", profile, name, raw)
				assert.GreaterOrEqual(t, grace, drain+5*time.Second,
					"%s/%s: stop_grace_period %s must be >= drain %s + 5s", profile, name, grace, drain)
			}
		})
	}
}

func TestSeedService(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			seed, ok := services["seed"]
			require.True(t, ok, "%s: seed service missing; the control plane starts with empty deny-all state (B1)", profile)

			build, _ := seed["build"].(map[string]interface{})
			assert.Equal(t, "seed", build["target"], "%s seed: build target", profile)
			assert.Equal(t, "no", fmt.Sprint(seed["restart"]), "%s seed: restart must be \"no\" (one-shot)", profile)
			assert.Empty(t, publishedPorts(seed), "%s seed: must not publish ports", profile)
			assert.Empty(t, exposedPorts(seed), "%s seed: must not expose ports", profile)

			env := envMap(seed)
			assert.Equal(t, "http://control-plane:8084", env["AEGIS_CP_URL"], "%s seed: AEGIS_CP_URL", profile)
			assert.Contains(t, env, "AEGIS_SEED_USER", "%s seed: AEGIS_SEED_USER", profile)
			assert.Contains(t, env, "AEGIS_SEED_PASSWORD", "%s seed: AEGIS_SEED_PASSWORD", profile)

			_, ok = dependsOn(seed)["control-plane"]
			assert.True(t, ok, "%s seed: must depend_on control-plane", profile)

			for _, g := range gatewayNames(services) {
				assert.Equal(t, "service_completed_successfully", dependsOn(services[g])["seed"],
					"%s/%s: gateway must wait for seed to complete (B1)", profile, g)
			}
		})
	}
}

func TestControlPlaneHealthcheck(t *testing.T) {
	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			cp, ok := services["control-plane"]
			require.True(t, ok, "%s: control-plane missing", profile)
			test := healthcheckTest(cp)
			assert.Contains(t, test, "8084", "%s control-plane healthcheck must hit the REST port", profile)
			assert.Contains(t, test, "/control/v1/auth/me", "%s control-plane healthcheck must use /control/v1/auth/me", profile)
			assert.NotContains(t, test, "/livez", "%s control-plane has no /livez", profile)
			assert.NotContains(t, test, "/readyz", "%s control-plane has no /readyz", profile)

			for _, g := range gatewayNames(services) {
				assert.Contains(t, healthcheckTest(services[g]), "/readyz", "%s/%s: gateway healthcheck must use /readyz", profile, g)
			}
		})
	}
}

func TestPortsMatchBinaries(t *testing.T) {
	t.Run("mvp", func(t *testing.T) {
		services, _ := loadCompose(t, "mvp")
		gw := services["gateway"]
		require.NotNil(t, gw, "mvp gateway missing")
		assert.Equal(t, []string{"8080:8080", "9443:9443"}, publishedPorts(gw))
		assert.Equal(t, "8080", envMap(gw)["AEGIS_PORT"])
		assert.Equal(t, "9443", envMap(gw)["AEGIS_WORKLOAD_PORT"])
		assert.Equal(t, []string{"8085:8085"}, publishedPorts(services["demo-issuer"]))
	})

	t.Run("hardened", func(t *testing.T) {
		services, _ := loadCompose(t, "hardened")
		gw := services["gateway"]
		require.NotNil(t, gw, "hardened gateway missing")
		assert.Equal(t, "8080", envMap(gw)["AEGIS_PORT"])
		assert.Equal(t, "9443", envMap(gw)["AEGIS_WORKLOAD_PORT"])
		assert.Equal(t, []string{"8085:8085"}, publishedPorts(services["demo-issuer"]))
		cp := services["control-plane"]
		require.NotNil(t, cp, "hardened control-plane missing")
		assert.Equal(t, []string{"8084:8084", "9090:9090"}, publishedPorts(cp))
		assert.Equal(t, "8084", envMap(cp)["AEGIS_PORT"])
		assert.Equal(t, "9090", envMap(cp)["AEGIS_GRPC_PORT"])
	})

	t.Run("distributed", func(t *testing.T) {
		services, _ := loadCompose(t, "distributed")
		hp := services["haproxy"]
		require.NotNil(t, hp, "distributed haproxy missing")
		assert.Equal(t, []string{"8080:8080", "8404:8404", "9443:9443"}, publishedPorts(hp))
		cp := services["control-plane"]
		require.NotNil(t, cp, "distributed control-plane missing")
		assert.Equal(t, []string{"8084:8084", "9090:9090", "9092:9092"}, publishedPorts(cp))
		assert.Equal(t, "8084", envMap(cp)["AEGIS_PORT"])
		assert.Equal(t, "9090", envMap(cp)["AEGIS_GRPC_PORT"])
		for _, g := range gatewayNames(services) {
			assert.Equal(t, "8080", envMap(services[g])["AEGIS_PORT"], "distributed/%s", g)
			assert.Equal(t, "9443", envMap(services[g])["AEGIS_WORKLOAD_PORT"], "distributed/%s", g)
			assert.Empty(t, publishedPorts(services[g]), "distributed/%s: reachable only via haproxy", g)
		}
	})

	backends := map[string]string{"orders": "8081", "payments": "8082", "admin": "8083"}
	for _, profile := range profiles {
		t.Run(profile+"-backends", func(t *testing.T) {
			services, _ := loadCompose(t, profile)
			for name, port := range backends {
				svc, ok := services[name]
				require.True(t, ok, "%s: backend %s missing", profile, name)
				assert.Equal(t, port, envMap(svc)["PORT"], "%s/%s PORT", profile, name)
				assert.Equal(t, []string{port}, exposedPorts(svc), "%s/%s must only expose its port", profile, name)
				assert.Empty(t, publishedPorts(svc), "%s/%s must not publish ports (BYP-01)", profile, name)
			}
		})
	}
}

func TestSeedDockerfileTarget(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deployments", "compose", "Dockerfile"))
	require.NoError(t, err)
	text := string(data)

	idx := strings.Index(text, "FROM alpine:3.21 AS seed")
	require.GreaterOrEqual(t, idx, 0, "Dockerfile must define stage `FROM alpine:3.21 AS seed`")
	stage := text[idx:]
	if next := strings.Index(stage[1:], "\nFROM "); next >= 0 {
		stage = stage[:next+1]
	}
	assert.Contains(t, stage, "apk add --no-cache curl jq", "seed stage must install curl and jq")
	assert.Contains(t, stage, "seed.sh", "seed stage must copy seed.sh")
}
