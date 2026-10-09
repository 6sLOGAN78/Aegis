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

func TestSmokeScriptNoCredentialEcho(t *testing.T) {
	_, script := readSmokeScript(t)
	printing := regexp.MustCompile(`^\s*(echo|printf)\b`)
	secret := regexp.MustCompile(`SMOKE_PASSWORD|CSRF|SESSION`)
	for i, line := range strings.Split(script, "\n") {
		if printing.MatchString(line) && secret.MatchString(line) {
			t.Errorf("line %d prints a credential variable: %s", i+1, line)
		}
	}
}
