package manifests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
