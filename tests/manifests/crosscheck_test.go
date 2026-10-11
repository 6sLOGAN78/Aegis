package manifests

// Source-derived cross-checks between the Kubernetes manifests and the Go
// binaries they deploy (Phase 11, D-19..D-23). Nothing here needs a cluster.

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type obj = map[string]interface{}

const (
	repoRoot = "../.."
	k8sRoot  = "../../deployments/kubernetes"
)

// ---------------------------------------------------------------- helpers

type manifestDoc struct {
	Path string
	Body obj
}

func (d manifestDoc) kind() string { return str(d.Body["kind"]) }
func (d manifestDoc) name() string { return str(dig(d.Body, "metadata", "name")) }

func str(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

func dig(v interface{}, keys ...string) interface{} {
	cur := v
	for _, k := range keys {
		m, ok := cur.(obj)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

func list(v interface{}) []obj {
	var out []obj
	if l, ok := v.([]interface{}); ok {
		for _, e := range l {
			if m, ok := e.(obj); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// loadDocs reads every multi-document YAML file under dir.
func loadDocs(t *testing.T, dir string) []manifestDoc {
	t.Helper()
	var docs []manifestDoc
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var m obj
			if err := dec.Decode(&m); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return err
			}
			if m != nil {
				docs = append(docs, manifestDoc{Path: p, Body: m})
			}
		}
		return nil
	})
	require.NoError(t, err)
	return docs
}

func baseDocs(t *testing.T) []manifestDoc { return loadDocs(t, filepath.Join(k8sRoot, "base")) }

func findDoc(t *testing.T, docs []manifestDoc, kind, name string) manifestDoc {
	t.Helper()
	for _, d := range docs {
		if d.kind() == kind && d.name() == name {
			return d
		}
	}
	t.Fatalf("no %s named %s", kind, name)
	return manifestDoc{}
}

func podSpec(d manifestDoc) obj {
	m, _ := dig(d.Body, "spec", "template", "spec").(obj)
	return m
}

func containerByName(t *testing.T, d manifestDoc, name string) obj {
	t.Helper()
	for _, c := range list(podSpec(d)["containers"]) {
		if str(c["name"]) == name {
			return c
		}
	}
	t.Fatalf("%s has no container %s", d.name(), name)
	return nil
}

var (
	aegisKeyRe = regexp.MustCompile(`"(AEGIS_[A-Z0-9_]+)"`)
	getenvRe   = regexp.MustCompile(`os\.Getenv\("([A-Z0-9_]+)"\)`)
)

func goFiles(t *testing.T, paths ...string) []string {
	t.Helper()
	var out []string
	for _, p := range paths {
		full := filepath.Join(repoRoot, p)
		st, err := os.Stat(full)
		require.NoError(t, err, p)
		if !st.IsDir() {
			out = append(out, full)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(full, "*.go"))
		require.NoError(t, err)
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				out = append(out, m)
			}
		}
	}
	return out
}

func sourceKeys(t *testing.T, re *regexp.Regexp, paths ...string) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for _, f := range goFiles(t, paths...) {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllSubmatch(data, -1) {
			keys[string(m[1])] = true
		}
	}
	require.NotEmpty(t, keys)
	return keys
}

func gatewayKeys(t *testing.T) map[string]bool {
	return sourceKeys(t, aegisKeyRe, "cmd/gateway", "internal/config/config.go", "internal/proxy/router.go")
}
func controlPlaneKeys(t *testing.T) map[string]bool {
	return sourceKeys(t, aegisKeyRe, "cmd/control-plane", "internal/control")
}
func auditWorkerKeys(t *testing.T) map[string]bool {
	return sourceKeys(t, aegisKeyRe, "cmd/audit-worker")
}
func backendKeys(t *testing.T, svc string) map[string]bool {
	return sourceKeys(t, getenvRe, "cmd/services/"+svc)
}

