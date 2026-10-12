package compose

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hermetic lint of scripts/compose-smoke.sh. None of these tests need Docker: they
// guard the script's syntax and the audit evidence it claims to assert (B7, AUD-03,
// AUD-04) so the sections and honest wording cannot regress unnoticed.

func readSmokeScript(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join("..", "..", "scripts", "compose-smoke.sh")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)
	return path, string(data)
}

func TestSmokeScriptSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	path, _ := readSmokeScript(t)
	out, err := exec.Command(bash, "-n", path).CombinedOutput()
	require.NoError(t, err, "bash -n failed: %s", out)
}

func TestSmokeScriptCoversAuditEvidence(t *testing.T) {
	_, script := readSmokeScript(t)
	required := []string{
		"=== AUDIT-TYPES",
		"=== DEDUPE",
		"=== ROTATION",
		"gw_probe",
		"completion|allow|200|1",
		"decision|allow|0|0",
		"denial|deny|403|1|1|FORBIDDEN",
		"denial|deny|401|anonymous",
		"suppressed_count=1",
		"SMOKE_ROTATION_REQUESTS",
		"SMOKE_SUPPRESS_WINDOW_SECS",
		"ROTATION SKIPPED",
		"AUDIT-TYPES SKIPPED",
		"previous suppression window",
	}
	for _, want := range required {
		assert.Contains(t, script, want, "smoke script must contain %q", want)
	}
}

func TestSmokeScriptHonestHeader(t *testing.T) {
	_, script := readSmokeScript(t)
	assert.NotContains(t, script, "It does NOT establish REV-03 or AUD-03", "stale header sentence")
	assert.NotContains(t, script, "not AUD-03", "stale AUDIT PASS suffix")
	assert.Contains(t, script, "REV-03", "REV-03 must stay documented as open")
	assert.Contains(t, script, "TestAuditWorker_RestartRecovery", "crash-between-insert-and-cursor-save must be named as unit-level only")

	traps := 0
	for _, line := range strings.Split(script, "\n") {
		if regexp.MustCompile(`^trap cleanup EXIT`).MatchString(line) {
			traps++
		}
	}
	assert.Equal(t, 1, traps, "exactly one trap cleanup EXIT line")
}

func TestScriptsSendIdempotencyKeys(t *testing.T) {
	_, smoke := readSmokeScript(t)
	seedData, err := os.ReadFile(filepath.Join("..", "..", "deployments", "compose", "seed", "seed.sh"))
	require.NoError(t, err)
	seed := string(seedData)

	assert.GreaterOrEqual(t, strings.Count(smoke, "Idempotency-Key"), 2, "compose-smoke.sh must key both curl branches")
	assert.GreaterOrEqual(t, strings.Count(seed, "Idempotency-Key"), 2, "seed.sh must key both curl branches")
	assert.Contains(t, seed, "kernel/random/uuid", "seed.sh must mint a fresh key per cp_call")
	assert.Contains(t, smoke, "=== GATEWAYS", "smoke must assert gateways stay healthy after idle")
	assert.Contains(t, smoke, "sleep 65")
	assert.Contains(t, smoke, "partitioned")
}

func TestSmokeScriptNoCredentialEcho(t *testing.T) {
	_, script := readSmokeScript(t)
	printing := regexp.MustCompile(`^\s*(echo|printf)\b`)
	secret := regexp.MustCompile(`SMOKE_PASSWORD|SMOKE_DEV_PASSWORD|CSRF|SESSION`)
	for i, line := range strings.Split(script, "\n") {
		if printing.MatchString(line) && secret.MatchString(line) {
			t.Errorf("line %d prints a credential variable: %s", i+1, line)
		}
	}
}

