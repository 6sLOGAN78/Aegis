package manifests

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const dbSecretName = "aegis-db-credentials"

// TestManifestDBCredentialsFromSecret enforces that Postgres and every DB
// client take their credentials from one Secret (aegis-db-credentials) and
// that no *PASSWORD* env var carries a literal value (gap N2).
func TestManifestDBCredentialsFromSecret(t *testing.T) {
	// want maps a credential env var to the Secret key it must reference.
	want := map[string]string{
		"AEGIS_DB_PASSWORD": "AEGIS_DB_PASSWORD",
		"POSTGRES_PASSWORD": "AEGIS_DB_PASSWORD",
		"AEGIS_DB_USER":     "AEGIS_DB_USER",
		"POSTGRES_USER":     "AEGIS_DB_USER",
	}
	// seen records "container/ENV" pairs observed in base docs (vacuity guard).
	seen := map[string]bool{}
	baseDir := filepath.Join(k8sRoot, "base") + string(filepath.Separator)

	for _, d := range loadDocs(t, k8sRoot) {
		ps := podSpec(d)
		if ps == nil {
			continue
		}
		isBase := strings.HasPrefix(d.Path, baseDir)
		var containers []obj
		containers = append(containers, list(ps["containers"])...)
		containers = append(containers, list(ps["initContainers"])...)
		for _, c := range containers {
			cname := str(c["name"])
			for _, e := range list(c["env"]) {
				name := str(e["name"])
				if strings.Contains(name, "PASSWORD") {
					_, literal := e["value"]
					assert.Falsef(t, literal, "%s: container %s env %s has a literal value; use secretKeyRef", d.Path, cname, name)
				}
				key, ok := want[name]
				if !ok {
					continue
				}
				if isBase {
					seen[cname+"/"+name] = true
				}
				ref := dig(e, "valueFrom", "secretKeyRef")
				assert.NotNilf(t, ref, "%s: container %s env %s must use valueFrom.secretKeyRef", d.Path, cname, name)
				assert.Equalf(t, dbSecretName, str(dig(ref, "name")), "%s: container %s env %s secret name", d.Path, cname, name)
				assert.Equalf(t, key, str(dig(ref, "key")), "%s: container %s env %s secret key", d.Path, cname, name)
			}
		}
	}

	for _, s := range []string{
		"postgres/POSTGRES_PASSWORD",
		"postgres/POSTGRES_USER",
		"control-plane/AEGIS_DB_PASSWORD",
		"control-plane/AEGIS_DB_USER",
		"audit-worker/AEGIS_DB_PASSWORD",
		"audit-worker/AEGIS_DB_USER",
	} {
		assert.Truef(t, seen[s], "vacuity guard: never saw %s in base manifests", s)
	}
}

// TestManifestAuditWorkerPrunesDelivered keeps the K8s audit-worker sidecar in
// line with compose: without AEGIS_PRUNE_ARCHIVED=true the worker never deletes
// delivered WAL segments, the PVC fills to the 0.90 gate and the gateway
// answers 503 SPOOL_SATURATED (Phase 13 review CR-01).
func TestManifestAuditWorkerPrunesDelivered(t *testing.T) {
	sts := findDoc(t, baseDocs(t), "StatefulSet", "aegis-gateway")
	c := containerByName(t, sts, "audit-worker")
	assert.Equal(t, "true", envValue(c, "AEGIS_PRUNE_ARCHIVED"),
		"audit-worker sidecar must set AEGIS_PRUNE_ARCHIVED=true or the WAL PVC never shrinks")
}