// configMapData returns the data keys of the ConfigMap named in a configMapRef.
func configMapData(t *testing.T, docs []manifestDoc, name string) obj {
	cm := findDoc(t, docs, "ConfigMap", name)
	data, _ := cm.Body["data"].(obj)
	return data
}

// containerEnvNames returns the env names a container receives: explicit env
// entries plus every key of each envFrom ConfigMap.
func containerEnvNames(t *testing.T, docs []manifestDoc, c obj) []string {
	t.Helper()
	var names []string
	for _, e := range list(c["env"]) {
		names = append(names, str(e["name"]))
	}
	for _, ef := range list(c["envFrom"]) {
		ref := str(dig(ef, "configMapRef", "name"))
		if ref == "" {
			continue
		}
		for k := range configMapData(t, docs, ref) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

func assertEnvKnown(t *testing.T, who string, names []string, allowed map[string]bool, helpers ...string) {
	t.Helper()
	for _, n := range names {
		ok := allowed[n]
		for _, h := range helpers {
			if n == h {
				ok = true
			}
		}
		assert.True(t, ok, "%s sets %s but the binary never reads it", who, n)
	}
}

func envValue(c obj, name string) string {
	for _, e := range list(c["env"]) {
		if str(e["name"]) == name {
			return str(e["value"])
		}
	}
	return ""
}

func envIndex(c obj, name string) int {
	for i, e := range list(c["env"]) {
		if str(e["name"]) == name {
			return i
		}
	}
	return -1
}

func portNumbers(c obj) map[int]bool {
	out := map[int]bool{}
	for _, p := range list(c["ports"]) {
		if n, ok := p["containerPort"].(int); ok {
			out[n] = true
		}
	}
	return out
}

// ------------------------------------------------------------------ tests

func TestManifestEnvKeysAreReadBySource(t *testing.T) {
	docs := baseDocs(t)
	gw := findDoc(t, docs, "StatefulSet", "aegis-gateway")

	assertEnvKnown(t, "gateway", containerEnvNames(t, docs, containerByName(t, gw, "gateway")), gatewayKeys(t), "POD_NAME")
	assertEnvKnown(t, "gateway audit-worker sidecar", containerEnvNames(t, docs, containerByName(t, gw, "audit-worker")), auditWorkerKeys(t))

	cp := findDoc(t, docs, "Deployment", "aegis-control-plane")
	assertEnvKnown(t, "control-plane", containerEnvNames(t, docs, containerByName(t, cp, "control-plane")), controlPlaneKeys(t))

	// The dead keys of the pre-Phase-11 ConfigMap must never come back.
	for _, d := range docs {
		raw, err := os.ReadFile(d.Path)
		require.NoError(t, err)
		for _, dead := range []string{"AEGIS_HTTP_ADDR", "AEGIS_WORKLOAD_ADDR", "AEGIS_METRICS_ADDR"} {
			assert.NotContains(t, string(raw), dead, d.Path)
		}
	}
}

func TestManifestGatewayPortsMatchBinary(t *testing.T) {
	docs := baseDocs(t)
	keys := gatewayKeys(t)
	for _, k := range []string{"AEGIS_PORT", "AEGIS_WORKLOAD_PORT", "AEGIS_METRICS_PORT"} {
		require.True(t, keys[k], "gateway source no longer reads %s", k)
	}
	cfg := configMapData(t, docs, "aegis-gateway-config")
	user, _ := strconv.Atoi(str(cfg["AEGIS_PORT"]))
	workload, _ := strconv.Atoi(str(cfg["AEGIS_WORKLOAD_PORT"]))
	metrics, _ := strconv.Atoi(str(cfg["AEGIS_METRICS_PORT"]))
	require.Equal(t, 8080, user)
	require.Equal(t, 8443, workload, "AEGIS_WORKLOAD_PORT must equal the declared 8443 (binary default is 9443)")
	require.Equal(t, 9091, metrics)

	gw := findDoc(t, docs, "StatefulSet", "aegis-gateway")
	c := containerByName(t, gw, "gateway")
	assert.Equal(t, map[int]bool{user: true, workload: true, metrics: true}, portNumbers(c))

	for _, svcName := range []string{"aegis-gateway", "aegis-gateway-lb"} {
		svc := findDoc(t, docs, "Service", svcName)
		got := map[int]bool{}
		for _, p := range list(dig(svc.Body, "spec", "ports")) {
			assert.Equal(t, p["port"], p["targetPort"], svcName)
			got[p["port"].(int)] = true
		}
		assert.Equal(t, map[int]bool{user: true, workload: true, metrics: true}, got, svcName)
	}

	np := findDoc(t, docs, "NetworkPolicy", "aegis-gateway-networkpolicy")
	ing := map[int]bool{}
	for _, r := range list(dig(np.Body, "spec", "ingress")) {
		for _, p := range list(r["ports"]) {
			ing[p["port"].(int)] = true
		}
	}
	assert.True(t, ing[user], "NetworkPolicy ingress must allow %d", user)
	assert.True(t, ing[workload], "NetworkPolicy ingress must allow %d", workload)

	for probe, path := range map[string]string{"startupProbe": "/livez", "livenessProbe": "/livez", "readinessProbe": "/readyz"} {
		hg, _ := dig(c[probe], "httpGet").(obj)
		require.NotNil(t, hg, probe)
		assert.Equal(t, path, hg["path"], probe)
		assert.Equal(t, user, hg["port"], probe)
	}
}

func TestManifestAuditSpoolPVCBacked(t *testing.T) {
	docs := baseDocs(t)
	for _, d := range docs {
		if d.kind() == "Deployment" {
			assert.NotEqual(t, "aegis-audit-worker", d.name(), "standalone audit-worker Deployment must be gone")
		}
		ps := podSpec(d)
		if ps == nil {
			continue
		}
		emptyDirs := map[string]bool{}
		for _, v := range list(ps["volumes"]) {
			if _, ok := v["emptyDir"]; ok {
				emptyDirs[str(v["name"])] = true
			}
		}
		for _, c := range list(ps["containers"]) {
			for _, vm := range list(c["volumeMounts"]) {
				if str(vm["mountPath"]) == "/var/log/aegis/wal" {
					assert.False(t, emptyDirs[str(vm["name"])], "%s mounts emptyDir at the WAL path", d.name())
				}
			}
		}
	}

	gw := findDoc(t, docs, "StatefulSet", "aegis-gateway")
	cs := list(podSpec(gw)["containers"])
	require.GreaterOrEqual(t, len(cs), 2)
	assert.Equal(t, "gateway", str(cs[0]["name"]))
	idx := -1
	for i, c := range cs {
		if str(c["name"]) == "audit-worker" {
			idx = i
		}
	}
	require.Greater(t, idx, 0, "audit-worker must be a sidecar after the gateway container")

	var vct []string
	for _, v := range list(dig(gw.Body, "spec", "volumeClaimTemplates")) {
		vct = append(vct, str(dig(v, "metadata", "name")))
	}
	var mountedVol string
	for _, vm := range list(cs[idx]["volumeMounts"]) {
		if str(vm["mountPath"]) == "/var/log/aegis/wal" {
			mountedVol = str(vm["name"])
		}
	}
	assert.Equal(t, "wal-spool", mountedVol)
	assert.Contains(t, vct, mountedVol)
	assert.Equal(t, "/var/log/aegis/wal", envValue(cs[idx], "AEGIS_SPOOL_DIR"))
}

func TestManifestGatewayPerPodCertSelection(t *testing.T) {
	docs := baseDocs(t)
	gw := findDoc(t, docs, "StatefulSet", "aegis-gateway")
	c := containerByName(t, gw, "gateway")

	pod := envIndex(c, "POD_NAME")
	cert := envIndex(c, "AEGIS_CP_CLIENT_CERT_PATH")
	key := envIndex(c, "AEGIS_CP_CLIENT_KEY_PATH")
	require.GreaterOrEqual(t, pod, 0, "POD_NAME env missing")
	require.Greater(t, cert, pod, "POD_NAME must be declared before the cert path")
	require.Greater(t, key, pod, "POD_NAME must be declared before the key path")

	e := list(c["env"])[pod]
	assert.Equal(t, "metadata.name", str(dig(e, "valueFrom", "fieldRef", "fieldPath")))

	mount := ""
	for _, vm := range list(c["volumeMounts"]) {
		for _, v := range list(podSpec(gw)["volumes"]) {
			if str(v["name"]) == str(vm["name"]) && str(dig(v, "secret", "secretName")) == "aegis-gateway-cp-clients" {
				mount = str(vm["mountPath"])
			}
		}
	}
	require.NotEmpty(t, mount, "aegis-gateway-cp-clients Secret must be mounted")
	for _, name := range []string{"AEGIS_CP_CLIENT_CERT_PATH", "AEGIS_CP_CLIENT_KEY_PATH"} {
		v := envValue(c, name)
		assert.Contains(t, v, "$(POD_NAME)")
		assert.True(t, strings.HasPrefix(v, mount+"/"), "%s=%s not under %s", name, v, mount)
	}
}

// ---------------------------------------------------- control plane / backends

// mountsBySource maps "secret:<name>" / "configmap:<name>" to the mountPath
// at which the container mounts that source.
func mountsBySource(d manifestDoc, c obj) map[string]string {
	out := map[string]string{}
	for _, vm := range list(c["volumeMounts"]) {
		for _, v := range list(podSpec(d)["volumes"]) {
			if str(v["name"]) != str(vm["name"]) {
				continue
			}
			if s := str(dig(v, "secret", "secretName")); s != "" {
				out["secret:"+s] = str(vm["mountPath"])
			}
			if s := str(dig(v, "configMap", "name")); s != "" {
				out["configmap:"+s] = str(vm["mountPath"])
			}
		}
	}
	return out
}

func underMount(t *testing.T, mounts map[string]string, src, path string) {
	t.Helper()
	mp, ok := mounts[src]
	require.True(t, ok, "%s is not mounted", src)
	assert.True(t, strings.HasPrefix(path, mp+"/"), "%s is not under %s mount %s", path, src, mp)
}

func TestManifestControlPlaneEnvAndProbes(t *testing.T) {
	docs := baseDocs(t)
	cp := findDoc(t, docs, "Deployment", "aegis-control-plane")
	c := containerByName(t, cp, "control-plane")
	cfg := configMapData(t, docs, "aegis-control-plane-config")

	assertEnvKnown(t, "control-plane", containerEnvNames(t, docs, c), controlPlaneKeys(t))

	// Default listener ports come from the binary source.
	src, err := os.ReadFile(filepath.Join(repoRoot, "cmd/control-plane/main.go"))
	require.NoError(t, err)
	for _, def := range []string{`":8084"`, `":9090"`, `":9092"`} {
		require.Contains(t, string(src), def, "control-plane default port changed")
	}
	assert.Equal(t, map[int]bool{8084: true, 9090: true, 9092: true}, portNumbers(c))

	api, err := os.ReadFile(filepath.Join(repoRoot, "internal/control/api.go"))
	require.NoError(t, err)
	require.Contains(t, string(api), `"/livez"`)
	require.Contains(t, string(api), `"/readyz"`)
	for probe, path := range map[string]string{"startupProbe": "/livez", "livenessProbe": "/livez", "readinessProbe": "/readyz"} {
		hg, _ := dig(c[probe], "httpGet").(obj)
		require.NotNil(t, hg, probe)
		assert.Equal(t, path, hg["path"], probe)
		assert.Equal(t, 8084, hg["port"], probe)
	}

	mounts := mountsBySource(cp, c)
	underMount(t, mounts, "secret:aegis-control-plane-tls", str(cfg["AEGIS_GRPC_TLS_CERT_PATH"]))
	underMount(t, mounts, "secret:aegis-control-plane-tls", str(cfg["AEGIS_GRPC_TLS_KEY_PATH"]))
	underMount(t, mounts, "configmap:aegis-root-ca", str(cfg["AEGIS_GRPC_CLIENT_CA_PATH"]))
	underMount(t, mounts, "secret:aegis-control-plane-signing-key", str(cfg["AEGIS_SIGNING_KEY_PATH"]))

	for _, name := range []string{"AEGIS_DB_USER", "AEGIS_DB_PASSWORD"} {
		var found bool
		for _, e := range list(c["env"]) {
			if str(e["name"]) == name {
				found = true
				assert.Equal(t, "aegis-db-credentials", str(dig(e, "valueFrom", "secretKeyRef", "name")))
				assert.Empty(t, str(e["value"]), "%s must not be inline", name)
			}
		}
		assert.True(t, found, name)
	}
}

func TestManifestBackendsMTLSWiring(t *testing.T) {
	docs := baseDocs(t)
	ports := map[string]int{"orders": 8081, "payments": 8082, "admin": 8083}
	for svc, port := range ports {
		d := findDoc(t, docs, "Deployment", svc)
		c := containerByName(t, d, svc)
		assertEnvKnown(t, svc, containerEnvNames(t, docs, c), backendKeys(t, svc))
		assert.Equal(t, strconv.Itoa(port), envValue(c, "PORT"))
		assert.Equal(t, map[int]bool{port: true}, portNumbers(c))

		mounts := mountsBySource(d, c)
		underMount(t, mounts, "secret:aegis-backend-"+svc+"-tls", envValue(c, "TLS_CERT_PATH"))
		underMount(t, mounts, "secret:aegis-backend-"+svc+"-tls", envValue(c, "TLS_KEY_PATH"))
		underMount(t, mounts, "configmap:aegis-root-ca", envValue(c, "CA_CERT_PATH"))
		underMount(t, mounts, "configmap:aegis-assertion-pubkey", envValue(c, "GATEWAY_ASSERTION_PUBKEY_PATH"))

		for _, probe := range []string{"startupProbe", "livenessProbe", "readinessProbe"} {
			ts, _ := dig(c[probe], "tcpSocket").(obj)
			require.NotNil(t, ts, "%s %s must be tcpSocket (mTLS forbids httpGet)", svc, probe)
			assert.Equal(t, port, ts["port"])
			assert.Nil(t, dig(c[probe], "httpGet"))
		}
	}
	// D-13: the backend NetworkPolicy (pinned gateway identity) is still present.
	findDoc(t, docs, "NetworkPolicy", "aegis-backends-networkpolicy")
}

func TestManifestOverlayPatchTargetsExist(t *testing.T) {
	base := baseDocs(t)
	targets := map[string][2]string{
		"gateway-prod.yaml":       {"StatefulSet", "gateway"},
		"control-plane-prod.yaml": {"Deployment", "control-plane"},
	}
	for file, kc := range targets {
		raw, err := os.ReadFile(filepath.Join(k8sRoot, "overlays", "production", file))
		require.NoError(t, err)
		var patch obj
		require.NoError(t, yaml.Unmarshal(raw, &patch))
		pd := manifestDoc{Path: file, Body: patch}
		require.Equal(t, kc[0], pd.kind())
		baseDoc := findDoc(t, base, kc[0], pd.name())
		for _, pc := range list(podSpec(pd)["containers"]) {
			containerByName(t, baseDoc, str(pc["name"]))
			assert.Equal(t, kc[1], str(pc["name"]))
		}
	}
}
