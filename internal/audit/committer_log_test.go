package audit

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// WR-01: a burst of shed completions must never suppress the fault / write-error lines.
func TestCommitterShedLogDoesNotStarveFaultLog(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	c := &Committer{}
	for i := 0; i < 1000; i++ {
		c.logShed()
	}
	c.logf("AuditCommitter warning: audit write fault set (%s); refusing allowed traffic", "write_error")
	c.logShed()

	out := buf.String()
	assert.Contains(t, out, "audit write fault set (write_error)", "fault line must not be rate-limited by the shed log")
	assert.Equal(t, 1, strings.Count(out, "completion(s) shed"), "shed lines are still rate-limited")
	assert.Contains(t, out, "1 completion(s) shed since last report")
}
