package manifests

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var preStopSleepRe = regexp.MustCompile(`^\s*sleep\s+(\d+)\s*$`)

// TestManifestGatewayDrainBudget asserts the gateway StatefulSet grants enough
// termination time for the two-phase drain (DIST-01, T-12-21, T-12-23):
// grace >= preStop sleep + drain propagation delay + drain timeout + 10s
// (5s audit flush + 5s metrics shutdown).
func TestManifestGatewayDrainBudget(t *testing.T) {
	docs := baseDocs(t)
	sts := findDoc(t, docs, "StatefulSet", "aegis-gateway")

	graceRaw, ok := podSpec(sts)["terminationGracePeriodSeconds"]
	require.True(t, ok, "gateway pod spec must set terminationGracePeriodSeconds")
	grace, err := strconv.Atoi(str(graceRaw))
	require.NoError(t, err)

	c := containerByName(t, sts, "gateway")
	cmd, _ := dig(c, "lifecycle", "preStop", "exec", "command").([]interface{})
	require.Len(t, cmd, 3, "gateway preStop must be exec [sh -c 'sleep N']")
	require.Equal(t, "sh", str(cmd[0]))
	require.Equal(t, "-c", str(cmd[1]))
	m := preStopSleepRe.FindStringSubmatch(str(cmd[2]))
	require.NotNil(t, m, "preStop command must be 'sleep N', got %q", str(cmd[2]))
	preStop, _ := strconv.Atoi(m[1])

	cm := findDoc(t, docs, "ConfigMap", "aegis-gateway-config")
	data, _ := dig(cm.Body, "data").(obj)
	delayRaw, ok := data["AEGIS_DRAIN_PROPAGATION_DELAY"]
	require.True(t, ok, "configmap must set AEGIS_DRAIN_PROPAGATION_DELAY")
	delay, err := time.ParseDuration(str(delayRaw))
	require.NoError(t, err)

	drain := 30 * time.Second
	if v, ok := data["AEGIS_DRAIN_TIMEOUT"]; ok {
		drain, err = time.ParseDuration(str(v))
		require.NoError(t, err)
	}

	need := time.Duration(preStop)*time.Second + delay + drain + 10*time.Second
	require.GreaterOrEqual(t, time.Duration(grace)*time.Second, need,
		"terminationGracePeriodSeconds=%d too small for preStop=%ds + delay=%s + drain=%s + 10s",
		grace, preStop, delay, drain)
}

// TestManifestAuditWorkerOutlivesGatewayDrain asserts the audit-worker sidecar
// only receives SIGTERM after the gateway's worst-case drain finished writing
// WAL records (W3, T-13-05, T-13-06): sidecar preStop sleep >= gateway preStop
// + propagation delay + drain timeout + 10s, and sleep + 10s <= grace.
func TestManifestAuditWorkerOutlivesGatewayDrain(t *testing.T) {
	docs := baseDocs(t)
	sts := findDoc(t, docs, "StatefulSet", "aegis-gateway")

	graceRaw, ok := podSpec(sts)["terminationGracePeriodSeconds"]
	require.True(t, ok, "gateway pod spec must set terminationGracePeriodSeconds")
	grace, err := strconv.Atoi(str(graceRaw))
	require.NoError(t, err)

	sleepOf := func(name string) time.Duration {
		c := containerByName(t, sts, name)
		cmd, _ := dig(c, "lifecycle", "preStop", "exec", "command").([]interface{})
		require.Len(t, cmd, 3, "%s preStop must be exec [sh -c 'sleep N']", name)
		require.Equal(t, "sh", str(cmd[0]))
		require.Equal(t, "-c", str(cmd[1]))
		m := preStopSleepRe.FindStringSubmatch(str(cmd[2]))
		require.NotNil(t, m, "%s preStop command must be 'sleep N', got %q", name, str(cmd[2]))
		n, _ := strconv.Atoi(m[1])
		return time.Duration(n) * time.Second
	}
	gwPreStop := sleepOf("gateway")
	sidecar := sleepOf("audit-worker")

	cm := findDoc(t, docs, "ConfigMap", "aegis-gateway-config")
	data, _ := dig(cm.Body, "data").(obj)
	delayRaw, ok := data["AEGIS_DRAIN_PROPAGATION_DELAY"]
	require.True(t, ok, "configmap must set AEGIS_DRAIN_PROPAGATION_DELAY")
	delay, err := time.ParseDuration(str(delayRaw))
	require.NoError(t, err)
	drain := 30 * time.Second
	if v, ok := data["AEGIS_DRAIN_TIMEOUT"]; ok {
		drain, err = time.ParseDuration(str(v))
		require.NoError(t, err)
	}

	need := gwPreStop + delay + drain + 10*time.Second
	require.GreaterOrEqual(t, sidecar, need,
		"audit-worker preStop sleep %s must cover gateway preStop %s + delay %s + drain %s + 10s", sidecar, gwPreStop, delay, drain)
	require.LessOrEqual(t, sidecar+10*time.Second, time.Duration(grace)*time.Second,
		"audit-worker sleep %s + 10s must fit terminationGracePeriodSeconds=%d", sidecar, grace)
}
