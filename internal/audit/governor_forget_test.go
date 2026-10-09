package audit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WR-01 at pipeline level: a denial whose durable write fails must not open a
// suppression window that masks its repeats.
func TestPipelineFailedFirstDenialDoesNotMaskRepeats(t *testing.T) {
	spool, rc := newTestSpoolControlled(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: 60 * time.Second},
		Committer: pipelineFast(),
	}, clk)

	rc.Set(0.96) // hard limit: WriteFrames refuses, the first denial is lost
	first := pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429)
	p.RecordDenial(first)
	require.Empty(t, readAllEvents(t, spool.cfg.SpoolDir))
	require.Equal(t, 1, rec.dropped("denial", "hard_limit"))

	rc.Set(0.10) // the disk recovered
	second := pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429)
	p.RecordDenial(second)

	evs := readAllEvents(t, spool.cfg.SpoolDir)
	require.Len(t, evs, 1, "the repeat must be recorded, not suppressed behind a row that was never written")
	assert.Equal(t, second.RequestID, evs[0].RequestID)
	assert.Equal(t, 0, rec.suppressed("RATE_LIMIT_EXCEEDED"))

	// Once recorded, normal suppression resumes and the summary points at the written row.
	p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	assert.Equal(t, 1, rec.suppressed("RATE_LIMIT_EXCEEDED"))
	p.sweepOnce(pipelineT0.Add(61 * time.Second))
	eventually(t, func() bool { return len(readAllEvents(t, spool.cfg.SpoolDir)) == 2 }, "summary row")
	for _, e := range readAllEvents(t, spool.cfg.SpoolDir) {
		if e.SuppressedCount > 0 {
			assert.Equal(t, second.RequestID, e.RequestID)
			assert.Equal(t, 1, e.SuppressedCount)
		}
	}
}

// The unauthenticated token is refunded when its record was not written.
func TestPipelineFailedUnauthDenialRefundsToken(t *testing.T) {
	spool, rc := newTestSpoolControlled(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{UnauthRate: 1, UnauthBurst: 2},
		Committer: pipelineFast(),
	}, clk)

	rc.Set(0.96)
	for i := 0; i < 5; i++ {
		p.RecordDenial(pipelineDenial("anonymous", "anonymous", "", "UNAUTHORIZED", 401))
	}
	require.Empty(t, readAllEvents(t, spool.cfg.SpoolDir))
	assert.Equal(t, 0, rec.dropped("denial", "unauth_cap"), "failed writes must not drain the bucket")

	rc.Set(0.10)
	p.RecordDenial(pipelineDenial("anonymous", "anonymous", "", "UNAUTHORIZED", 401))
	p.RecordDenial(pipelineDenial("anonymous", "anonymous", "", "UNAUTHORIZED", 401))
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 2, "the full burst is still available after the outage")
}

func TestGovernorForgetClearsEmptyWindow(t *testing.T) {
	g := govNew(100)
	adm := g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0)
	require.Equal(t, Record, adm.Disposition)
	g.Forget(adm)
	assert.Equal(t, 0, g.keyCount())

	assert.Equal(t, Record, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(time.Second)).Disposition)
	assert.Empty(t, g.Sweep(govT0.Add(2*govWindow)), "no repeats were counted, so no summary")
}

func TestGovernorForgetKeepsConcurrentRepeatsCounted(t *testing.T) {
	g := govNew(100)
	first := govEvent("u1", "r1", "ROUTE_NOT_FOUND")
	adm := g.Admit(first, govT0)
	require.Equal(t, Record, adm.Disposition)
	// Two repeats arrive while the first write is still in flight.
	require.Equal(t, Suppress, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(time.Second)).Disposition)
	require.Equal(t, Suppress, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(2*time.Second)).Disposition)
	g.Forget(adm) // the first write failed

	retry := govEvent("u1", "r1", "ROUTE_NOT_FOUND")
	retry.RequestID = "retry-req"
	radm := g.Admit(retry, govT0.Add(3*time.Second))
	require.Equal(t, Record, radm.Disposition, "the next occurrence is recorded again")

	sums := g.Sweep(govT0.Add(2 * govWindow))
	require.Len(t, sums, 1)
	assert.Equal(t, 3, sums[0].SuppressedCount, "two suppressed repeats plus the lost first denial")
	assert.Equal(t, "retry-req", sums[0].RequestID, "the summary points at the row that was written")
}

func TestGovernorUnwrittenWindowSummaryDoesNotPointAtMissingRow(t *testing.T) {
	g := govNew(100)
	first := govEvent("u1", "r1", "ROUTE_NOT_FOUND")
	adm := g.Admit(first, govT0)
	g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(time.Second))
	g.Forget(adm)

	sums := g.Sweep(govT0.Add(2 * govWindow))
	require.Len(t, sums, 1)
	assert.Equal(t, 2, sums[0].SuppressedCount, "the repeat plus the denial whose row was lost")
	assert.NotEqual(t, first.RequestID, sums[0].RequestID, "the first request id was never written")
	assert.NotEmpty(t, sums[0].RequestID)
}

func TestGovernorForgetIgnoresStaleAdmission(t *testing.T) {
	g := govNew(100)
	old := g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0)
	// The window ended and a newer, successfully written window replaced it.
	fresh := g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(2*govWindow))
	require.Equal(t, Record, fresh.Disposition)
	g.Forget(old) // a late failure of the old write must not touch the new window
	assert.Equal(t, 1, g.keyCount())
	assert.Equal(t, Suppress, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(2*govWindow+time.Second)).Disposition)
}

func TestGovernorForgetOverflowAndBucket(t *testing.T) {
	g := govNew(16)
	for i := 0; i < 16; i++ {
		g.Admit(govEvent(string(rune('a'+i)), "r", "RATE_LIMIT_EXCEEDED"), govT0)
	}
	adm := g.Admit(govEvent("zz", "r", "RATE_LIMIT_EXCEEDED"), govT0)
	require.True(t, adm.Overflow)
	require.Equal(t, Record, adm.Disposition)
	g.Forget(adm)
	assert.Equal(t, 0, g.overflowCount())

	b := NewGovernor(GovernorConfig{UnauthRate: 1, UnauthBurst: 1})
	a1 := b.Admit(govAnon("UNAUTHORIZED"), govT0)
	require.Equal(t, Record, a1.Disposition)
	b.Forget(a1)
	assert.Equal(t, Record, b.Admit(govAnon("UNAUTHORIZED"), govT0).Disposition, "refunded token is usable")
	assert.Equal(t, Drop, b.Admit(govAnon("UNAUTHORIZED"), govT0).Disposition, "refund never exceeds the burst")
}