func TestSmokeScriptCoversJTIAndWorkloadQuarantine(t *testing.T) {
	_, script := readSmokeScript(t)
	required := []string{
		"=== REV-03 JTI",
		"=== WORKLOAD QUARANTINE",
		"=== DISK",
		"TOKEN_REVOKED",
		"PRINCIPAL_QUARANTINED",
		"workload-orders",
		"@uri",
		"denied after",
		"stat -f",
		"DISK SKIPPED",
		"/control/v1/revocations",
		"principal_id",
		"jti",
		"grep '^gateway'",
	}
	for _, want := range required {
		assert.Contains(t, script, want, "smoke script must contain %q", want)
	}
	assert.NotContains(t, script, `"role"`, "login bodies must be {username,password}, not role")
}

func TestSmokeScriptNoTokenValueEcho(t *testing.T) {
	_, script := readSmokeScript(t)
	printing := regexp.MustCompile(`^\s*(echo|printf)\b`)
	// Expansions of token variables; bare "access_token" reads are fine.
	secret := regexp.MustCompile(`\$\{?(tok|token|rtok|fresh)\b`)
	for i, line := range strings.Split(script, "\n") {
		if printing.MatchString(line) && secret.MatchString(line) {
			t.Errorf("line %d prints a token variable: %s", i+1, line)
		}
	}
}

func TestSmokeScriptCoversDRKeyrotAndMTLS(t *testing.T) {
	_, script := readSmokeScript(t)
	required := []string{
		"=== DR", "=== KEYROT-JWT", "=== KEYROT-SNAPSHOT", "=== MTLS",
		"FLUSHALL", "SECURITY_STATE_MISSING", "PRINCIPAL_QUARANTINED", "TOKEN_REVOKED",
		"REPLICA_POLL_LIMIT", "-keypair", "AEGIS_KEYS_DIR",
	}
	for _, want := range required {
		assert.Contains(t, script, want, "smoke script must contain %q", want)
	}
	after := strings.Index(script, "=== WORKLOAD QUARANTINE")
	before := strings.Index(script, "=== GATEWAYS")
	require.True(t, after >= 0 && before > after, "WORKLOAD QUARANTINE must precede GATEWAYS")
	for _, sec := range []string{"=== DR", "=== KEYROT-JWT", "=== KEYROT-SNAPSHOT", "=== MTLS"} {
		idx := strings.Index(script, sec)
		require.True(t, idx >= 0, "missing %s", sec)
		assert.Greater(t, idx, after, "%s must come after WORKLOAD QUARANTINE", sec)
		assert.Less(t, idx, before, "%s must come before GATEWAYS idle health", sec)
	}
}

func TestSmokeScriptHeaderDocumentsPhase11Sections(t *testing.T) {
	_, script := readSmokeScript(t)
	header := script
	if i := strings.Index(script, "set -euo pipefail"); i > 0 {
		header = script[:i]
	}
	for _, want := range []string{"DR", "KEYROT", "MTLS", "REV-03", "AUTH-01", "CTRL-02", "CTRL-05", "REPLICA_POLL_LIMIT", "AEGIS_KEYS_DIR"} {
		assert.Contains(t, header, want, "header must document %q", want)
	}
	lower := strings.ToLower(header)
	assert.NotContains(t, lower, "kubernetes live bring-up is covered")
	assert.NotContains(t, lower, "kubernetes is proven")
}

func TestSmokeScriptDRRecoveryLimit60(t *testing.T) {
	_, script := readSmokeScript(t)
	assert.Contains(t, script, `REPLICA_POLL_LIMIT="${REPLICA_POLL_LIMIT:-5}"`, "default stays 5")
	assert.Contains(t, script, "REPLICA_POLL_LIMIT=60", "DR section raises the limit to 60")
}

func TestSmokeScriptRestoresKeys(t *testing.T) {
	_, script := readSmokeScript(t)
	assert.Contains(t, script, "trap")
	assert.Contains(t, script, "KEYS_BACKUP")
}

func TestSmokeScriptNoKeyFileCat(t *testing.T) {
	_, script := readSmokeScript(t)
	// Writing a key with `cat new > file` is allowed; printing one to stdout is not.
	catKey := regexp.MustCompile(`\bcat\s+[^|>]*\.key\b`)
	for i, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if catKey.MatchString(line) && !strings.Contains(line, ">") {
			t.Errorf("line %d prints a private key file: %s", i+1, line)
		}
	}
}
