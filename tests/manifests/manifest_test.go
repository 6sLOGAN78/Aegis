package manifests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func loadYAML(t *testing.T, relPath string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(relPath)
	require.NoError(t, err, "failed to read manifest file: %s", relPath)

	var doc map[string]interface{}
	err = yaml.Unmarshal(data, &doc)
	require.NoError(t, err, "failed to parse YAML from: %s", relPath)
	return doc
}

func TestGatewayManifestHardening(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "deployments", "kubernetes", "base", "gateway", "statefulset.yaml")
	doc := loadYAML(t, manifestPath)

	// Verify Kind and Replicas
	assert.Equal(t, "StatefulSet", doc["kind"])
	spec, ok := doc["spec"].(map[string]interface{})
	require.True(t, ok, "spec must be a map")
	assert.Equal(t, 3, spec["replicas"])

	// Verify Pod Security Context
	template, ok := spec["template"].(map[string]interface{})
	require.True(t, ok, "template must be a map")
	podSpec, ok := template["spec"].(map[string]interface{})
	require.True(t, ok, "podSpec must be a map")
	podSec, ok := podSpec["securityContext"].(map[string]interface{})
	require.True(t, ok, "pod securityContext must be a map")
	assert.Equal(t, true, podSec["runAsNonRoot"])
	assert.Equal(t, 10001, podSec["runAsUser"])
	assert.Equal(t, 10001, podSec["runAsGroup"])
	assert.Equal(t, 10001, podSec["fsGroup"])

	seccomp, ok := podSec["seccompProfile"].(map[string]interface{})
	require.True(t, ok, "seccompProfile must be a map")
	assert.Equal(t, "RuntimeDefault", seccomp["type"])

	// Verify Container Security Context
	containers, ok := podSpec["containers"].([]interface{})
	require.True(t, ok, "containers must be a slice")
	require.NotEmpty(t, containers)
	gatewayContainer, ok := containers[0].(map[string]interface{})
	require.True(t, ok)

	cSec, ok := gatewayContainer["securityContext"].(map[string]interface{})
	require.True(t, ok, "container securityContext must be a map")
	assert.Equal(t, false, cSec["allowPrivilegeEscalation"])
	assert.Equal(t, true, cSec["readOnlyRootFilesystem"])

	caps, ok := cSec["capabilities"].(map[string]interface{})
	require.True(t, ok, "capabilities must be a map")
	dropList, ok := caps["drop"].([]interface{})
	require.True(t, ok, "drop must be a list")
	assert.Contains(t, dropList, "ALL")

	// Verify volume mounts include /var/log/aegis/wal and /tmp
	volumeMounts, ok := gatewayContainer["volumeMounts"].([]interface{})
	require.True(t, ok, "volumeMounts must be a slice")
	mountPaths := make(map[string]string)
	for _, vm := range volumeMounts {
		vmMap := vm.(map[string]interface{})
		mountPaths[vmMap["mountPath"].(string)] = vmMap["name"].(string)
	}
	assert.Contains(t, mountPaths, "/var/log/aegis/wal")
	assert.Contains(t, mountPaths, "/tmp")

	// Verify volumeClaimTemplates requests storage 2Gi with ReadWriteOnce
	vctList, ok := spec["volumeClaimTemplates"].([]interface{})
	require.True(t, ok, "volumeClaimTemplates must be a slice")
	require.NotEmpty(t, vctList)
	vct := vctList[0].(map[string]interface{})
	vctMetadata := vct["metadata"].(map[string]interface{})
	assert.Equal(t, "wal-spool", vctMetadata["name"])
	vctSpec := vct["spec"].(map[string]interface{})
	accessModes := vctSpec["accessModes"].([]interface{})
	assert.Contains(t, accessModes, "ReadWriteOnce")
	resources := vctSpec["resources"].(map[string]interface{})
	requests := resources["requests"].(map[string]interface{})
	assert.Equal(t, "2Gi", requests["storage"])

	// Verify probes: startupProbe, livenessProbe, readinessProbe pointing to /livez and /readyz
	startupProbe, ok := gatewayContainer["startupProbe"].(map[string]interface{})
	require.True(t, ok, "startupProbe must be present")
	spHttpGet := startupProbe["httpGet"].(map[string]interface{})
	assert.Equal(t, "/livez", spHttpGet["path"])

	livenessProbe, ok := gatewayContainer["livenessProbe"].(map[string]interface{})
	require.True(t, ok, "livenessProbe must be present")
	lpHttpGet := livenessProbe["httpGet"].(map[string]interface{})
	assert.Equal(t, "/livez", lpHttpGet["path"])

	readinessProbe, ok := gatewayContainer["readinessProbe"].(map[string]interface{})
	require.True(t, ok, "readinessProbe must be present")
	rpHttpGet := readinessProbe["httpGet"].(map[string]interface{})
	assert.Equal(t, "/readyz", rpHttpGet["path"])
}

