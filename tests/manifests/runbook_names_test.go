package manifests

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunbooksNameRealObjects guards runbooks against naming Kubernetes
// objects that do not exist (W5, T-13-07). The audit worker is a sidecar
// container of statefulset/aegis-gateway; there is no aegis-audit-worker
// Deployment or ConfigMap.
func TestRunbooksNameRealObjects(t *testing.T) {
	dir := filepath.Join(repoRoot, "docs", "runbooks")
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		require.NotContains(t, string(b), "aegis-audit-worker", "%s names a nonexistent object", f)
	}

	b, err := os.ReadFile(filepath.Join(dir, "spool-saturation-recovery.md"))
	require.NoError(t, err)
	for _, want := range []string{"statefulset/aegis-gateway", "-c audit-worker", "AEGIS_AUDIT_BATCH_SIZE", "AEGIS_AUDIT_FLUSH_INTERVAL_MS"} {
		require.True(t, strings.Contains(string(b), want), "runbook must contain %q", want)
	}
}

// TestRunbooksNameRealMetrics fails when a runbook names an aegis_* metric that
// no Go source registers, and when it uses kubectl forms that cannot work for
// this deployment (Phase 13 review CR-02, WR-04).
func TestRunbooksNameRealMetrics(t *testing.T) {
	known := map[string]bool{}
	nameRe := regexp.MustCompile(`Name:\s*"(aegis_[a-z0-9_]+)"`)
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range nameRe.FindAllStringSubmatch(string(b), -1) {
			known[m[1]] = true
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, known, "no metric names found under internal/")

	// Every aegis_* token is treated as a metric name except these non-metric identifiers.
	notMetrics := map[string]bool{"aegis_session": true} // control-plane session cookie
	metricRe := regexp.MustCompile(`\b(aegis_[a-z0-9_]+)`)
	bad := []string{"statefulset/aegis-gateway-0", "aegis-wal-prune", "AegisWALSpoolSaturated"}
	files, err := filepath.Glob(filepath.Join(repoRoot, "docs", "runbooks", "*.md"))
	require.NoError(t, err)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		s := string(b)
		for _, m := range metricRe.FindAllStringSubmatch(s, -1) {
			n := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(m[1], "_bucket"), "_sum"), "_count")
			if notMetrics[n] {
				continue
			}
			ok := known[n]
			if strings.HasSuffix(n, "_") { // grep prefix such as aegis_keyset_
				for k := range known {
					ok = ok || strings.HasPrefix(k, n)
				}
			}
			assert.Truef(t, ok, "%s names metric %s, which nothing registers", filepath.Base(f), m[1])
		}
		for _, x := range bad {
			assert.NotContainsf(t, s, x, "%s uses %q, which does not exist", filepath.Base(f), x)
		}
	}
}
