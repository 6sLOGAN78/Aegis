package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestComposeDistributedDrainGrace asserts stop_grace_period covers the
// two-phase drain: grace >= propagation delay + drain timeout + 10s.
func TestComposeDistributedDrainGrace(t *testing.T) {
	services, _ := loadCompose(t, "distributed")
	for _, name := range []string{"gateway-1", "gateway-2", "gateway-3"} {
		svc, ok := services[name]
		require.True(t, ok, "distributed compose missing %s", name)

		raw, ok := svc["stop_grace_period"]
		require.True(t, ok, "%s must set stop_grace_period", name)
		grace, err := time.ParseDuration(fmt.Sprint(raw))
		require.NoError(t, err)

		env := envMap(svc)
		delayStr, ok := env["AEGIS_DRAIN_PROPAGATION_DELAY"]
		require.True(t, ok, "%s must set AEGIS_DRAIN_PROPAGATION_DELAY", name)
		delay, err := time.ParseDuration(delayStr)
		require.NoError(t, err)

		drain := 30 * time.Second
		if v, ok := env["AEGIS_DRAIN_TIMEOUT"]; ok {
			drain, err = time.ParseDuration(v)
			require.NoError(t, err)
		}
		require.GreaterOrEqual(t, grace, delay+drain+10*time.Second,
			"%s: stop_grace_period %s too small for delay %s + drain %s + 10s", name, grace, delay, drain)
	}
}

// TestHAProxyWorkloadTracksReadyz asserts the :9443 workload servers follow
// the :8080 /readyz check so a draining replica leaves both backends.
func TestHAProxyWorkloadTracksReadyz(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deployments", "compose", "haproxy", "haproxy.cfg"))
	require.NoError(t, err)
	cfg := string(data)
	for _, n := range []string{"1", "2", "3"} {
		re := regexp.MustCompile(`(?m)^\s*server gateway-` + n + ` gateway-` + n + `:9443\b(.*)$`)
		m := re.FindStringSubmatch(cfg)
		require.NotNil(t, m, "no :9443 server line for gateway-%s", n)
		require.Contains(t, m[1], "track aegis_http_back/gateway-"+n)
		require.NotRegexp(t, `\bcheck\b`, m[1], "tracking server must not carry its own check")
	}
}