func TestNetworkPolicyIsolation(t *testing.T) {
	// 1. Gateway NetworkPolicy
	gwPolicyPath := filepath.Join("..", "..", "deployments", "kubernetes", "base", "gateway", "networkpolicy.yaml")
	gwDoc := loadYAML(t, gwPolicyPath)

	assert.Equal(t, "NetworkPolicy", gwDoc["kind"])
	spec := gwDoc["spec"].(map[string]interface{})

	// Validate policyTypes includes Ingress and Egress
	policyTypes := spec["policyTypes"].([]interface{})
	assert.Contains(t, policyTypes, "Ingress")
	assert.Contains(t, policyTypes, "Egress")

	// Validate egress rules
	egressRules, ok := spec["egress"].([]interface{})
	require.True(t, ok, "egress rules must be present")

	hasCoreDNS53UDP := false
	hasCoreDNS53TCP := false
	hasRedis6379 := false
	hasControlPlane9090 := false

	for _, r := range egressRules {
		ruleMap := r.(map[string]interface{})
		ports, _ := ruleMap["ports"].([]interface{})
		toList, _ := ruleMap["to"].([]interface{})

		for _, p := range ports {
			pMap := p.(map[string]interface{})
			portVal := pMap["port"]
			protoVal, _ := pMap["protocol"].(string)

			if portVal == 53 && protoVal == "UDP" {
				hasCoreDNS53UDP = true
			}
			if portVal == 53 && protoVal == "TCP" {
				hasCoreDNS53TCP = true
			}
			if portVal == 6379 {
				hasRedis6379 = true
			}
			if portVal == 9090 {
				hasControlPlane9090 = true
			}
		}

		for _, to := range toList {
			toMap := to.(map[string]interface{})
			if podSel, ok := toMap["podSelector"].(map[string]interface{}); ok {
				if matchLabels, ok := podSel["matchLabels"].(map[string]interface{}); ok {
					if matchLabels["app"] == "redis" {
						hasRedis6379 = true
					}
					if matchLabels["app"] == "aegis-control-plane" {
						hasControlPlane9090 = true
					}
				}
			}
		}
	}

	assert.True(t, hasCoreDNS53UDP, "Egress must explicitly permit CoreDNS port 53 UDP")
	assert.True(t, hasCoreDNS53TCP, "Egress must explicitly permit CoreDNS port 53 TCP")
	assert.True(t, hasRedis6379, "Egress must permit Redis port 6379")
	assert.True(t, hasControlPlane9090, "Egress must permit Control Plane port 9090")

	// 2. Backend NetworkPolicy in aegis-apps
	bePolicyPath := filepath.Join("..", "..", "deployments", "kubernetes", "base", "backends", "networkpolicy.yaml")
	beDoc := loadYAML(t, bePolicyPath)
	beSpec := beDoc["spec"].(map[string]interface{})
	beIngress := beSpec["ingress"].([]interface{})
	require.NotEmpty(t, beIngress)

	// Validates backend NetworkPolicy in aegis-apps permits ingress ONLY from pods matching app: aegis-gateway in namespace aegis-system
	onlyGatewayIngress := true
	foundGatewayIngress := false
	for _, ing := range beIngress {
		ingMap := ing.(map[string]interface{})
		fromList, ok := ingMap["from"].([]interface{})
		require.True(t, ok)
		for _, from := range fromList {
			fromMap := from.(map[string]interface{})
			nsSel, nsOk := fromMap["namespaceSelector"].(map[string]interface{})
			podSel, podOk := fromMap["podSelector"].(map[string]interface{})
			if nsOk && podOk {
				nsLabels := nsSel["matchLabels"].(map[string]interface{})
				podLabels := podSel["matchLabels"].(map[string]interface{})
				if nsLabels["kubernetes.io/metadata.name"] == "aegis-system" && podLabels["app"] == "aegis-gateway" {
					foundGatewayIngress = true
				} else {
					onlyGatewayIngress = false
				}
			} else {
				onlyGatewayIngress = false
			}
		}
	}
	assert.True(t, foundGatewayIngress, "Backend NetworkPolicy must permit ingress from aegis-gateway in aegis-system")
	assert.True(t, onlyGatewayIngress, "Backend NetworkPolicy must permit ingress ONLY from aegis-gateway in aegis-system")
}

func TestPodDisruptionBudget(t *testing.T) {
	pdbPath := filepath.Join("..", "..", "deployments", "kubernetes", "base", "gateway", "pdb.yaml")
	pdbDoc := loadYAML(t, pdbPath)

	assert.Equal(t, "PodDisruptionBudget", pdbDoc["kind"])
	spec := pdbDoc["spec"].(map[string]interface{})
	assert.Equal(t, 2, spec["minAvailable"])

	selector := spec["selector"].(map[string]interface{})
	matchLabels := selector["matchLabels"].(map[string]interface{})
	assert.Equal(t, "aegis-gateway", matchLabels["app"])
}

func TestProductionOverlayKustomization(t *testing.T) {
	kustPath := filepath.Join("..", "..", "deployments", "kubernetes", "overlays", "production", "kustomization.yaml")
	kustDoc := loadYAML(t, kustPath)

	resources := kustDoc["resources"].([]interface{})
	assert.Contains(t, resources, "../../base")

	// Validate production gateway patch
	patchPath := filepath.Join("..", "..", "deployments", "kubernetes", "overlays", "production", "gateway-prod.yaml")
	patchDoc := loadYAML(t, patchPath)

	spec := patchDoc["spec"].(map[string]interface{})
	template := spec["template"].(map[string]interface{})
	podSpec := template["spec"].(map[string]interface{})
	tscList, ok := podSpec["topologySpreadConstraints"].([]interface{})
	require.True(t, ok, "topologySpreadConstraints must be defined in production overlay")

	hasZoneSpread := false
	hasHostnameSpread := false
	for _, tsc := range tscList {
		tscMap := tsc.(map[string]interface{})
		topKey := tscMap["topologyKey"]
		if topKey == "topology.kubernetes.io/zone" {
			hasZoneSpread = true
		}
		if topKey == "kubernetes.io/hostname" {
			hasHostnameSpread = true
		}
	}
	assert.True(t, hasZoneSpread, "Production overlay must spread across topology.kubernetes.io/zone")
	assert.True(t, hasHostnameSpread, "Production overlay must spread across kubernetes.io/hostname")
}
